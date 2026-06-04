package storage

import (
	"context"
	"errors"
	"time"

	"example.com/llm-chat-web/internal/llm"
)

var (
	ErrConflict        = errors.New("storage conflict")
	ErrInvalidArgument = errors.New("invalid storage argument")
	ErrNotFound        = errors.New("storage record not found")
)

type Store interface {
	Migrate(context.Context) error
	CreateUser(context.Context, CreateUserParams) (User, error)
	UserByUsername(context.Context, string) (User, error)
	CreateSession(context.Context, CreateSessionParams) (Session, error)
	RotateSession(context.Context, string, CreateSessionParams) (Session, error)
	SessionByID(context.Context, string) (Session, error)
	DeleteSession(context.Context, string) error
	DefaultConversationForUser(context.Context, int64) (Conversation, error)
	Messages(context.Context, int64) ([]llm.Message, error)
	AppendTurn(context.Context, int64, llm.Message, llm.Message) error
	Close() error
}

type User struct {
	ID           int64
	Username     string
	PasswordHash []byte
	CreatedAt    time.Time
}

type CreateUserParams struct {
	Username     string
	PasswordHash []byte
	CreatedAt    time.Time
}

type Session struct {
	ID         string
	UserID     int64
	SecretHash []byte
	CSRFToken  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
}

func (s Session) Authenticated() bool {
	return s.UserID > 0
}

type CreateSessionParams struct {
	ID         string
	UserID     int64
	SecretHash []byte
	CSRFToken  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
}

type Conversation struct {
	ID        int64
	UserID    int64
	Title     string
	IsDefault bool
	CreatedAt time.Time
}
