package storage

import (
	"context"
	"errors"
	"path/filepath"
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
