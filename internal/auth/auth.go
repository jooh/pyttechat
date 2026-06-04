package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"example.com/llm-chat-web/internal/storage"

	"golang.org/x/crypto/bcrypt"
)

const (
	DefaultSessionTTL     = 720 * time.Hour
	MinimumPasswordLength = 8
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidSession     = errors.New("invalid session")
	ErrInvalidUsername    = errors.New("invalid username")
	ErrSessionExpired     = errors.New("session expired")
	ErrUsernameTaken      = errors.New("username already exists")
	ErrWeakPassword       = errors.New("password is too short")
)

type Service struct {
	store      storage.Store
	random     io.Reader
	now        func() time.Time
	sessionTTL time.Duration
	bcryptCost int
}

type Options struct {
	Store      storage.Store
	Random     io.Reader
	Now        func() time.Time
	SessionTTL time.Duration
	BCryptCost int
}

type BrowserSession struct {
	CookieValue string
	Session     storage.Session
}

func NewService(opts Options) *Service {
	randomReader := opts.Random
	if randomReader == nil {
		randomReader = rand.Reader
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	sessionTTL := opts.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = DefaultSessionTTL
	}
	bcryptCost := opts.BCryptCost
	if bcryptCost == 0 {
		bcryptCost = bcrypt.DefaultCost
	}
	return &Service{
		store:      opts.Store,
		random:     randomReader,
		now:        now,
		sessionTTL: sessionTTL,
		bcryptCost: bcryptCost,
	}
}

func NormalizeUsername(username string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(username))
	if len(normalized) < 3 || len(normalized) > 32 {
		return "", ErrInvalidUsername
	}
	for i, char := range normalized {
		ok := char >= 'a' && char <= 'z' ||
			char >= '0' && char <= '9' ||
			char == '.' || char == '_' || char == '-'
		if !ok {
			return "", ErrInvalidUsername
		}
		if i == 0 || i == len(normalized)-1 {
			if char == '.' || char == '_' || char == '-' {
				return "", ErrInvalidUsername
			}
		}
	}
	return normalized, nil
}

func (s *Service) Register(ctx context.Context, username, password string) (storage.User, error) {
	normalized, err := NormalizeUsername(username)
	if err != nil {
		return storage.User{}, err
	}
	if len(password) < MinimumPasswordLength {
		return storage.User{}, ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return storage.User{}, err
	}
	user, err := s.store.CreateUser(ctx, storage.CreateUserParams{
		Username:     normalized,
		PasswordHash: hash,
		CreatedAt:    s.now().UTC(),
	})
	if errors.Is(err, storage.ErrConflict) {
		return storage.User{}, ErrUsernameTaken
	}
	if err != nil {
		return storage.User{}, err
	}
	return user, nil
}

func (s *Service) Authenticate(ctx context.Context, username, password string) (storage.User, error) {
	normalized, err := NormalizeUsername(username)
	if err != nil {
		return storage.User{}, ErrInvalidCredentials
	}
	user, err := s.store.UserByUsername(ctx, normalized)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return storage.User{}, err
	}
	if err := bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password)); err != nil {
		return storage.User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (s *Service) CreateAnonymousBrowserSession(ctx context.Context) (BrowserSession, error) {
	return s.createBrowserSession(ctx, 0, "")
}

func (s *Service) CreateAuthenticatedBrowserSession(ctx context.Context, userID int64) (BrowserSession, error) {
	if userID <= 0 {
		return BrowserSession{}, storage.ErrInvalidArgument
	}
	return s.createBrowserSession(ctx, userID, "")
}

func (s *Service) RotateBrowserSession(ctx context.Context, oldSessionID string, userID int64) (BrowserSession, error) {
	if userID <= 0 {
		return BrowserSession{}, storage.ErrInvalidArgument
	}
	return s.createBrowserSession(ctx, userID, oldSessionID)
}

func (s *Service) createBrowserSession(ctx context.Context, userID int64, oldSessionID string) (BrowserSession, error) {
	id, err := randomToken(s.random, "sess")
	if err != nil {
		return BrowserSession{}, err
	}
	secret, err := randomToken(s.random, "secret")
	if err != nil {
		return BrowserSession{}, err
	}
	csrf, err := randomToken(s.random, "csrf")
	if err != nil {
		return BrowserSession{}, err
	}
	now := s.now().UTC()
	params := storage.CreateSessionParams{
		ID:         id,
		UserID:     userID,
		SecretHash: secretHash(secret),
		CSRFToken:  csrf,
		ExpiresAt:  now.Add(s.sessionTTL),
		CreatedAt:  now,
	}
	var session storage.Session
	if oldSessionID == "" {
		session, err = s.store.CreateSession(ctx, params)
	} else {
		session, err = s.store.RotateSession(ctx, oldSessionID, params)
	}
	if err != nil {
		return BrowserSession{}, err
	}
	return BrowserSession{
		CookieValue: id + "." + secret,
		Session:     session,
	}, nil
}

func (s *Service) VerifyBrowserSession(ctx context.Context, cookieValue string) (storage.Session, error) {
	sessionID, secret, ok := strings.Cut(strings.TrimSpace(cookieValue), ".")
	if !ok || sessionID == "" || secret == "" {
		return storage.Session{}, ErrInvalidSession
	}
	session, err := s.store.SessionByID(ctx, sessionID)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Session{}, ErrInvalidSession
	}
	if err != nil {
		return storage.Session{}, err
	}
	if !s.now().UTC().Before(session.ExpiresAt) {
		return storage.Session{}, ErrSessionExpired
	}
	got := secretHash(secret)
	if subtle.ConstantTimeCompare(got, session.SecretHash) != 1 {
		return storage.Session{}, ErrInvalidSession
	}
	return session, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.store.DeleteSession(ctx, sessionID)
}

func secretHash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func randomToken(random io.Reader, prefix string) (string, error) {
	var raw [32]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s", prefix, base64.RawURLEncoding.EncodeToString(raw[:])), nil
}
