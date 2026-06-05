package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"example.com/llm-chat-web/internal/llm"

	_ "modernc.org/sqlite"
)

const DefaultDatabaseURL = "sqlite://./.cache/pyttechat/pyttechat.db"

var (
	sqliteDriverName = "sqlite"
	sqliteOpen       = sql.Open
)

type SQLite struct {
	db *sql.DB
}

func OpenSQLite(ctx context.Context, databaseURL string) (*SQLite, error) {
	dsn, err := sqliteDSN(databaseURL)
	if err != nil {
		return nil, err
	}
	if err := ensureSQLiteDir(dsn); err != nil {
		return nil, err
	}
	db, err := sqliteOpen(sqliteDriverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &SQLite{db: db}
	if _, err := db.ExecContext(ctx, `
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func NewSQLiteForDB(db *sql.DB) *SQLite {
	db.SetMaxOpenConns(1)
	return &SQLite{db: db}
}

func sqliteDSN(databaseURL string) (string, error) {
	value := strings.TrimSpace(databaseURL)
	if value == "" {
		value = DefaultDatabaseURL
	}
	if !strings.HasPrefix(value, "sqlite://") {
		return "", fmt.Errorf("%w: database URL must start with sqlite://", ErrInvalidArgument)
	}
	dsn := strings.TrimPrefix(value, "sqlite://")
	if dsn == "" {
		return "", fmt.Errorf("%w: sqlite database path is required", ErrInvalidArgument)
	}
	if strings.HasPrefix(dsn, "/") {
		return dsn, nil
	}
	return dsn, nil
}

func ensureSQLiteDir(dsn string) error {
	if dsn == ":memory:" || strings.HasPrefix(dsn, "file:") {
		return nil
	}
	dir := filepath.Dir(dsn)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

func (s *SQLite) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER NOT NULL
);

INSERT INTO schema_version (version)
SELECT 1
WHERE NOT EXISTS (SELECT 1 FROM schema_version);

UPDATE schema_version SET version = 1 WHERE version < 1;

CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE,
	password_hash BLOB NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS web_sessions (
	id TEXT PRIMARY KEY,
	user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
	secret_hash BLOB NOT NULL,
	csrf_token TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS web_sessions_user_id_idx ON web_sessions(user_id);
CREATE INDEX IF NOT EXISTS web_sessions_expires_at_idx ON web_sessions(expires_at);

CREATE TABLE IF NOT EXISTS conversations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	title TEXT NOT NULL,
	is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
	created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS conversations_one_default_per_user_idx
	ON conversations(user_id)
	WHERE is_default = 1;

CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	sequence INTEGER NOT NULL,
	role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
	parts_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE (conversation_id, sequence)
);

CREATE INDEX IF NOT EXISTS messages_conversation_sequence_idx
	ON messages(conversation_id, sequence);
`)
	return err
}

func (s *SQLite) CreateUser(ctx context.Context, params CreateUserParams) (User, error) {
	if strings.TrimSpace(params.Username) == "" || len(params.PasswordHash) == 0 {
		return User{}, ErrInvalidArgument
	}
	createdAt := nonZeroUTC(params.CreatedAt)
	result, err := s.db.ExecContext(ctx, `
INSERT INTO users (username, password_hash, created_at)
VALUES (?, ?, ?)
`, params.Username, cloneBytes(params.PasswordHash), formatTime(createdAt))
	if err != nil {
		if sqliteIsConstraint(err) {
			return User{}, ErrConflict
		}
		return User{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return User{
		ID:           id,
		Username:     params.Username,
		PasswordHash: cloneBytes(params.PasswordHash),
		CreatedAt:    createdAt,
	}, nil
}

func (s *SQLite) UserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
SELECT id, username, password_hash, created_at
FROM users
WHERE username = ?
`, username).Scan(&user.ID, &user.Username, &user.PasswordHash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	user.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return User{}, err
	}
	user.PasswordHash = cloneBytes(user.PasswordHash)
	return user, nil
}

func (s *SQLite) CreateSession(ctx context.Context, params CreateSessionParams) (Session, error) {
	return s.createSession(ctx, nil, params)
}

func (s *SQLite) RotateSession(ctx context.Context, oldSessionID string, params CreateSessionParams) (Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer rollback(tx)

	if strings.TrimSpace(oldSessionID) != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM web_sessions WHERE id = ?`, oldSessionID); err != nil {
			return Session{}, err
		}
	}
	session, err := s.createSession(ctx, tx, params)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *SQLite) createSession(ctx context.Context, tx *sql.Tx, params CreateSessionParams) (Session, error) {
	if strings.TrimSpace(params.ID) == "" || len(params.SecretHash) == 0 || strings.TrimSpace(params.CSRFToken) == "" {
		return Session{}, ErrInvalidArgument
	}
	createdAt := nonZeroUTC(params.CreatedAt)
	expiresAt := nonZeroUTC(params.ExpiresAt)
	var userID any
	if params.UserID > 0 {
		userID = params.UserID
	}
	exec := execer(s.db)
	if tx != nil {
		exec = tx
	}
	_, err := exec.ExecContext(ctx, `
INSERT INTO web_sessions (id, user_id, secret_hash, csrf_token, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?)
`, params.ID, userID, cloneBytes(params.SecretHash), params.CSRFToken, formatTime(expiresAt), formatTime(createdAt))
	if err != nil {
		if sqliteIsConstraint(err) {
			return Session{}, ErrConflict
		}
		return Session{}, err
	}
	return Session{
		ID:         params.ID,
		UserID:     params.UserID,
		SecretHash: cloneBytes(params.SecretHash),
		CSRFToken:  params.CSRFToken,
		ExpiresAt:  expiresAt,
		CreatedAt:  createdAt,
	}, nil
}

