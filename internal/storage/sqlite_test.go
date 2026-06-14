package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/llm-chat-web/internal/llm"
)

func TestSQLiteMigrateIsIdempotent(t *testing.T) {
	store := newTestSQLite(t)

	for i := 0; i < 2; i++ {
		if err := store.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate #%d error = %v, want nil", i+1, err)
		}
	}

	var version int
	if err := store.db.QueryRowContext(context.Background(), `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("schema version query error = %v", err)
	}
	if version != 2 {
		t.Fatalf("schema version = %d, want 2", version)
	}
}

func TestSQLiteMigrateAddsCompletedAtToVersionOneMessages(t *testing.T) {
	db, openErr := sql.Open("sqlite", ":memory:")
	if openErr != nil {
		t.Fatalf("sql.Open memory error = %v, want nil", openErr)
	}
	store := NewSQLiteForDB(db)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `
CREATE TABLE schema_version (version INTEGER NOT NULL);
INSERT INTO schema_version (version) VALUES (1);
CREATE TABLE users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE,
	password_hash BLOB NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE conversations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	title TEXT NOT NULL,
	is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
	created_at TEXT NOT NULL
);
CREATE TABLE messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	sequence INTEGER NOT NULL,
	role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
	parts_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE (conversation_id, sequence)
);
`); err != nil {
		t.Fatalf("create version one schema error = %v, want nil", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate version one schema error = %v, want nil", err)
	}

	var version int
	if err := store.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("schema version query error = %v", err)
	}
	if version != 2 {
		t.Fatalf("schema version = %d, want 2", version)
	}
	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(messages)`)
	if err != nil {
		t.Fatalf("messages table_info error = %v, want nil", err)
	}
	defer rows.Close()
	hasCompletedAt := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info error = %v, want nil", err)
		}
		if name == "completed_at" {
			hasCompletedAt = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows error = %v, want nil", err)
	}
	if !hasCompletedAt {
		t.Fatalf("messages table missing completed_at column after migration")
	}
}

func TestSQLiteOpenDSNAndDirectoryHandling(t *testing.T) {
	if _, err := OpenSQLite(context.Background(), "postgres://example"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("OpenSQLite invalid scheme error = %v, want ErrInvalidArgument", err)
	}
	if _, err := sqliteDSN("sqlite://"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("sqliteDSN empty path error = %v, want ErrInvalidArgument", err)
	}

	defaultDSN, defaultErr := sqliteDSN("  ")
	if defaultErr != nil {
		t.Fatalf("sqliteDSN blank error = %v, want nil", defaultErr)
	}
	if !strings.Contains(defaultDSN, ".cache/pyttechat/pyttechat.db") {
		t.Fatalf("default DSN = %q, want default cache database path", defaultDSN)
	}
	if got, err := sqliteDSN(" sqlite://relative.db "); err != nil || got != "relative.db" {
		t.Fatalf("sqliteDSN relative = %q, %v; want relative.db, nil", got, err)
	}
	absolute := filepath.Join(t.TempDir(), "absolute.db")
	if got, err := sqliteDSN("sqlite://" + absolute); err != nil || got != absolute {
		t.Fatalf("sqliteDSN absolute = %q, %v; want %q, nil", got, err, absolute)
	}

	if err := ensureSQLiteDir(":memory:"); err != nil {
		t.Fatalf("ensureSQLiteDir memory error = %v, want nil", err)
	}
	if err := ensureSQLiteDir("file:pyttechat?mode=memory&cache=shared"); err != nil {
		t.Fatalf("ensureSQLiteDir file URI error = %v, want nil", err)
	}
	if err := ensureSQLiteDir("plain.db"); err != nil {
		t.Fatalf("ensureSQLiteDir plain file error = %v, want nil", err)
	}
	nested := filepath.Join(t.TempDir(), "nested", "pyttechat.db")
	if err := ensureSQLiteDir(nested); err != nil {
		t.Fatalf("ensureSQLiteDir nested path error = %v, want nil", err)
	}
	if info, err := os.Stat(filepath.Dir(nested)); err != nil || !info.IsDir() {
		t.Fatalf("nested database dir stat = %#v, %v; want directory", info, err)
	}

	memory, memoryErr := OpenSQLite(context.Background(), "sqlite://:memory:")
	if memoryErr != nil {
		t.Fatalf("OpenSQLite memory error = %v, want nil", memoryErr)
	}
	if migrateErr := memory.Migrate(context.Background()); migrateErr != nil {
		t.Fatalf("memory Migrate error = %v, want nil", migrateErr)
	}
	if closeErr := memory.Close(); closeErr != nil {
		t.Fatalf("memory Close error = %v, want nil", closeErr)
	}

	db, openErr := sql.Open("sqlite", ":memory:")
	if openErr != nil {
		t.Fatalf("sql.Open memory error = %v, want nil", openErr)
	}
	store := NewSQLiteForDB(db)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("NewSQLiteForDB Migrate error = %v, want nil", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("NewSQLiteForDB Close error = %v, want nil", err)
	}
}

