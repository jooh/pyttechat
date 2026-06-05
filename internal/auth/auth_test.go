package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/llm-chat-web/internal/storage"

	"golang.org/x/crypto/bcrypt"
)

func TestRegisterNormalizesHashesAndRejectsDuplicates(t *testing.T) {
	service, store := newTestService(t)
	ctx := context.Background()

	user, err := service.Register(ctx, "  Alice-1  ", "correct horse")
	if err != nil {
		t.Fatalf("Register error = %v, want nil", err)
	}
	if user.Username != "alice-1" {
		t.Fatalf("username = %q, want normalized alice-1", user.Username)
	}
	if string(user.PasswordHash) == "correct horse" {
		t.Fatalf("password hash stores raw password")
	}
	if err := bcrypt.CompareHashAndPassword(user.PasswordHash, []byte("correct horse")); err != nil {
		t.Fatalf("stored password hash does not verify: %v", err)
	}

	stored, err := store.UserByUsername(ctx, "alice-1")
	if err != nil {
		t.Fatalf("UserByUsername error = %v, want nil", err)
	}
	if stored.ID != user.ID || stored.Username != "alice-1" {
		t.Fatalf("stored user = %#v, want registered user", stored)
	}

	_, err = service.Register(ctx, "ALICE-1", "another password")
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate Register error = %v, want ErrUsernameTaken", err)
	}
}

func TestRegisterRejectsInvalidUsernamesAndWeakPasswords(t *testing.T) {
	service, _ := newTestService(t)
	for _, username := range []string{"ab", "-alice", "alice_", "ali ce", "ålice", strings.Repeat("a", 33)} {
		t.Run(username, func(t *testing.T) {
			_, err := service.Register(context.Background(), username, "correct horse")
			if !errors.Is(err, ErrInvalidUsername) {
				t.Fatalf("Register error = %v, want ErrInvalidUsername", err)
			}
		})
	}

	_, err := service.Register(context.Background(), "alice", "short")
	if !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password Register error = %v, want ErrWeakPassword", err)
	}
}

func TestAuthenticateRejectsWrongPasswordAndUnknownUser(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	if _, err := service.Register(ctx, "alice", "correct horse"); err != nil {
		t.Fatalf("Register error = %v, want nil", err)
	}

	if _, err := service.Authenticate(ctx, "ALICE", "correct horse"); err != nil {
		t.Fatalf("Authenticate normalized username error = %v, want nil", err)
	}
	for _, tc := range []struct {
		name     string
		username string
		password string
	}{
		{name: "wrong password", username: "alice", password: "wrong password"},
		{name: "missing user", username: "missing", password: "correct horse"},
		{name: "invalid username", username: "!!", password: "correct horse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Authenticate(ctx, tc.username, tc.password)
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Authenticate error = %v, want ErrInvalidCredentials", err)
			}
		})
	}
}

func TestBrowserSessionsRotateVerifyExpireAndLogout(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	service, store := newTestServiceWithClock(t, func() time.Time { return now })
	ctx := context.Background()
	user, err := service.Register(ctx, "alice", "correct horse")
	if err != nil {
		t.Fatalf("Register error = %v, want nil", err)
	}

	anonymous, err := service.CreateAnonymousBrowserSession(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBrowserSession error = %v, want nil", err)
	}
	if anonymous.Session.Authenticated() {
		t.Fatalf("anonymous session is authenticated")
	}
	if _, err := service.VerifyBrowserSession(ctx, anonymous.CookieValue); err != nil {
		t.Fatalf("VerifyBrowserSession anonymous error = %v, want nil", err)
	}

	rotated, err := service.RotateBrowserSession(ctx, anonymous.Session.ID, user.ID)
	if err != nil {
		t.Fatalf("RotateBrowserSession error = %v, want nil", err)
	}
	if rotated.CookieValue == anonymous.CookieValue || rotated.Session.ID == anonymous.Session.ID {
		t.Fatalf("rotated session did not change ID and secret")
	}
	if _, err := store.SessionByID(ctx, anonymous.Session.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("old SessionByID error = %v, want ErrNotFound", err)
	}

	verified, err := service.VerifyBrowserSession(ctx, rotated.CookieValue)
	if err != nil {
		t.Fatalf("VerifyBrowserSession rotated error = %v, want nil", err)
	}
	if verified.UserID != user.ID || verified.CSRFToken == "" {
		t.Fatalf("verified session = %#v, want user binding and csrf", verified)
	}
	if len(verified.SecretHash) != sha256.Size || strings.Contains(rotated.CookieValue, string(verified.SecretHash)) {
		t.Fatalf("cookie value appears to contain stored secret hash")
	}

	badCookie := rotated.CookieValue + "x"
	if _, err := service.VerifyBrowserSession(ctx, badCookie); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("bad secret VerifyBrowserSession error = %v, want ErrInvalidSession", err)
	}
	if _, err := service.VerifyBrowserSession(ctx, "malformed"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("malformed VerifyBrowserSession error = %v, want ErrInvalidSession", err)
	}

	service.now = func() time.Time { return now.Add(DefaultSessionTTL).Add(time.Second) }
	if _, err := service.VerifyBrowserSession(ctx, rotated.CookieValue); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired VerifyBrowserSession error = %v, want ErrSessionExpired", err)
	}

	if err := service.Logout(ctx, rotated.Session.ID); err != nil {
		t.Fatalf("Logout error = %v, want nil", err)
	}
	service.now = func() time.Time { return now }
	if _, err := service.VerifyBrowserSession(ctx, rotated.CookieValue); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("logged out VerifyBrowserSession error = %v, want ErrInvalidSession", err)
	}
}

