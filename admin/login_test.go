package admin_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/admin"
	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/ratelimit"
)

type loginStore struct {
	admin.IStore
	user admin.User
}

func (s *loginStore) GetUser(context.Context, string) (*admin.User, error) {
	return &s.user, nil
}

func (*loginStore) CreateSession(context.Context, int, string, string, time.Duration) (*admin.Session, error) {
	return &admin.Session{ID: "completed"}, nil
}

func (*loginStore) CreatePendingSession(context.Context, int, string, string, time.Duration) (*admin.Session, error) {
	return &admin.Session{ID: "awaiting-verification"}, nil
}

type loginLimiter struct{}

func (loginLimiter) Check(context.Context, string, int, time.Duration) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

func (loginLimiter) Reset(context.Context, string) error {
	return nil
}

func TestPasswordLoginIssuesExpectedSession(t *testing.T) {
	passwordHash, err := cryptoext.HashPassword("test-password")
	require.NoError(t, err)

	for _, tc := range []struct {
		name, cookie, value, location string
		requireTOTP                   bool
	}{
		{"password", admin.DefaultCookieName, "completed", "/admin/2fa/prompt", false},
		{"two factors", admin.DefaultCookieName + "_pending", "awaiting-verification", "/admin/login/2fa", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &loginStore{user: admin.User{ID: 1, Username: "admin", PasswordHash: passwordHash, TOTPEnabled: tc.requireTOTP}}
			state, err := admin.NewAppState(store, loginLimiter{}, nil)
			require.NoError(t, err)

			form := url.Values{"username": {"admin"}, "password": {"test-password"}}
			req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			rec := httptest.NewRecorder()
			admin.LoginSubmitHandler(state)(rec, req)

			res := rec.Result()
			defer res.Body.Close()

			require.Equal(t, http.StatusSeeOther, res.StatusCode)
			require.Equal(t, tc.location, res.Header.Get("Location"))
			require.Len(t, res.Cookies(), 1)
			require.Equal(t, tc.cookie, res.Cookies()[0].Name)
			require.Equal(t, tc.value, res.Cookies()[0].Value)
		})
	}
}
