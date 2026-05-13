package admin

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"

	"github.com/gosom/google-maps-scraper/infra"
	"github.com/gosom/google-maps-scraper/log"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(_ *http.Request) bool {
		// Same-origin enforced by session cookie + SameSite
		return true
	},
}

// TerminalPageHandler renders the terminal page for a worker.
func TerminalPageHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		idStr := chi.URLParam(r, "id")

		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Redirect(w, r, "/admin/workers?error=Invalid+worker+ID", http.StatusSeeOther)
			return
		}

		resource, err := appState.Store.GetProvisionedResource(r.Context(), id)
		if err != nil {
			http.Redirect(w, r, "/admin/workers?error=Worker+not+found", http.StatusSeeOther)
			return
		}

		if resource.IPAddress == "" {
			http.Redirect(w, r, "/admin/workers?error=Worker+has+no+IP+address", http.StatusSeeOther)
			return
		}

		data := map[string]any{
			"WorkerID":   resource.ID,
			"WorkerName": resource.Name,
			"WorkerIP":   resource.IPAddress,
		}

		renderTemplate(appState, w, r, "terminal.html", data)
	}
}

// terminalSize is sent as binary WebSocket message for resize events.
type terminalSize struct {
	High  int `json:"high"`
	Width int `json:"width"`
}

// TerminalWSHandler handles the WebSocket connection for a worker terminal.
//
//nolint:gocyclo // complexity inherent in WebSocket + SSH proxy with authentication and I/O multiplexing
func TerminalWSHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		idStr := chi.URLParam(r, "id")

		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, "Invalid worker ID", http.StatusBadRequest)
			return
		}

		resource, err := appState.Store.GetProvisionedResource(r.Context(), id)
		if err != nil || resource.IPAddress == "" {
			http.Error(w, "Worker not found or has no IP", http.StatusNotFound)
			return
		}

		// Get SSH private key from app_config
		sshKeyCfg, err := appState.Store.GetConfig(r.Context(), AppKeyPairKey)
		if err != nil || sshKeyCfg == nil || sshKeyCfg.Value == "" {
			http.Error(w, "SSH key not configured", http.StatusInternalServerError)
			return
		}

		var sshKey infra.SSHKey
		if err := json.Unmarshal([]byte(sshKeyCfg.Value), &sshKey); err != nil {
			log.Error("failed to parse SSH key", "error", err)
			http.Error(w, "Failed to read SSH key", http.StatusInternalServerError)

			return
		}

		signer, err := ssh.ParsePrivateKey([]byte(sshKey.Key))
		if err != nil {
			log.Error("failed to parse SSH private key", "error", err)
			http.Error(w, "Invalid SSH key", http.StatusInternalServerError)

			return
		}

		// Upgrade to WebSocket
		wsConn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Error("websocket upgrade failed", "error", err)
			return
		}

		defer func() { _ = wsConn.Close() }() // safety net for early returns

		// Read initial terminal size (binary message)
		msgType, msg, err := wsConn.ReadMessage()
		if err != nil {
			log.Error("failed to read initial terminal size", "error", err)
			return
		}

		var size terminalSize
		if msgType == websocket.BinaryMessage {
			if err := json.Unmarshal(msg, &size); err != nil {
				log.Error("failed to parse terminal size", "error", err)
				return
			}
		}

		if size.High <= 0 {
			size.High = 24
		}

		if size.Width <= 0 {
			size.Width = 80
		}

		hostPort := net.JoinHostPort(resource.IPAddress, "2222")

		hostKeyCallback, err := infra.NewTOFUHostKeyCallback(hostPort)
		if err != nil {
			log.Error("failed to initialize SSH host key callback", "error", err, "ip", resource.IPAddress)
			_ = wsConn.WriteMessage(websocket.TextMessage, []byte("\r\nFailed to initialize SSH verification: "+err.Error()+"\r\n"))

			return
		}

		// Connect to worker via SSH
		sshConfig := &ssh.ClientConfig{
			User: "root",
			Auth: []ssh.AuthMethod{
				ssh.PublicKeys(signer),
			},
			HostKeyCallback: hostKeyCallback,
			Timeout:         30 * time.Second,
		}

		sshClient, err := ssh.Dial("tcp", hostPort, sshConfig)
		if err != nil {
			log.Error("SSH dial failed", "error", err, "ip", resource.IPAddress)
			_ = wsConn.WriteMessage(websocket.TextMessage, []byte("\r\nFailed to connect to worker: "+err.Error()+"\r\n"))

			return
		}

		defer func() { _ = sshClient.Close() }()

		sshSession, err := sshClient.NewSession()
		if err != nil {
			log.Error("SSH session failed", "error", err)
			_ = wsConn.WriteMessage(websocket.TextMessage, []byte("\r\nFailed to create SSH session: "+err.Error()+"\r\n"))

			return
		}

		defer func() { _ = sshSession.Close() }()

		// Request PTY
		modes := ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}

		if err := sshSession.RequestPty("xterm", size.High, size.Width, modes); err != nil {
			log.Error("PTY request failed", "error", err)
			_ = wsConn.WriteMessage(websocket.TextMessage, []byte("\r\nFailed to request PTY: "+err.Error()+"\r\n"))

			return
		}

		stdinPipe, err := sshSession.StdinPipe()
		if err != nil {
			log.Error("failed to get stdin pipe", "error", err)
			return
		}

		stdoutPipe, err := sshSession.StdoutPipe()
		if err != nil {
			log.Error("failed to get stdout pipe", "error", err)
			return
		}

		if err := sshSession.Shell(); err != nil {
			log.Error("failed to start shell", "error", err)
			_ = wsConn.WriteMessage(websocket.TextMessage, []byte("\r\nFailed to start shell: "+err.Error()+"\r\n"))

			return
		}

		var wg sync.WaitGroup

		wg.Add(2)

		// wsRead: WebSocket -> SSH stdin
		go func() {
			defer wg.Done()
			defer func() { _ = stdinPipe.Close() }()

			for {
				msgType, msg, err := wsConn.ReadMessage()
				if err != nil {
					return
				}

				switch msgType {
				case websocket.TextMessage:
					if _, err := stdinPipe.Write(msg); err != nil {
						return
					}
				case websocket.BinaryMessage:
					var resize terminalSize
					if err := json.Unmarshal(msg, &resize); err == nil && resize.High > 0 && resize.Width > 0 {
						_ = sshSession.WindowChange(resize.High, resize.Width)
					}
				}
			}
		}()

		// wsWrite: SSH stdout -> WebSocket
		go func() {
			defer wg.Done()

			buf := make([]byte, 8192)

			for {
				n, err := stdoutPipe.Read(buf)
				if err != nil {
					if err != io.EOF {
						log.Debug("SSH stdout read error", "error", err)
					}

					_ = wsConn.WriteMessage(websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))

					return
				}

				if n > 0 {
					if err := wsConn.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
						return
					}
				}
			}
		}()

		// Wait for SSH session to end, then close WebSocket to unblock wsRead
		_ = sshSession.Wait()
		_ = wsConn.Close()

		wg.Wait()

		log.Info("terminal session ended", "worker_id", id, "ip", resource.IPAddress)
	}
}