func TestBrowserSessionRandomFailures(t *testing.T) {
	service, _ := newTestService(t)
	service.random = failingReader{}

	_, err := service.CreateAnonymousBrowserSession(context.Background())
	if err == nil {
		t.Fatalf("CreateAnonymousBrowserSession error = nil, want random failure")
	}
}

func TestServiceOptionsAndAuthenticatedBrowserSessions(t *testing.T) {
	defaults := NewService(Options{})
	if defaults.random == nil || defaults.now == nil {
		t.Fatalf("NewService defaults = %#v, want random reader and clock", defaults)
	}
	if defaults.sessionTTL != DefaultSessionTTL {
		t.Fatalf("default session TTL = %v, want %v", defaults.sessionTTL, DefaultSessionTTL)
	}
	if defaults.bcryptCost == 0 {
		t.Fatalf("default bcrypt cost = 0, want configured cost")
	}

	now := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	service, _ := newTestServiceWithClock(t, func() time.Time { return now })
	service.sessionTTL = time.Hour
	ctx := context.Background()
	user, err := service.Register(ctx, "session-user", "correct horse")
	if err != nil {
		t.Fatalf("Register error = %v, want nil", err)
	}

	browserSession, err := service.CreateAuthenticatedBrowserSession(ctx, user.ID)
	if err != nil {
		t.Fatalf("CreateAuthenticatedBrowserSession error = %v, want nil", err)
	}
	if browserSession.CookieValue == "" || browserSession.Session.UserID != user.ID || !browserSession.Session.Authenticated() {
		t.Fatalf("browser session = %#v, want authenticated user session", browserSession)
	}
	if !browserSession.Session.ExpiresAt.Equal(now.Add(time.Hour)) || !browserSession.Session.CreatedAt.Equal(now) {
		t.Fatalf("browser session timestamps = %s/%s, want %s/%s", browserSession.Session.CreatedAt, browserSession.Session.ExpiresAt, now, now.Add(time.Hour))
	}
	if _, err := service.VerifyBrowserSession(ctx, browserSession.CookieValue); err != nil {
		t.Fatalf("VerifyBrowserSession authenticated error = %v, want nil", err)
	}

	if _, err := service.CreateAuthenticatedBrowserSession(ctx, 0); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("CreateAuthenticatedBrowserSession invalid user error = %v, want ErrInvalidArgument", err)
	}
	if _, err := service.RotateBrowserSession(ctx, browserSession.Session.ID, 0); !errors.Is(err, storage.ErrInvalidArgument) {
		t.Fatalf("RotateBrowserSession invalid user error = %v, want ErrInvalidArgument", err)
	}
}

func TestBrowserSessionRandomFailurePositions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bytes int
	}{
		{name: "secret token", bytes: 32},
		{name: "csrf token", bytes: 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _ := newTestService(t)
			service.random = strings.NewReader(strings.Repeat("x", tc.bytes))

			_, err := service.CreateAnonymousBrowserSession(context.Background())
			if err == nil {
				t.Fatalf("CreateAnonymousBrowserSession error = nil, want random failure")
			}
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func newTestService(t *testing.T) (*Service, storage.Store) {
	t.Helper()
	return newTestServiceWithClock(t, func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	})
}

func newTestServiceWithClock(t *testing.T, now func() time.Time) (*Service, storage.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pyttechat.db")
	store, err := storage.OpenSQLite(context.Background(), "sqlite://"+path)
	if err != nil {
		t.Fatalf("OpenSQLite error = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close error = %v", err)
		}
	})
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate error = %v, want nil", err)
	}
	return NewService(Options{
		Store:      store,
		Now:        now,
		BCryptCost: bcrypt.MinCost,
	}), store
}
