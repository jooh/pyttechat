package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	if version != 1 {
		t.Fatalf("schema version = %d, want 1", version)
	}
}

func TestSQLiteOpenDSNAndDirectoryHandling(t *testing.T) {
	if _, err := OpenSQLite(context.Background(), "postgres://example"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("OpenSQLite invalid scheme error = %v, want ErrInvalidArgument", err)
	}
	if _, err := sqliteDSN("sqlite://"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("sqliteDSN empty path error = %v, want ErrInvalidArgument", err)
	}

	defaultDSN, err := sqliteDSN("  ")
	if err != nil {
		t.Fatalf("sqliteDSN blank error = %v, want nil", err)
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

	memory, err := OpenSQLite(context.Background(), "sqlite://:memory:")
	if err != nil {
		t.Fatalf("OpenSQLite memory error = %v, want nil", err)
	}
	if err := memory.Migrate(context.Background()); err != nil {
		t.Fatalf("memory Migrate error = %v, want nil", err)
	}
	if err := memory.Close(); err != nil {
		t.Fatalf("memory Close error = %v, want nil", err)
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open memory error = %v, want nil", err)
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

	originalDriverName := sqliteDriverName
	t.Cleanup(func() {
		sqliteDriverName = originalDriverName
	})
	sqliteDriverName = "missing-sqlite-driver-for-test"
	if _, err := OpenSQLite(context.Background(), "sqlite://:memory:"); err == nil {
		t.Fatalf("OpenSQLite sql.Open error = nil, want error")
	}
	sqliteDriverName = originalDriverName

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
	if _, err := store.SessionByID(ctx, anonymous.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old SessionByID error = %v, want ErrNotFound", err)
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
		Role: llm.RoleAssistant,
		Parts: []llm.Part{
			{Type: llm.PartReasoning, ID: "rs_1", Summary: []string{"thinking"}, EncryptedContent: "encrypted"},
			{Type: llm.PartText, Text: "answer one"},
			{Type: llm.PartImage, URL: "/assets/app.css", Filename: "image.png", Width: 10, Height: 20},
		},
	}
	if err := store.AppendTurn(ctx, conversation.ID, llm.NewTextMessage(llm.RoleUser, "first"), firstAssistant); err != nil {
		t.Fatalf("AppendTurn first error = %v, want nil", err)
	}
	if err := store.AppendTurn(ctx, conversation.ID, llm.NewTextMessage(llm.RoleUser, "second"), llm.NewTextMessage(llm.RoleAssistant, "answer two")); err != nil {
		t.Fatalf("AppendTurn second error = %v, want nil", err)
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
	reasoning := messages[1].Parts[0]
	if reasoning.ID != "rs_1" || reasoning.EncryptedContent != "encrypted" || len(reasoning.Summary) != 1 {
		t.Fatalf("round-tripped reasoning part = %#v, want metadata intact", reasoning)
	}
	image := messages[1].Parts[2]
	if image.Type != llm.PartImage || image.URL != "/assets/app.css" || image.Width != 10 || image.Height != 20 {
		t.Fatalf("round-tripped image part = %#v, want image metadata intact", image)
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

func TestSQLiteWritePathsPropagateDriverErrors(t *testing.T) {
	ctx := context.Background()
	errDriver := errors.New("driver failed")

	t.Run("create user exec", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			exec: func(string, []driver.NamedValue) (driver.Result, error) {
				return nil, errDriver
			},
		})
		if _, err := store.CreateUser(ctx, CreateUserParams{Username: "alice", PasswordHash: []byte("hash")}); !errors.Is(err, errDriver) {
			t.Fatalf("CreateUser error = %v, want driver error", err)
		}
	})

	t.Run("create user last insert id", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			exec: func(string, []driver.NamedValue) (driver.Result, error) {
				return scriptedSQLResult{lastIDErr: errDriver}, nil
			},
		})
		if _, err := store.CreateUser(ctx, CreateUserParams{Username: "alice", PasswordHash: []byte("hash")}); !errors.Is(err, errDriver) {
			t.Fatalf("CreateUser error = %v, want LastInsertId error", err)
		}
	})

	t.Run("rotate begin", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{beginErr: errDriver})
		_, err := store.RotateSession(ctx, "", validSessionParams("session"))
		if !errors.Is(err, errDriver) {
			t.Fatalf("RotateSession error = %v, want begin error", err)
		}
	})

	t.Run("rotate delete", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			exec: func(query string, _ []driver.NamedValue) (driver.Result, error) {
				if strings.Contains(query, "DELETE FROM web_sessions") {
					return nil, errDriver
				}
				return scriptedSQLResult{}, nil
			},
		})
		_, err := store.RotateSession(ctx, "old", validSessionParams("session"))
		if !errors.Is(err, errDriver) {
			t.Fatalf("RotateSession error = %v, want delete error", err)
		}
	})

	t.Run("rotate create session", func(t *testing.T) {
		store := newMigratedTestSQLite(t)
		_, err := store.RotateSession(ctx, "", CreateSessionParams{})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("RotateSession error = %v, want invalid argument", err)
		}
	})

	t.Run("rotate commit", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{commitErr: errDriver})
		_, err := store.RotateSession(ctx, "", validSessionParams("session"))
		if !errors.Is(err, errDriver) {
			t.Fatalf("RotateSession error = %v, want commit error", err)
		}
	})

	t.Run("create session exec", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			exec: func(string, []driver.NamedValue) (driver.Result, error) {
				return nil, errDriver
			},
		})
		_, err := store.CreateSession(ctx, validSessionParams("session"))
		if !errors.Is(err, errDriver) {
			t.Fatalf("CreateSession error = %v, want exec error", err)
		}
	})

	t.Run("default conversation insert", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return &scriptedRows{columns: conversationColumns}, nil
			},
			exec: func(string, []driver.NamedValue) (driver.Result, error) {
				return nil, errDriver
			},
		})
		if _, err := store.DefaultConversationForUser(ctx, 1); !errors.Is(err, errDriver) {
			t.Fatalf("DefaultConversationForUser error = %v, want insert error", err)
		}
	})

	t.Run("default conversation last insert id", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return &scriptedRows{columns: conversationColumns}, nil
			},
			exec: func(string, []driver.NamedValue) (driver.Result, error) {
				return scriptedSQLResult{lastIDErr: errDriver}, nil
			},
		})
		if _, err := store.DefaultConversationForUser(ctx, 1); !errors.Is(err, errDriver) {
			t.Fatalf("DefaultConversationForUser error = %v, want LastInsertId error", err)
		}
	})

	t.Run("append begin", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{beginErr: errDriver})
		err := store.AppendTurn(ctx, 1, llm.NewTextMessage(llm.RoleUser, "hello"), llm.NewTextMessage(llm.RoleAssistant, "answer"))
		if !errors.Is(err, errDriver) {
			t.Fatalf("AppendTurn error = %v, want begin error", err)
		}
	})

	t.Run("append max sequence", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return nil, errDriver
			},
		})
		err := store.AppendTurn(ctx, 1, llm.NewTextMessage(llm.RoleUser, "hello"), llm.NewTextMessage(llm.RoleAssistant, "answer"))
		if !errors.Is(err, errDriver) {
			t.Fatalf("AppendTurn error = %v, want query error", err)
		}
	})

	t.Run("append user insert generic", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: maxSequenceQuery,
			exec: func(string, []driver.NamedValue) (driver.Result, error) {
				return nil, errDriver
			},
		})
		err := store.AppendTurn(ctx, 1, llm.NewTextMessage(llm.RoleUser, "hello"), llm.NewTextMessage(llm.RoleAssistant, "answer"))
		if !errors.Is(err, errDriver) {
			t.Fatalf("AppendTurn error = %v, want insert error", err)
		}
	})

	t.Run("append assistant insert constraint", func(t *testing.T) {
		execCount := 0
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: maxSequenceQuery,
			exec: func(string, []driver.NamedValue) (driver.Result, error) {
				execCount++
				if execCount == 2 {
					return nil, errors.New("constraint failed: messages_conversation_sequence_idx")
				}
				return scriptedSQLResult{}, nil
			},
		})
		err := store.AppendTurn(ctx, 1, llm.NewTextMessage(llm.RoleUser, "hello"), llm.NewTextMessage(llm.RoleAssistant, "answer"))
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("AppendTurn error = %v, want invalid argument", err)
		}
	})

	t.Run("append assistant insert generic", func(t *testing.T) {
		execCount := 0
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: maxSequenceQuery,
			exec: func(string, []driver.NamedValue) (driver.Result, error) {
				execCount++
				if execCount == 2 {
					return nil, errDriver
				}
				return scriptedSQLResult{}, nil
			},
		})
		err := store.AppendTurn(ctx, 1, llm.NewTextMessage(llm.RoleUser, "hello"), llm.NewTextMessage(llm.RoleAssistant, "answer"))
		if !errors.Is(err, errDriver) {
			t.Fatalf("AppendTurn error = %v, want assistant insert error", err)
		}
	})

	t.Run("append commit", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query:     maxSequenceQuery,
			commitErr: errDriver,
		})
		err := store.AppendTurn(ctx, 1, llm.NewTextMessage(llm.RoleUser, "hello"), llm.NewTextMessage(llm.RoleAssistant, "answer"))
		if !errors.Is(err, errDriver) {
			t.Fatalf("AppendTurn error = %v, want commit error", err)
		}
	})
}

