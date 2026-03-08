package admin

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/log"
)

// pendingBackupCodesKey returns the config key for storing pending backup codes.
func pendingBackupCodesKey(userID int) string {
	return fmt.Sprintf("pending_backup_codes:%d", userID)
}

// TwoFactorVerifyPageHandler renders the 2FA verification page during login.
func TwoFactorVerifyPageHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(appState.CookieName + "_pending")
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		_, err = appState.Store.GetSession(r.Context(), cookie.Value)
		if err != nil {
			http.Redirect(w, r, "/admin/login?error=Session+expired", http.StatusSeeOther)
			return
		}

		data := map[string]any{
			"Error": r.URL.Query().Get("error"),
		}
		renderTemplate(appState, w, r, "2fa_verify.html", data)
	}
}

// TwoFactorVerifySubmitHandler handles 2FA code verification during login.
func TwoFactorVerifySubmitHandler(appState *AppState) http.HandlerFunc {
	const (
		max2FAAttempts  = 5
		twoFARateWindow = 5 * time.Minute
	)

	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/admin/login/2fa?error=Invalid+form", http.StatusSeeOther)
			return
		}

		pendingCookie, err := r.Cookie(appState.CookieName + "_pending")
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		pendingSession, err := appState.Store.GetSession(r.Context(), pendingCookie.Value)
		if err != nil {
			http.Redirect(w, r, "/admin/login?error=Session+expired", http.StatusSeeOther)
			return
		}

		user, err := appState.Store.GetUserByID(r.Context(), pendingSession.UserID)
		if err != nil {
			http.Redirect(w, r, "/admin/login?error=User+not+found", http.StatusSeeOther)
			return
		}

		// Check rate limit before attempting 2FA verification
		rateLimitKey := "2fa:" + strconv.Itoa(user.ID)

		result, err := appState.RateLimiter.Check(r.Context(), rateLimitKey, max2FAAttempts, twoFARateWindow)
		if err != nil {
			http.Redirect(w, r, "/admin/login/2fa?error=Internal+error", http.StatusSeeOther)
			return
		}

		if !result.Allowed {
			remaining := time.Until(result.ResetAt).Round(time.Minute)
			errorMsg := fmt.Sprintf("Too+many+attempts.+Try+again+in+%d+minutes", int(remaining.Minutes()))
			http.Redirect(w, r, "/admin/login/2fa?error="+errorMsg, http.StatusSeeOther)

			return
		}

		code := r.FormValue("code")
		valid := false

		secret, err := appState.Store.GetTOTPSecret(r.Context(), user.ID)
		if err == nil && secret != "" {
			if totp.Validate(code, secret) {
				valid = true
			}
		}

		if !valid && strings.Contains(code, "-") {
			if ok, _ := appState.Store.ValidateBackupCode(r.Context(), user.ID, code); ok {
				valid = true
			}
		}

		if !valid {
			http.Redirect(w, r, "/admin/login/2fa?error=Invalid+code", http.StatusSeeOther)
			return
		}

		// Reset both rate limits on successful 2FA verification (login is now complete)
		_ = appState.RateLimiter.Reset(r.Context(), rateLimitKey)
		_ = appState.RateLimiter.Reset(r.Context(), "login:"+user.Username)

		_ = appState.Store.DeleteSession(r.Context(), pendingCookie.Value)

		http.SetCookie(w, &http.Cookie{
			Name:     appState.CookieName + "_pending",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   isSecureRequest(r),
		})

		ipAddress := r.RemoteAddr
		userAgent := r.UserAgent()

		session, err := appState.Store.CreateSession(r.Context(), user.ID, ipAddress, userAgent, 24*time.Hour)
		if err != nil {
			http.Redirect(w, r, "/admin/login?error=Failed+to+create+session", http.StatusSeeOther)
			return
		}

		log.Info("audit", "action", "login_2fa", "user_id", user.ID, "ip", ipAddress)

		http.SetCookie(w, &http.Cookie{
			Name:     appState.CookieName,
			Value:    session.ID,
			Path:     "/",
			HttpOnly: true,
			Secure:   isSecureRequest(r),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400,
		})

		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	}
}

// TwoFactorPromptPageHandler renders the 2FA prompt page for users without 2FA enabled.
func TwoFactorPromptPageHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		user, err := appState.Store.GetUserByID(r.Context(), session.UserID)
		if err != nil {
			http.Redirect(w, r, "/admin/login?error=User+not+found", http.StatusSeeOther)
			return
		}

		// If 2FA is already enabled, redirect to dashboard
		if user.TOTPEnabled {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		renderTemplate(appState, w, r, "2fa_prompt.html", nil)
	}
}