func TestSQLiteOpenPropagatesSetupFailures(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatalf("write blocker file error = %v, want nil", err)
	}
	if _, err := OpenSQLite(context.Background(), "sqlite://"+filepath.Join(blocker, "pyttechat.db")); err == nil {
		t.Fatalf("OpenSQLite directory setup error = nil, want error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenSQLite(ctx, "sqlite://:memory:"); err == nil {
		t.Fatalf("OpenSQLite canceled setup error = nil, want error")
	}
}

func TestSQLiteUsersAreUniqueByNormalizedUsername(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserParams{
		Username:     "alice",
		PasswordHash: []byte("hash"),
		CreatedAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateUser error = %v, want nil", err)
	}
	if user.ID == 0 || user.Username != "alice" || string(user.PasswordHash) != "hash" {
		t.Fatalf("user = %#v, want persisted alice with hash", user)
	}

	_, err = store.CreateUser(ctx, CreateUserParams{Username: "alice", PasswordHash: []byte("other")})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CreateUser error = %v, want ErrConflict", err)
	}

	got, err := store.UserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("UserByUsername error = %v, want nil", err)
	}
	if got.ID != user.ID || got.Username != "alice" || string(got.PasswordHash) != "hash" {
		t.Fatalf("UserByUsername = %#v, want original user", got)
	}

	_, err = store.UserByUsername(ctx, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing UserByUsername error = %v, want ErrNotFound", err)
	}
}

func TestSQLiteUserInputsAndStoredBytesAreDefensive(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()

	for _, params := range []CreateUserParams{
		{Username: " ", PasswordHash: []byte("hash")},
		{Username: "alice"},
	} {
		if _, err := store.CreateUser(ctx, params); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("CreateUser(%#v) error = %v, want ErrInvalidArgument", params, err)
		}
	}

	hash := []byte("hash")
	user, err := store.CreateUser(ctx, CreateUserParams{Username: "alice", PasswordHash: hash})
	if err != nil {
		t.Fatalf("CreateUser error = %v, want nil", err)
	}
	hash[0] = 'H'
	if string(user.PasswordHash) != "hash" {
		t.Fatalf("returned user hash = %q, want defensive copy", user.PasswordHash)
	}

	got, err := store.UserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("UserByUsername error = %v, want nil", err)
	}
	got.PasswordHash[0] = 'X'
	again, err := store.UserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("second UserByUsername error = %v, want nil", err)
	}
	if string(again.PasswordHash) != "hash" {
		t.Fatalf("stored hash = %q, want query result mutation not to affect storage", again.PasswordHash)
	}
	if user.CreatedAt.IsZero() {
		t.Fatalf("CreatedAt is zero, want default timestamp")
	}

	if got := cloneBytes(nil); got != nil {
		t.Fatalf("cloneBytes(nil) = %#v, want nil", got)
	}
}