func TestSQLiteReadPathsPropagateDriverErrors(t *testing.T) {
	ctx := context.Background()
	errDriver := errors.New("driver failed")

	t.Run("user query", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return nil, errDriver
			},
		})
		if _, err := store.UserByUsername(ctx, "alice"); !errors.Is(err, errDriver) {
			t.Fatalf("UserByUsername error = %v, want query error", err)
		}
	})

	t.Run("session query", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return nil, errDriver
			},
		})
		if _, err := store.SessionByID(ctx, "session"); !errors.Is(err, errDriver) {
			t.Fatalf("SessionByID error = %v, want query error", err)
		}
	})

	t.Run("default conversation begin", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{beginErr: errDriver})
		if _, err := store.DefaultConversationForUser(ctx, 1); !errors.Is(err, errDriver) {
			t.Fatalf("DefaultConversationForUser error = %v, want begin error", err)
		}
	})

	t.Run("default conversation query", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return nil, errDriver
			},
		})
		if _, err := store.DefaultConversationForUser(ctx, 1); !errors.Is(err, errDriver) {
			t.Fatalf("DefaultConversationForUser error = %v, want query error", err)
		}
	})

	t.Run("default conversation existing commit", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return &scriptedRows{
					columns: conversationColumns,
					rows: [][]driver.Value{{
						int64(10),
						int64(1),
						"Default",
						int64(1),
						formatTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
					}},
				}, nil
			},
			commitErr: errDriver,
		})
		if _, err := store.DefaultConversationForUser(ctx, 1); !errors.Is(err, errDriver) {
			t.Fatalf("DefaultConversationForUser error = %v, want commit error", err)
		}
	})

	t.Run("default conversation insert commit", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return &scriptedRows{columns: conversationColumns}, nil
			},
			commitErr: errDriver,
		})
		if _, err := store.DefaultConversationForUser(ctx, 1); !errors.Is(err, errDriver) {
			t.Fatalf("DefaultConversationForUser error = %v, want commit error", err)
		}
	})

	t.Run("messages query", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return nil, errDriver
			},
		})
		if _, err := store.Messages(ctx, 1); !errors.Is(err, errDriver) {
			t.Fatalf("Messages error = %v, want query error", err)
		}
	})

	t.Run("messages scan", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return &scriptedRows{
					columns: []string{"role", "parts_json"},
					rows:    [][]driver.Value{{int64(1), `[]`}},
				}, nil
			},
		})
		if _, err := store.Messages(ctx, 1); err == nil {
			t.Fatalf("Messages scan error = nil, want error")
		}
	})

	t.Run("messages rows", func(t *testing.T) {
		store := newScriptedSQLite(t, &scriptedSQLScenario{
			query: func(string, []driver.NamedValue) (driver.Rows, error) {
				return &scriptedRows{columns: []string{"role", "parts_json"}, err: errDriver}, nil
			},
		})
		if _, err := store.Messages(ctx, 1); !errors.Is(err, errDriver) {
			t.Fatalf("Messages error = %v, want rows error", err)
		}
	})
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