// TwoFactorSetupPageHandler renders the 2FA setup page.
func TwoFactorSetupPageHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		user, err := appState.Store.GetUserByID(r.Context(), session.UserID)
		if err != nil {
			http.Redirect(w, r, "/admin?error=User+not+found", http.StatusSeeOther)
			return
		}

		// If 2FA is already enabled, show the disable option
		if user.TOTPEnabled {
			data := map[string]any{
				"TOTPEnabled": true,
				"Success":     r.URL.Query().Get("success"),
				"Error":       r.URL.Query().Get("error"),
			}
			renderTemplate(appState, w, r, "2fa_setup.html", data)

			return
		}

		step := r.URL.Query().Get("step")

		// Step: Show backup codes (retrieved from server-side storage)
		if step == "backup" {
			cfg, err := appState.Store.GetConfig(r.Context(), pendingBackupCodesKey(user.ID))
			if err != nil || cfg == nil {
				http.Redirect(w, r, "/admin/2fa/setup", http.StatusSeeOther)
				return
			}

			data := map[string]any{
				"TOTPEnabled": false,
				"Step":        "backup",
				"BackupCodes": splitBackupCodes(cfg.Value),
				"Error":       r.URL.Query().Get("error"),
			}
			renderTemplate(appState, w, r, "2fa_setup.html", data)

			return
		}

		// Step: Show password verification form before starting setup
		if step != "qr" {
			data := map[string]any{
				"TOTPEnabled": false,
				"Step":        "password",
				"Error":       r.URL.Query().Get("error"),
			}
			renderTemplate(appState, w, r, "2fa_setup.html", data)

			return
		}

		// Step: Show QR code (only accessible after password verification via POST)
		// The TOTP secret should already be set by the POST handler
		secret, err := appState.Store.GetTOTPSecret(r.Context(), user.ID)
		if err != nil || secret == "" {
			http.Redirect(w, r, "/admin/2fa/setup", http.StatusSeeOther)
			return
		}

		key, err := totp.Generate(totp.GenerateOpts{
			Issuer:      "Google Maps Scraper Pro",
			AccountName: user.Username,
			Secret:      []byte(secret),
		})
		if err != nil {
			// Regenerate if we can't use the existing secret
			key, err = totp.Generate(totp.GenerateOpts{
				Issuer:      "Google Maps Scraper Pro",
				AccountName: user.Username,
			})
			if err != nil {
				http.Redirect(w, r, "/admin?error=Failed+to+generate+2FA", http.StatusSeeOther)
				return
			}
		}

		qrCode, err := cryptoext.GenerateQRCode(key.URL(), 256)
		if err != nil {
			http.Redirect(w, r, "/admin?error=Failed+to+generate+QR+code", http.StatusSeeOther)
			return
		}

		qrCodeBase64 := base64.StdEncoding.EncodeToString(qrCode)

		data := map[string]any{
			"TOTPEnabled": false,
			"Step":        "qr",
			"QRCode":      qrCodeBase64,
			"Secret":      secret,
			"Error":       r.URL.Query().Get("error"),
		}
		renderTemplate(appState, w, r, "2fa_setup.html", data)
	}
}