func TestSQLiteSessionsSupportAnonymousAuthenticatedRotateAndDelete(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()
	user := createTestUser(t, store, "bob")

	anonymous, err := store.CreateSession(ctx, CreateSessionParams{
		ID:         "session-anon",
		SecretHash: []byte("anon-secret-hash"),
		CSRFToken:  "csrf-anon",
		ExpiresAt:  time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateSession anonymous error = %v, want nil", err)
	}
	if anonymous.Authenticated() {
		t.Fatalf("anonymous session is authenticated")
	}

	rotated, err := store.RotateSession(ctx, anonymous.ID, CreateSessionParams{
		ID:         "session-auth",
		UserID:     user.ID,
		SecretHash: []byte("auth-secret-hash"),
		CSRFToken:  "csrf-auth",
		ExpiresAt:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RotateSession error = %v, want nil", err)
	}
	if rotated.ID != "session-auth" || rotated.UserID != user.ID || string(rotated.SecretHash) != "auth-secret-hash" || rotated.CSRFToken != "csrf-auth" {
		t.Fatalf("rotated session = %#v, want authenticated replacement", rotated)
	}
	if _, lookupErr := store.SessionByID(ctx, anonymous.ID); !errors.Is(lookupErr, ErrNotFound) {
		t.Fatalf("old SessionByID error = %v, want ErrNotFound", lookupErr)
	}

	got, err := store.SessionByID(ctx, rotated.ID)
	if err != nil {
		t.Fatalf("SessionByID error = %v, want nil", err)
	}
	if got.UserID != user.ID || !got.ExpiresAt.Equal(rotated.ExpiresAt) {
		t.Fatalf("SessionByID = %#v, want rotated authenticated session", got)
	}

	if err := store.DeleteSession(ctx, rotated.ID); err != nil {
		t.Fatalf("DeleteSession error = %v, want nil", err)
	}
	if _, err := store.SessionByID(ctx, rotated.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted SessionByID error = %v, want ErrNotFound", err)
	}
}

func TestSQLiteSessionInvalidInputsAndRotationWithoutOldSession(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()

	for _, params := range []CreateSessionParams{
		{SecretHash: []byte("secret"), CSRFToken: "csrf"},
		{ID: "session", CSRFToken: "csrf"},
		{ID: "session", SecretHash: []byte("secret")},
	} {
		if _, err := store.CreateSession(ctx, params); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("CreateSession(%#v) error = %v, want ErrInvalidArgument", params, err)
		}
	}

	secret := []byte("secret")
	session, err := store.RotateSession(ctx, "", CreateSessionParams{
		ID:         "rotated-new",
		SecretHash: secret,
		CSRFToken:  "csrf",
		ExpiresAt:  time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RotateSession without old ID error = %v, want nil", err)
	}
	secret[0] = 'S'
	if string(session.SecretHash) != "secret" {
		t.Fatalf("session secret hash = %q, want defensive copy", session.SecretHash)
	}
	got, err := store.SessionByID(ctx, "rotated-new")
	if err != nil {
		t.Fatalf("SessionByID error = %v, want nil", err)
	}
	got.SecretHash[0] = 'X'
	again, err := store.SessionByID(ctx, "rotated-new")
	if err != nil {
		t.Fatalf("second SessionByID error = %v, want nil", err)
	}
	if string(again.SecretHash) != "secret" {
		t.Fatalf("stored session secret = %q, want query result mutation not to affect storage", again.SecretHash)
	}
}

func TestSQLiteForeignKeysAndDefaultConversation(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()
	user := createTestUser(t, store, "carol")

	conversation, err := store.DefaultConversationForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("DefaultConversationForUser error = %v, want nil", err)
	}
	if conversation.ID == 0 || conversation.UserID != user.ID || !conversation.IsDefault {
		t.Fatalf("conversation = %#v, want default conversation for user", conversation)
	}

	again, err := store.DefaultConversationForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("second DefaultConversationForUser error = %v, want nil", err)
	}
	if again.ID != conversation.ID {
		t.Fatalf("second default ID = %d, want %d", again.ID, conversation.ID)
	}

	_, err = store.CreateSession(ctx, CreateSessionParams{
		ID:         "bad-fk",
		UserID:     user.ID + 999,
		SecretHash: []byte("secret"),
		CSRFToken:  "csrf",
		ExpiresAt:  time.Now().Add(time.Hour),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("session with missing user error = %v, want ErrConflict", err)
	}
}

func TestSQLiteConversationAndMessageInvalidInputs(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()

	if _, err := store.DefaultConversationForUser(ctx, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("DefaultConversationForUser(0) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := store.DefaultConversationForUser(ctx, 999); !errors.Is(err, ErrConflict) {
		t.Fatalf("DefaultConversationForUser missing user error = %v, want ErrConflict", err)
	}
	if _, err := store.Messages(ctx, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Messages(0) error = %v, want ErrInvalidArgument", err)
	}
}

func TestSQLiteMessagesAreOrderedAndPartsJSONRoundTrips(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()
	user := createTestUser(t, store, "dana")
	conversation, err := store.DefaultConversationForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("DefaultConversationForUser error = %v, want nil", err)
	}

	firstAssistant := llm.Message{
		Role:        llm.RoleAssistant,
		CompletedAt: time.Date(2026, 6, 14, 10, 11, 12, 0, time.UTC),
		Parts: []llm.Part{
			{Type: llm.PartReasoning, ID: "rs_1", Summary: []string{"thinking"}, EncryptedContent: "encrypted"},
			{Type: llm.PartText, Text: "answer one"},
			{Type: llm.PartImage, URL: "/assets/app.css", Filename: "image.png", Width: 10, Height: 20},
		},
	}
	if appendErr := store.AppendTurn(ctx, conversation.ID, llm.NewTextMessage(llm.RoleUser, "first"), firstAssistant); appendErr != nil {
		t.Fatalf("AppendTurn first error = %v, want nil", appendErr)
	}
	if appendErr := store.AppendTurn(ctx, conversation.ID, llm.NewTextMessage(llm.RoleUser, "second"), llm.NewTextMessage(llm.RoleAssistant, "answer two")); appendErr != nil {
		t.Fatalf("AppendTurn second error = %v, want nil", appendErr)
	}

	messages, err := store.Messages(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("Messages error = %v, want nil", err)
	}
	if len(messages) != 4 {
		t.Fatalf("message count = %d, want 4", len(messages))
	}
	if messages[0].Text() != "first" || messages[1].Text() != "answer one" || messages[2].Text() != "second" || messages[3].Text() != "answer two" {
		t.Fatalf("messages = %#v, want insertion order", messages)
	}
	if !messages[1].CompletedAt.Equal(firstAssistant.CompletedAt) {
		t.Fatalf("first assistant completed_at = %v, want %v", messages[1].CompletedAt, firstAssistant.CompletedAt)
	}
	if !messages[3].CompletedAt.IsZero() {
		t.Fatalf("second assistant completed_at = %v, want zero value", messages[3].CompletedAt)
	}
	reasoning := messages[1].Parts[0]
	if reasoning.ID != "rs_1" || reasoning.EncryptedContent != "encrypted" || len(reasoning.Summary) != 1 {
		t.Fatalf("round-tripped reasoning part = %#v, want metadata intact", reasoning)
	}
	image := messages[1].Parts[2]
	if image.Type != llm.PartImage || image.URL != "/assets/app.css" || image.Width != 10 || image.Height != 20 {
		t.Fatalf("round-tripped image part = %#v, want image metadata intact", image)
	}
}

func TestSQLiteReplaceTailAndAppendTurnTruncatesAndAppends(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()
	user := createTestUser(t, store, "dina")
	conversation, err := store.DefaultConversationForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("DefaultConversationForUser error = %v, want nil", err)
	}

	if appendErr := store.AppendTurn(ctx, conversation.ID, llm.NewTextMessage(llm.RoleUser, "first"), llm.NewTextMessage(llm.RoleAssistant, "answer one")); appendErr != nil {
		t.Fatalf("AppendTurn first error = %v, want nil", appendErr)
	}
	if appendErr := store.AppendTurn(ctx, conversation.ID, llm.NewTextMessage(llm.RoleUser, "second"), llm.NewTextMessage(llm.RoleAssistant, "answer two")); appendErr != nil {
		t.Fatalf("AppendTurn second error = %v, want nil", appendErr)
	}

	err = store.ReplaceTailAndAppendTurn(ctx, conversation.ID, 2, llm.NewTextMessage(llm.RoleUser, "edited second"), llm.NewTextMessage(llm.RoleAssistant, "replacement answer"))
	if err != nil {
		t.Fatalf("ReplaceTailAndAppendTurn error = %v, want nil", err)
	}

	messages, err := store.Messages(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("Messages error = %v, want nil", err)
	}
	if len(messages) != 4 {
		t.Fatalf("message count = %d, want 4", len(messages))
	}
	if messages[0].Text() != "first" || messages[1].Text() != "answer one" || messages[2].Text() != "edited second" || messages[3].Text() != "replacement answer" {
		t.Fatalf("messages = %#v, want first turn plus edited replacement turn", messages)
	}
}

