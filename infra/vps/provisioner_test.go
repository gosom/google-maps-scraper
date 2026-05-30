package vps_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/gosom/google-maps-scraper/infra"
	"github.com/gosom/google-maps-scraper/infra/vps"
)

func TestCheckConnectivity_NoPrivateKey(t *testing.T) {
	t.Parallel()

	p, err := vps.New(
		vps.WithHost("127.0.0.1"),
		vps.WithPort("1222"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = p.CheckConnectivity(t.Context())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, infra.ErrConnectionFailed) {
		t.Errorf("expected ErrConnectionFailed, got: %v", err)
	}
}

func TestCheckConnectivity_InvalidPrivateKey(t *testing.T) {
	t.Parallel()

	p, err := vps.New(
		vps.WithHost("127.0.0.1"),
		vps.WithPort("1222"),
		vps.WithPrivateKey([]byte("not-a-valid-key")),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = p.CheckConnectivity(t.Context())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, infra.ErrConnectionFailed) {
		t.Errorf("expected ErrConnectionFailed, got: %v", err)
	}
}

func TestCheckConnectivity_WrongKey(t *testing.T) {
	t.Parallel()

	_, serverPubKey := generateTestKeyPair(t)

	addr, cleanup := startTestSSHServer(t, serverPubKey)
	defer cleanup()

	clientPrivPEM, _ := generateTestKeyPair(t)

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	p, err := vps.New(
		vps.WithHost(host),
		vps.WithPort(port),
		vps.WithUser("testuser"),
		vps.WithPrivateKey(clientPrivPEM),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = p.CheckConnectivity(t.Context())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, infra.ErrConnectionFailed) {
		t.Errorf("expected ErrConnectionFailed, got: %v", err)
	}
}

func TestCheckConnectivity_UnreachableHost(t *testing.T) {
	t.Parallel()
	privPEM, _ := generateTestKeyPair(t)

	p, err := vps.New(
		vps.WithHost("127.0.0.1"),
		vps.WithPort("1"),
		vps.WithUser("testuser"),
		vps.WithPrivateKey(privPEM),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = p.CheckConnectivity(t.Context())
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, infra.ErrConnectionFailed) {
		t.Errorf("expected ErrConnectionFailed, got: %v", err)
	}
}

func TestCheckConnectivity_Success(t *testing.T) {
	t.Parallel()

	privPEM, pubKey := generateTestKeyPair(t)

	addr, cleanup := startTestSSHServer(t, pubKey)
	defer cleanup()

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	p, err := vps.New(
		vps.WithHost(host),
		vps.WithPort(port),
		vps.WithUser("testuser"),
		vps.WithPrivateKey(privPEM),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := p.CheckConnectivity(t.Context()); err != nil {
		t.Errorf("CheckConnectivity should succeed, got: %v", err)
	}
}

func startTestSSHServer(t *testing.T, authorizedKey ssh.PublicKey) (addr string, cleanup func()) {
	t.Helper()

	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}

	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}

	config := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), authorizedKey.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("unauthorized")
		},
	}
	config.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func(c net.Conn) {
				defer func() { _ = c.Close() }()

				sshConn, chans, reqs, err := ssh.NewServerConn(c, config)
				if err != nil {
					return
				}

				defer func() { _ = sshConn.Close() }()

				go ssh.DiscardRequests(reqs)

				for ch := range chans {
					_ = ch.Reject(ssh.Prohibited, "no channels allowed")
				}
			}(conn)
		}
	}()

	return listener.Addr().String(), func() { _ = listener.Close() }
}

func generateTestKeyPair(t *testing.T) (privateKeyPEM []byte, pubKey ssh.PublicKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("create ssh public key: %v", err)
	}

	privBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}

	return pem.EncodeToMemory(privBlock), sshPub
}