// TwoFactorSetupSubmitHandler handles enabling 2FA after verification.
func TwoFactorSetupSubmitHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/admin/2fa/setup?error=Invalid+form", http.StatusSeeOther)
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		user, err := appState.Store.GetUserByID(r.Context(), session.UserID)
		if err != nil {
			http.Redirect(w, r, "/admin/2fa/setup?error=User+not+found", http.StatusSeeOther)
			return
		}

		action := r.FormValue("action")

		// Step 1: Verify password before starting 2FA setup
		if action == "verify_password" {
			password := r.FormValue("password")
			if err := validatePassword(user, password); err != nil {
				http.Redirect(w, r, "/admin/2fa/setup?error=Invalid+password", http.StatusSeeOther)
				return
			}

			// Password verified, generate and store TOTP secret
			key, err := totp.Generate(totp.GenerateOpts{
				Issuer:      "Google Maps Scraper Pro",
				AccountName: user.Username,
			})
			if err != nil {
				http.Redirect(w, r, "/admin/2fa/setup?error=Failed+to+generate+2FA+secret", http.StatusSeeOther)
				return
			}

			if err := appState.Store.SetTOTPSecret(r.Context(), user.ID, key.Secret()); err != nil {
				http.Redirect(w, r, "/admin/2fa/setup?error=Failed+to+save+2FA+secret", http.StatusSeeOther)
				return
			}

			http.Redirect(w, r, "/admin/2fa/setup?step=qr", http.StatusSeeOther)

			return
		}

		// Step 2: Verify TOTP code and generate backup codes
		if action == "verify_totp" {
			secret, err := appState.Store.GetTOTPSecret(r.Context(), user.ID)
			if err != nil || secret == "" {
				http.Redirect(w, r, "/admin/2fa/setup?error=2FA+not+configured", http.StatusSeeOther)
				return
			}

			code := r.FormValue("code")
			if !totp.Validate(code, secret) {
				http.Redirect(w, r, "/admin/2fa/setup?step=qr&error=Invalid+code.+Please+try+again.", http.StatusSeeOther)
				return
			}

			// Generate backup codes and store server-side
			backupCodes, err := cryptoext.GenerateBackupCodes(10)
			if err != nil {
				http.Redirect(w, r, "/admin/2fa/setup?error=Failed+to+generate+backup+codes", http.StatusSeeOther)
				return
			}

			// Store backup codes encrypted in config (temporary storage)
			cfg := &AppConfig{
				Key:   pendingBackupCodesKey(user.ID),
				Value: strings.Join(backupCodes, ","),
			}
			if err := appState.Store.SetConfig(r.Context(), cfg, true); err != nil {
				http.Redirect(w, r, "/admin/2fa/setup?error=Failed+to+save+backup+codes", http.StatusSeeOther)
				return
			}

			http.Redirect(w, r, "/admin/2fa/setup?step=backup", http.StatusSeeOther)

			return
		}

		// Step 3: Confirm backup codes saved and enable 2FA
		if action == "confirm_backup" {
			confirmed := r.FormValue("confirmed")
			if confirmed != "yes" {
				http.Redirect(w, r, "/admin/2fa/setup?step=backup&error=Please+confirm+you+saved+the+codes", http.StatusSeeOther)
				return
			}

			// Retrieve backup codes from server-side storage
			cfg, err := appState.Store.GetConfig(r.Context(), pendingBackupCodesKey(user.ID))
			if err != nil || cfg == nil {
				http.Redirect(w, r, "/admin/2fa/setup?error=Backup+codes+expired.+Please+start+over.", http.StatusSeeOther)
				return
			}

			codes := strings.Split(cfg.Value, ",")

			hashedCodes := cryptoext.HashBackupCodes(codes)
			if err := appState.Store.SetBackupCodes(r.Context(), session.UserID, hashedCodes); err != nil {
				http.Redirect(w, r, "/admin/2fa/setup?error=Failed+to+save+backup+codes", http.StatusSeeOther)
				return
			}

			if err := appState.Store.EnableTOTP(r.Context(), session.UserID); err != nil {
				http.Redirect(w, r, "/admin/2fa/setup?error=Failed+to+enable+2FA", http.StatusSeeOther)
				return
			}

			// Clean up temporary backup codes storage
			_ = appState.Store.DeleteConfig(r.Context(), pendingBackupCodesKey(user.ID))

			log.Info("audit", "action", "2fa_enable", "user_id", session.UserID, "ip", r.RemoteAddr)

			http.Redirect(w, r, "/admin/settings?success=Two-factor+authentication+enabled", http.StatusSeeOther)

			return
		}

		// Unknown action
		http.Redirect(w, r, "/admin/2fa/setup", http.StatusSeeOther)
	}
}

// TwoFactorDisableHandler handles disabling 2FA.
func TwoFactorDisableHandler(appState *AppState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/admin/2fa/setup?error=Invalid+form", http.StatusSeeOther)
			return
		}

		session := SessionFromContext(r.Context())
		if session == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}

		password := r.FormValue("password")

		user, err := appState.Store.GetUserByID(r.Context(), session.UserID)
		if err != nil {
			http.Redirect(w, r, "/admin/2fa/setup?error=User+not+found", http.StatusSeeOther)
			return
		}

		if err := validatePassword(user, password); err != nil {
			http.Redirect(w, r, "/admin/2fa/setup?error=Invalid+password", http.StatusSeeOther)
			return
		}

		if err := appState.Store.DisableTOTP(r.Context(), session.UserID); err != nil {
			http.Redirect(w, r, "/admin/settings?error=Failed+to+disable+2FA", http.StatusSeeOther)
			return
		}

		log.Info("audit", "action", "2fa_disable", "user_id", session.UserID, "ip", r.RemoteAddr)

		http.Redirect(w, r, "/admin/settings?success=Two-factor+authentication+disabled", http.StatusSeeOther)
	}
}