func TestSQLiteAppendTurnRejectsInvalidInputWithoutPersisting(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()
	user := createTestUser(t, store, "erin")
	conversation, err := store.DefaultConversationForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("DefaultConversationForUser error = %v, want nil", err)
	}

	err = store.AppendTurn(ctx, conversation.ID, llm.NewTextMessage(llm.RoleAssistant, "wrong"), llm.NewTextMessage(llm.RoleAssistant, "answer"))
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid role AppendTurn error = %v, want ErrInvalidArgument", err)
	}
	err = store.AppendTurn(ctx, conversation.ID+999, llm.NewTextMessage(llm.RoleUser, "missing"), llm.NewTextMessage(llm.RoleAssistant, "answer"))
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing conversation AppendTurn error = %v, want ErrInvalidArgument", err)
	}

	messages, err := store.Messages(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("Messages error = %v, want nil", err)
	}
	if len(messages) != 0 {
		t.Fatalf("message count after rejected appends = %d, want 0", len(messages))
	}
}

func TestSQLiteReplaceTailAndAppendTurnRejectsInvalidKeepCount(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()
	user := createTestUser(t, store, "edie")
	conversation, err := store.DefaultConversationForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("DefaultConversationForUser error = %v, want nil", err)
	}
	if appendErr := store.AppendTurn(ctx, conversation.ID, llm.NewTextMessage(llm.RoleUser, "first"), llm.NewTextMessage(llm.RoleAssistant, "answer")); appendErr != nil {
		t.Fatalf("AppendTurn error = %v, want nil", appendErr)
	}

	for _, keepMessages := range []int{-1, 3} {
		replaceErr := store.ReplaceTailAndAppendTurn(ctx, conversation.ID, keepMessages, llm.NewTextMessage(llm.RoleUser, "bad"), llm.NewTextMessage(llm.RoleAssistant, "bad answer"))
		if !errors.Is(replaceErr, ErrInvalidArgument) {
			t.Fatalf("ReplaceTailAndAppendTurn keep=%d error = %v, want ErrInvalidArgument", keepMessages, replaceErr)
		}
	}

	messages, err := store.Messages(ctx, conversation.ID)
	if err != nil {
		t.Fatalf("Messages error = %v, want nil", err)
	}
	if len(messages) != 2 || messages[0].Text() != "first" || messages[1].Text() != "answer" {
		t.Fatalf("messages after rejected replacements = %#v, want original turn intact", messages)
	}
}

