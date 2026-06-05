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

	"example.com/llm-chat-web/internal/llm"
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
	if hashErr := bcrypt.CompareHashAndPassword(user.PasswordHash, []byte("correct horse")); hashErr != nil {
		t.Fatalf("stored password hash does not verify: %v", hashErr)
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
	if _, verifyErr := service.VerifyBrowserSession(ctx, anonymous.CookieValue); verifyErr != nil {
		t.Fatalf("VerifyBrowserSession anonymous error = %v, want nil", verifyErr)
	}

	rotated, err := service.RotateBrowserSession(ctx, anonymous.Session.ID, user.ID)
	if err != nil {
		t.Fatalf("RotateBrowserSession error = %v, want nil", err)
	}
	if rotated.CookieValue == anonymous.CookieValue || rotated.Session.ID == anonymous.Session.ID {
		t.Fatalf("rotated session did not change ID and secret")
	}
	if _, lookupErr := store.SessionByID(ctx, anonymous.Session.ID); !errors.Is(lookupErr, storage.ErrNotFound) {
		t.Fatalf("old SessionByID error = %v, want ErrNotFound", lookupErr)
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

func TestServiceDefaultsAndStoreFailures(t *testing.T) {
	ctx := context.Background()
	errStore := errors.New("store failed")

	t.Run("default clock", func(t *testing.T) {
		service := NewService(Options{
			Store: authTestStore{
				createUser: func(_ context.Context, params storage.CreateUserParams) (storage.User, error) {
					if params.CreatedAt.IsZero() {
						t.Fatalf("CreatedAt is zero, want default clock")
					}
					return storage.User{ID: 1, Username: params.Username, PasswordHash: params.PasswordHash, CreatedAt: params.CreatedAt}, nil
				},
			},
			BCryptCost: bcrypt.MinCost,
		})
		if _, err := service.Register(ctx, "alice", "correct horse"); err != nil {
			t.Fatalf("Register error = %v, want nil", err)
		}
	})

	t.Run("bcrypt failure", func(t *testing.T) {
		service := NewService(Options{Store: authTestStore{}, BCryptCost: bcrypt.MaxCost + 1})
		if _, err := service.Register(ctx, "alice", "correct horse"); err == nil {
			t.Fatalf("Register bcrypt error = nil, want error")
		}
	})

	t.Run("register store failure", func(t *testing.T) {
		service := NewService(Options{
			Store: authTestStore{
				createUser: func(context.Context, storage.CreateUserParams) (storage.User, error) {
					return storage.User{}, errStore
				},
			},
			BCryptCost: bcrypt.MinCost,
		})
		if _, err := service.Register(ctx, "alice", "correct horse"); !errors.Is(err, errStore) {
			t.Fatalf("Register error = %v, want store error", err)
		}
	})

	t.Run("authenticate store failure", func(t *testing.T) {
		service := NewService(Options{
			Store: authTestStore{
				userByUsername: func(context.Context, string) (storage.User, error) {
					return storage.User{}, errStore
				},
			},
			BCryptCost: bcrypt.MinCost,
		})
		if _, err := service.Authenticate(ctx, "alice", "correct horse"); !errors.Is(err, errStore) {
			t.Fatalf("Authenticate error = %v, want store error", err)
		}
	})

	t.Run("create session store failure", func(t *testing.T) {
		service := NewService(Options{
			Store: authTestStore{
				createSession: func(context.Context, storage.CreateSessionParams) (storage.Session, error) {
					return storage.Session{}, errStore
				},
			},
			BCryptCost: bcrypt.MinCost,
		})
		if _, err := service.CreateAnonymousBrowserSession(ctx); !errors.Is(err, errStore) {
			t.Fatalf("CreateAnonymousBrowserSession error = %v, want store error", err)
		}
	})

	t.Run("verify session store failure", func(t *testing.T) {
		service := NewService(Options{
			Store: authTestStore{
				sessionByID: func(context.Context, string) (storage.Session, error) {
					return storage.Session{}, errStore
				},
			},
			BCryptCost: bcrypt.MinCost,
		})
		if _, err := service.VerifyBrowserSession(ctx, "session.secret"); !errors.Is(err, errStore) {
			t.Fatalf("VerifyBrowserSession error = %v, want store error", err)
		}
	})
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

type authTestStore struct {
	createUser     func(context.Context, storage.CreateUserParams) (storage.User, error)
	userByUsername func(context.Context, string) (storage.User, error)
	createSession  func(context.Context, storage.CreateSessionParams) (storage.Session, error)
	sessionByID    func(context.Context, string) (storage.Session, error)
}

func (s authTestStore) Migrate(context.Context) error {
	return nil
}

func (s authTestStore) CreateUser(ctx context.Context, params storage.CreateUserParams) (storage.User, error) {
	if s.createUser != nil {
		return s.createUser(ctx, params)
	}
	return storage.User{}, nil
}

func (s authTestStore) UserByUsername(ctx context.Context, username string) (storage.User, error) {
	if s.userByUsername != nil {
		return s.userByUsername(ctx, username)
	}
	return storage.User{}, storage.ErrNotFound
}

func (s authTestStore) CreateSession(ctx context.Context, params storage.CreateSessionParams) (storage.Session, error) {
	if s.createSession != nil {
		return s.createSession(ctx, params)
	}
	return storage.Session{}, nil
}

func (s authTestStore) RotateSession(context.Context, string, storage.CreateSessionParams) (storage.Session, error) {
	return storage.Session{}, nil
}

func (s authTestStore) SessionByID(ctx context.Context, id string) (storage.Session, error) {
	if s.sessionByID != nil {
		return s.sessionByID(ctx, id)
	}
	return storage.Session{}, storage.ErrNotFound
}

func (s authTestStore) DeleteSession(context.Context, string) error {
	return nil
}

func (s authTestStore) DefaultConversationForUser(context.Context, int64) (storage.Conversation, error) {
	return storage.Conversation{}, nil
}

func (s authTestStore) Messages(context.Context, int64) ([]llm.Message, error) {
	return nil, nil
}

func (s authTestStore) AppendTurn(context.Context, int64, llm.Message, llm.Message) error {
	return nil
}

func (s authTestStore) Close() error {
	return nil
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