func (s *SQLite) SessionByID(ctx context.Context, id string) (Session, error) {
	var session Session
	var userID sql.NullInt64
	var createdAt string
	var expiresAt string
	err := s.db.QueryRowContext(ctx, `
SELECT id, user_id, secret_hash, csrf_token, expires_at, created_at
FROM web_sessions
WHERE id = ?
`, id).Scan(&session.ID, &userID, &session.SecretHash, &session.CSRFToken, &expiresAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if userID.Valid {
		session.UserID = userID.Int64
	}
	var parseErr error
	session.ExpiresAt, parseErr = parseTime(expiresAt)
	if parseErr != nil {
		return Session{}, parseErr
	}
	session.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return Session{}, parseErr
	}
	session.SecretHash = cloneBytes(session.SecretHash)
	return session, nil
}

func (s *SQLite) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE id = ?`, id)
	return err
}

func (s *SQLite) DefaultConversationForUser(ctx context.Context, userID int64) (Conversation, error) {
	if userID <= 0 {
		return Conversation{}, ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, err
	}
	defer rollback(tx)

	conversation, err := conversationByDefault(ctx, tx, userID)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Conversation{}, err
		}
		return conversation, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Conversation{}, err
	}

	createdAt := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
INSERT INTO conversations (user_id, title, is_default, created_at)
VALUES (?, ?, 1, ?)
`, userID, "Default", formatTime(createdAt))
	if err != nil {
		if sqliteIsConstraint(err) {
			return Conversation{}, ErrConflict
		}
		return Conversation{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Conversation{}, err
	}
	conversation = Conversation{
		ID:        id,
		UserID:    userID,
		Title:     "Default",
		IsDefault: true,
		CreatedAt: createdAt,
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, err
	}
	return conversation, nil
}

func conversationByDefault(ctx context.Context, tx *sql.Tx, userID int64) (Conversation, error) {
	var conversation Conversation
	var isDefault int
	var createdAt string
	err := tx.QueryRowContext(ctx, `
SELECT id, user_id, title, is_default, created_at
FROM conversations
WHERE user_id = ? AND is_default = 1
`, userID).Scan(&conversation.ID, &conversation.UserID, &conversation.Title, &isDefault, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, err
	}
	conversation.IsDefault = isDefault == 1
	var parseErr error
	conversation.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return Conversation{}, parseErr
	}
	return conversation, nil
}

func (s *SQLite) Messages(ctx context.Context, conversationID int64) ([]llm.Message, error) {
	if conversationID <= 0 {
		return nil, ErrInvalidArgument
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT role, parts_json
FROM messages
WHERE conversation_id = ?
ORDER BY sequence ASC
`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []llm.Message
	for rows.Next() {
		var message llm.Message
		var partsJSON string
		if err := rows.Scan(&message.Role, &partsJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(partsJSON), &message.Parts); err != nil {
			return nil, err
		}
		messages = append(messages, message.Clone())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *SQLite) AppendTurn(ctx context.Context, conversationID int64, userMessage, assistantMessage llm.Message) error {
	if conversationID <= 0 || userMessage.Role != llm.RoleUser || assistantMessage.Role != llm.RoleAssistant {
		return ErrInvalidArgument
	}
	userParts, _ := json.Marshal(userMessage.Parts)
	assistantParts, _ := json.Marshal(assistantMessage.Parts)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	var maxSequence int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(sequence), 0)
FROM messages
WHERE conversation_id = ?
`, conversationID).Scan(&maxSequence); err != nil {
		return err
	}
	createdAt := formatTime(time.Now().UTC())
	if _, err := tx.ExecContext(ctx, `
INSERT INTO messages (conversation_id, sequence, role, parts_json, created_at)
VALUES (?, ?, ?, ?, ?)
`, conversationID, maxSequence+1, userMessage.Role, string(userParts), createdAt); err != nil {
		if sqliteIsConstraint(err) {
			return ErrInvalidArgument
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO messages (conversation_id, sequence, role, parts_json, created_at)
VALUES (?, ?, ?, ?, ?)
`, conversationID, maxSequence+2, assistantMessage.Role, string(assistantParts), createdAt); err != nil {
		if sqliteIsConstraint(err) {
			return ErrInvalidArgument
		}
		return err
	}
	return tx.Commit()
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func execer(db *sql.DB) sqlExecer {
	return db
}

func rollback(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func nonZeroUTC(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func sqliteIsConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "constraint failed") || strings.Contains(message, "constraint constraint")
}