func TestSQLiteReadPathDataCorruptionReturnsErrors(t *testing.T) {
	store := newMigratedTestSQLite(t)
	ctx := context.Background()
	user := createTestUser(t, store, "frank")

	if _, err := store.db.ExecContext(ctx, `
UPDATE users SET created_at = 'not-a-time' WHERE id = ?
`, user.ID); err != nil {
		t.Fatalf("corrupt user timestamp error = %v, want nil", err)
	}
	if _, err := store.UserByUsername(ctx, "frank"); err == nil {
		t.Fatalf("UserByUsername corrupt timestamp error = nil, want error")
	}

	if _, err := store.db.ExecContext(ctx, `
INSERT INTO web_sessions (id, user_id, secret_hash, csrf_token, expires_at, created_at)
VALUES ('bad-session-expires', NULL, X'01', 'csrf', 'not-a-time', ?)
`, formatTime(time.Now().UTC())); err != nil {
		t.Fatalf("insert corrupt session expires error = %v, want nil", err)
	}
	if _, err := store.SessionByID(ctx, "bad-session-expires"); err == nil {
		t.Fatalf("SessionByID corrupt expires_at error = nil, want error")
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO web_sessions (id, user_id, secret_hash, csrf_token, expires_at, created_at)
VALUES ('bad-session-created', NULL, X'01', 'csrf', ?, 'not-a-time')
`, formatTime(time.Now().UTC().Add(time.Hour))); err != nil {
		t.Fatalf("insert corrupt session created error = %v, want nil", err)
	}
	if _, err := store.SessionByID(ctx, "bad-session-created"); err == nil {
		t.Fatalf("SessionByID corrupt created_at error = nil, want error")
	}

	gina := createTestUser(t, store, "gina")
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO conversations (user_id, title, is_default, created_at)
VALUES (?, 'Default', 1, 'not-a-time')
`, gina.ID); err != nil {
		t.Fatalf("insert corrupt conversation error = %v, want nil", err)
	}
	if _, err := store.DefaultConversationForUser(ctx, gina.ID); err == nil {
		t.Fatalf("DefaultConversationForUser corrupt timestamp error = nil, want error")
	}

	henry := createTestUser(t, store, "henry")
	conversation, err := store.DefaultConversationForUser(ctx, henry.ID)
	if err != nil {
		t.Fatalf("DefaultConversationForUser henry error = %v, want nil", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO messages (conversation_id, sequence, role, parts_json, created_at)
VALUES (?, 1, 'assistant', 'not-json', ?)
`, conversation.ID, formatTime(time.Now().UTC())); err != nil {
		t.Fatalf("insert corrupt message error = %v, want nil", err)
	}
	if _, err := store.Messages(ctx, conversation.ID); err == nil {
		t.Fatalf("Messages corrupt parts_json error = nil, want error")
	}
}

func TestSQLiteConstraintDetector(t *testing.T) {
	if sqliteIsConstraint(nil) {
		t.Fatalf("sqliteIsConstraint(nil) = true, want false")
	}
	if sqliteIsConstraint(errors.New("ordinary error")) {
		t.Fatalf("sqliteIsConstraint ordinary error = true, want false")
	}
	if !sqliteIsConstraint(errors.New("constraint failed: users.username")) {
		t.Fatalf("sqliteIsConstraint constraint failed = false, want true")
	}
}

func newMigratedTestSQLite(t *testing.T) *SQLite {
	t.Helper()
	store := newTestSQLite(t)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate error = %v, want nil", err)
	}
	return store
}

func newTestSQLite(t *testing.T) *SQLite {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pyttechat.db")
	store, err := OpenSQLite(context.Background(), "sqlite://"+path)
	if err != nil {
		t.Fatalf("OpenSQLite error = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close error = %v", err)
		}
	})
	return store
}

func createTestUser(t *testing.T, store *SQLite, username string) User {
	t.Helper()
	user, err := store.CreateUser(context.Background(), CreateUserParams{
		Username:     username,
		PasswordHash: []byte("hash-" + username),
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateUser(%q) error = %v, want nil", username, err)
	}
	return user
}