func validSessionParams(id string) CreateSessionParams {
	return CreateSessionParams{
		ID:         id,
		SecretHash: []byte("secret"),
		CSRFToken:  "csrf",
		ExpiresAt:  time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		CreatedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

var conversationColumns = []string{"id", "user_id", "title", "is_default", "created_at"}

func maxSequenceQuery(string, []driver.NamedValue) (driver.Rows, error) {
	return &scriptedRows{
		columns: []string{"max_sequence"},
		rows:    [][]driver.Value{{int64(0)}},
	}, nil
}

var (
	registerScriptedSQLDriver sync.Once
	scriptedSQLScenarios      sync.Map
)

type scriptedSQLScenario struct {
	exec      func(string, []driver.NamedValue) (driver.Result, error)
	query     func(string, []driver.NamedValue) (driver.Rows, error)
	beginErr  error
	commitErr error
}

type scriptedSQLDriver struct{}

func (scriptedSQLDriver) Open(name string) (driver.Conn, error) {
	value, ok := scriptedSQLScenarios.Load(name)
	if !ok {
		return nil, errors.New("missing scripted SQL scenario")
	}
	return scriptedSQLConn{scenario: value.(*scriptedSQLScenario)}, nil
}

type scriptedSQLConn struct {
	scenario *scriptedSQLScenario
}

func (c scriptedSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("scripted SQL prepare is unsupported")
}

func (c scriptedSQLConn) Close() error {
	return nil
}

func (c scriptedSQLConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c scriptedSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if c.scenario.beginErr != nil {
		return nil, c.scenario.beginErr
	}
	return scriptedSQLTx{scenario: c.scenario}, nil
}

func (c scriptedSQLConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.scenario.exec != nil {
		return c.scenario.exec(query, args)
	}
	return scriptedSQLResult{}, nil
}

func (c scriptedSQLConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.scenario.query != nil {
		return c.scenario.query(query, args)
	}
	return &scriptedRows{}, nil
}

type scriptedSQLTx struct {
	scenario *scriptedSQLScenario
}

func (tx scriptedSQLTx) Commit() error {
	return tx.scenario.commitErr
}

func (scriptedSQLTx) Rollback() error {
	return nil
}

type scriptedSQLResult struct {
	lastID    int64
	rows      int64
	lastIDErr error
	rowsErr   error
}

func (r scriptedSQLResult) LastInsertId() (int64, error) {
	return r.lastID, r.lastIDErr
}

func (r scriptedSQLResult) RowsAffected() (int64, error) {
	return r.rows, r.rowsErr
}

type scriptedRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	err     error
}

func (r *scriptedRows) Columns() []string {
	if r.columns == nil {
		return []string{"value"}
	}
	return r.columns
}

func (r *scriptedRows) Close() error {
	return nil
}

func (r *scriptedRows) Next(dest []driver.Value) error {
	if r.index < len(r.rows) {
		copy(dest, r.rows[r.index])
		r.index++
		return nil
	}
	if r.err != nil {
		return r.err
	}
	return io.EOF
}

func newScriptedSQLite(t *testing.T, scenario *scriptedSQLScenario) *SQLite {
	t.Helper()
	registerScriptedSQLDriver.Do(func() {
		sql.Register("pyttechat_storage_scripted", scriptedSQLDriver{})
	})
	name := strings.ReplaceAll(t.Name(), "/", "-")
	scriptedSQLScenarios.Store(name, scenario)
	t.Cleanup(func() {
		scriptedSQLScenarios.Delete(name)
	})
	db, err := sql.Open("pyttechat_storage_scripted", name)
	if err != nil {
		t.Fatalf("scripted sql.Open error = %v, want nil", err)
	}
	store := NewSQLiteForDB(db)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("scripted Close error = %v", err)
		}
	})
	return store
}
