package chat

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"example.com/llm-chat-web/internal/llm"
	"example.com/llm-chat-web/internal/llm/dummy"
)

func TestSessionSendStreamsAndStoresCompletedTurn(t *testing.T) {
	completedAt := time.Date(2026, 6, 14, 12, 34, 56, 0, time.UTC)
	client := dummy.NewClient(dummy.Turn{
		ReasoningChunks: []string{"think", "ing"},
		TextChunks:      []string{"ans", "wer"},
		ReasoningPart: llm.Part{
			Type:             llm.PartReasoning,
			ID:               "rs_1",
			Summary:          []string{"thinking"},
			EncryptedContent: "encrypted",
		},
	})
	session := NewService(client).NewSession()

	stream, err := session.Send(context.Background(), "  hello  ", SendOptions{
		Model: "test-model",
		Now:   func() time.Time { return completedAt },
	})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	events := collectEvents(t, stream)
	if events[0].Type != llm.EventReasoningDelta {
		t.Fatalf("first event type = %q, want reasoning delta", events[0].Type)
	}
	if events[3].Type != llm.EventTextDelta {
		t.Fatalf("fourth event type = %q, want text delta", events[3].Type)
	}

	messages := session.Messages()
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	if messages[0].Role != llm.RoleUser || messages[0].Text() != "hello" {
		t.Fatalf("user message = %#v, want trimmed user hello", messages[0])
	}
	if messages[1].Role != llm.RoleAssistant {
		t.Fatalf("assistant role = %q, want assistant", messages[1].Role)
	}
	if got := messages[1].Text(); got != "answer" {
		t.Fatalf("assistant text = %q, want answer", got)
	}
	if !messages[1].CompletedAt.Equal(completedAt) {
		t.Fatalf("assistant completed_at = %v, want %v", messages[1].CompletedAt, completedAt)
	}
	reasoning := messages[1].Parts[0]
	if reasoning.Type != llm.PartReasoning {
		t.Fatalf("first assistant part type = %q, want reasoning", reasoning.Type)
	}
	if reasoning.Text != "thinking" {
		t.Fatalf("reasoning text = %q, want thinking", reasoning.Text)
	}
	if reasoning.ID != "rs_1" || reasoning.EncryptedContent != "encrypted" {
		t.Fatalf("reasoning metadata = %#v, want id and encrypted content", reasoning)
	}

	requests := client.Requests()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Model != "test-model" {
		t.Fatalf("request model = %q, want test-model", requests[0].Model)
	}
	if requests[0].Reasoning != (llm.ReasoningOptions{}) {
		t.Fatalf("request reasoning = %#v, want zero value without reasoning options", requests[0].Reasoning)
	}
}

func TestSessionSendIncludesPriorTurnsAndReasoning(t *testing.T) {
	client := dummy.NewClient(
		dummy.Turn{
			ReasoningChunks: []string{"first thoughts"},
			TextChunks:      []string{"first answer"},
			ReasoningPart: llm.Part{
				Type:             llm.PartReasoning,
				ID:               "rs_first",
				Summary:          []string{"first thoughts"},
				EncryptedContent: "encrypted_first",
			},
		},
		dummy.Turn{
			ReasoningChunks: []string{"second thoughts"},
			TextChunks:      []string{"second answer"},
			ReasoningPart: llm.Part{
				Type:             llm.PartReasoning,
				ID:               "rs_second",
				Summary:          []string{"second thoughts"},
				EncryptedContent: "encrypted_second",
			},
		},
	)
	session := NewService(client).NewSession()

	first, err := session.Send(context.Background(), "first", SendOptions{})
	if err != nil {
		t.Fatalf("first Send() error = %v, want nil", err)
	}
	collectEvents(t, first)

	second, err := session.Send(context.Background(), "second", SendOptions{ReasoningEffort: "high"})
	if err != nil {
		t.Fatalf("second Send() error = %v, want nil", err)
	}
	collectEvents(t, second)

	requests := client.Requests()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	got := requests[1]
	if got.Reasoning.Effort != "high" {
		t.Fatalf("reasoning effort = %q, want high", got.Reasoning.Effort)
	}
	if got.Reasoning.Summary != "auto" {
		t.Fatalf("reasoning summary = %q, want auto when reasoning is requested", got.Reasoning.Summary)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("second request message count = %d, want 3: %#v", len(got.Messages), got.Messages)
	}
	if got.Messages[0].Text() != "first" || got.Messages[2].Text() != "second" {
		t.Fatalf("second request user messages = %#v, want first and second", got.Messages)
	}
	priorReasoning := got.Messages[1].Parts[0]
	if priorReasoning.ID != "rs_first" {
		t.Fatalf("prior reasoning id = %q, want rs_first", priorReasoning.ID)
	}
	if priorReasoning.EncryptedContent != "encrypted_first" {
		t.Fatalf("prior encrypted content = %q, want encrypted_first", priorReasoning.EncryptedContent)
	}
}

func TestSessionSendCanReplaceTailFromUserMessage(t *testing.T) {
	client := dummy.NewClient(
		dummy.Turn{TextChunks: []string{"first answer"}},
		dummy.Turn{TextChunks: []string{"second answer"}},
		dummy.Turn{TextChunks: []string{"replacement answer"}},
	)
	session := NewService(client).NewSession()

	first, err := session.Send(context.Background(), "first", SendOptions{})
	if err != nil {
		t.Fatalf("first Send() error = %v, want nil", err)
	}
	collectEvents(t, first)
	second, err := session.Send(context.Background(), "second", SendOptions{})
	if err != nil {
		t.Fatalf("second Send() error = %v, want nil", err)
	}
	collectEvents(t, second)

	replaceFrom := 2
	replacement, err := session.Send(context.Background(), "edited second", SendOptions{ReplaceFrom: &replaceFrom})
	if err != nil {
		t.Fatalf("replacement Send() error = %v, want nil", err)
	}
	collectEvents(t, replacement)

	requests := client.Requests()
	if len(requests) != 3 {
		t.Fatalf("request count = %d, want 3", len(requests))
	}
	if got := requests[2].Messages; len(got) != 3 || got[0].Text() != "first" || got[1].Text() != "first answer" || got[2].Text() != "edited second" {
		t.Fatalf("replacement request messages = %#v, want first turn plus edited prompt", requests[2].Messages)
	}
	messages := session.Messages()
	if len(messages) != 4 {
		t.Fatalf("stored message count = %d, want 4", len(messages))
	}
	if messages[0].Text() != "first" || messages[1].Text() != "first answer" || messages[2].Text() != "edited second" || messages[3].Text() != "replacement answer" {
		t.Fatalf("stored messages = %#v, want tail replaced by edited turn", messages)
	}
}

func TestSessionSendRejectsReplaceFromAssistantMessage(t *testing.T) {
	session := NewService(dummy.NewClient(dummy.Turn{TextChunks: []string{"answer"}})).NewSession()
	stream, err := session.Send(context.Background(), "first", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	collectEvents(t, stream)

	replaceFrom := 1
	_, err = session.Send(context.Background(), "bad edit", SendOptions{ReplaceFrom: &replaceFrom})
	if !errors.Is(err, ErrInvalidReplaceFrom) {
		t.Fatalf("Send() error = %v, want ErrInvalidReplaceFrom", err)
	}
}

func TestSessionValidateReplaceFromAndCommitStopped(t *testing.T) {
	session := NewService(dummy.NewClient()).NewSession()
	if err := session.ValidateReplaceFrom(nil); err != nil {
		t.Fatalf("ValidateReplaceFrom(nil) error = %v, want nil", err)
	}
	if err := session.CommitStopped(context.Background(), "  first  ", SendOptions{}); err != nil {
		t.Fatalf("CommitStopped append error = %v, want nil", err)
	}
	messages := session.Messages()
	if len(messages) != 2 || messages[0].Text() != "first" || messages[1].Role != llm.RoleAssistant {
		t.Fatalf("messages after stopped append = %#v, want user plus empty assistant", messages)
	}

	replaceFrom := 0
	if err := session.ValidateReplaceFrom(&replaceFrom); err != nil {
		t.Fatalf("ValidateReplaceFrom(user index) error = %v, want nil", err)
	}
	if err := session.CommitStopped(context.Background(), "edited first", SendOptions{ReplaceFrom: &replaceFrom}); err != nil {
		t.Fatalf("CommitStopped replace error = %v, want nil", err)
	}
	messages = session.Messages()
	if len(messages) != 2 || messages[0].Text() != "edited first" {
		t.Fatalf("messages after stopped replacement = %#v, want edited stopped turn", messages)
	}

	assistantIndex := 1
	if err := session.ValidateReplaceFrom(&assistantIndex); !errors.Is(err, ErrInvalidReplaceFrom) {
		t.Fatalf("ValidateReplaceFrom(assistant index) error = %v, want ErrInvalidReplaceFrom", err)
	}
	if err := session.CommitStopped(context.Background(), "  ", SendOptions{}); !errors.Is(err, ErrEmptyPrompt) {
		t.Fatalf("CommitStopped(empty) error = %v, want ErrEmptyPrompt", err)
	}
}

func TestSessionCommitStoppedRejectsInvalidReplaceFromAndStoreFailure(t *testing.T) {
	session := NewService(dummy.NewClient()).NewSession()
	invalidReplaceFrom := 1
	if err := session.CommitStopped(context.Background(), "bad edit", SendOptions{ReplaceFrom: &invalidReplaceFrom}); !errors.Is(err, ErrInvalidReplaceFrom) {
		t.Fatalf("CommitStopped(invalid replace_from) error = %v, want ErrInvalidReplaceFrom", err)
	}
	if messages := session.Messages(); len(messages) != 0 {
		t.Fatalf("messages after invalid stopped edit = %#v, want none", messages)
	}

	store := &chatStore{appendErr: errors.New("stopped append failed")}
	persistent, err := NewPersistentService(dummy.NewClient(), store).NewPersistedSession(context.Background(), 42)
	if err != nil {
		t.Fatalf("NewPersistedSession error = %v, want nil", err)
	}
	err = persistent.CommitStopped(context.Background(), "hello", SendOptions{})
	if err == nil || !strings.Contains(err.Error(), "stopped append failed") {
		t.Fatalf("CommitStopped store error = %v, want stopped append failed", err)
	}
	if messages := persistent.Messages(); len(messages) != 0 {
		t.Fatalf("messages after failed stopped append = %#v, want none", messages)
	}
}

func TestPersistentSessionLoadsHistoryAndAppendsCompletedTurn(t *testing.T) {
	store := &chatStore{
		messages: []llm.Message{
			llm.NewTextMessage(llm.RoleUser, "stored prompt"),
			llm.NewTextMessage(llm.RoleAssistant, "stored answer"),
		},
	}
	client := dummy.NewClient(dummy.Turn{TextChunks: []string{"fresh answer"}})
	session, err := NewPersistentService(client, store).NewPersistedSession(context.Background(), 42)
	if err != nil {
		t.Fatalf("NewPersistedSession error = %v, want nil", err)
	}

	stream, err := session.Send(context.Background(), "fresh prompt", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	collectEvents(t, stream)

	requests := client.Requests()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if len(requests[0].Messages) != 3 {
		t.Fatalf("request messages = %#v, want stored user, stored assistant, fresh user", requests[0].Messages)
	}
	if requests[0].Messages[0].Text() != "stored prompt" || requests[0].Messages[1].Text() != "stored answer" || requests[0].Messages[2].Text() != "fresh prompt" {
		t.Fatalf("request messages = %#v, want stored history before fresh prompt", requests[0].Messages)
	}
	if len(store.replaced) != 1 {
		t.Fatalf("replace count = %d, want 1", len(store.replaced))
	}
	if store.replaced[0].conversationID != 42 || store.replaced[0].keepMessages != 2 || store.replaced[0].user.Text() != "fresh prompt" || store.replaced[0].assistant.Text() != "fresh answer" {
		t.Fatalf("replaced turn = %#v, want completed fresh turn after stored history in conversation 42", store.replaced[0])
	}
}

func TestPersistentSessionReplaceTailPersistsEditedTurn(t *testing.T) {
	store := &chatStore{
		messages: []llm.Message{
			llm.NewTextMessage(llm.RoleUser, "stored first"),
			llm.NewTextMessage(llm.RoleAssistant, "stored first answer"),
			llm.NewTextMessage(llm.RoleUser, "stored second"),
			llm.NewTextMessage(llm.RoleAssistant, "stored second answer"),
		},
	}
	client := dummy.NewClient(dummy.Turn{TextChunks: []string{"edited answer"}})
	session, err := NewPersistentService(client, store).NewPersistedSession(context.Background(), 42)
	if err != nil {
		t.Fatalf("NewPersistedSession error = %v, want nil", err)
	}

	replaceFrom := 2
	stream, err := session.Send(context.Background(), "edited second", SendOptions{ReplaceFrom: &replaceFrom})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	collectEvents(t, stream)

	if len(store.replaced) != 1 {
		t.Fatalf("replace count = %d, want 1", len(store.replaced))
	}
	replaced := store.replaced[0]
	if replaced.conversationID != 42 || replaced.keepMessages != 2 || replaced.user.Text() != "edited second" || replaced.assistant.Text() != "edited answer" {
		t.Fatalf("replaced turn = %#v, want replacement at message index 2", replaced)
	}
}

func TestPersistentSessionFallsBackWithoutStore(t *testing.T) {
	session, err := NewPersistentService(dummy.NewClient(), nil).NewPersistedSession(context.Background(), 42)
	if err != nil {
		t.Fatalf("NewPersistedSession error = %v, want nil", err)
	}
	if session.store != nil || session.conversationID != 0 {
		t.Fatalf("session = %#v, want ephemeral session", session)
	}
}

func TestPersistentSessionReturnsHistoryLoadFailure(t *testing.T) {
	errLoad := errors.New("load failed")
	_, err := NewPersistentService(dummy.NewClient(), &chatStore{messagesErr: errLoad}).NewPersistedSession(context.Background(), 42)
	if !errors.Is(err, errLoad) {
		t.Fatalf("NewPersistedSession error = %v, want load error", err)
	}
}

func TestPersistentSessionDoesNotAppendFailedOrClosedTurn(t *testing.T) {
	t.Run("failed stream", func(t *testing.T) {
		store := &chatStore{}
		session, err := NewPersistentService(failingClient{}, store).NewPersistedSession(context.Background(), 42)
		if err != nil {
			t.Fatalf("NewPersistedSession error = %v, want nil", err)
		}
		stream, err := session.Send(context.Background(), "hello", SendOptions{})
		if err != nil {
			t.Fatalf("Send() error = %v, want nil", err)
		}

		_, err = stream.Next()
		if err == nil {
			t.Fatalf("Next() error = nil, want failure")
		}
		if len(store.appended) != 0 || len(store.replaced) != 0 {
			t.Fatalf("stored turn count = appended %d replaced %d, want 0", len(store.appended), len(store.replaced))
		}
	})

	t.Run("closed stream without partial commit", func(t *testing.T) {
		store := &chatStore{}
		session, err := NewPersistentService(eventClient{events: []llm.Event{{Type: llm.EventTextDelta, Delta: "partial"}}}, store).NewPersistedSession(context.Background(), 42)
		if err != nil {
			t.Fatalf("NewPersistedSession error = %v, want nil", err)
		}
		stream, err := session.Send(context.Background(), "hello", SendOptions{})
		if err != nil {
			t.Fatalf("Send() error = %v, want nil", err)
		}
		if err := stream.Close(); err != nil {
			t.Fatalf("Close() error = %v, want nil", err)
		}
		if len(store.appended) != 0 || len(store.replaced) != 0 {
			t.Fatalf("stored turn count = appended %d replaced %d, want 0", len(store.appended), len(store.replaced))
		}
	})
}

func TestSessionCanCommitPartialTurn(t *testing.T) {
	session := NewService(eventClient{
		events: []llm.Event{
			{Type: llm.EventReasoningDelta, Delta: "thinking"},
			{Type: llm.EventTextDelta, Delta: "partial"},
		},
	}).NewSession()

	stream, err := session.Send(context.Background(), "hello", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	if event, nextErr := stream.Next(); nextErr != nil || event.Type != llm.EventReasoningDelta {
		t.Fatalf("first Next() = %#v, %v; want reasoning delta", event, nextErr)
	}
	if event, nextErr := stream.Next(); nextErr != nil || event.Type != llm.EventTextDelta {
		t.Fatalf("second Next() = %#v, %v; want text delta", event, nextErr)
	}
	if err := stream.CommitPartial(); err != nil {
		t.Fatalf("CommitPartial() error = %v, want nil", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}

	messages := session.Messages()
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	if messages[0].Text() != "hello" || messages[1].Text() != "partial" {
		t.Fatalf("messages = %#v, want committed partial turn", messages)
	}
	if messages[1].Parts[0].Type != llm.PartReasoning || messages[1].Parts[0].Text != "thinking" {
		t.Fatalf("assistant parts = %#v, want partial reasoning and text", messages[1].Parts)
	}
}

func TestPersistentSessionReturnsAppendFailureAndDoesNotKeepInFlight(t *testing.T) {
	store := &chatStore{appendErr: errors.New("append failed")}
	session, err := NewPersistentService(dummy.NewClient(dummy.Turn{TextChunks: []string{"answer"}}), store).NewPersistedSession(context.Background(), 42)
	if err != nil {
		t.Fatalf("NewPersistedSession error = %v, want nil", err)
	}
	stream, err := session.Send(context.Background(), "hello", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	for {
		_, err = stream.Next()
		if err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "append failed") {
		t.Fatalf("stream error = %v, want append failed", err)
	}
	retry, err := session.Send(context.Background(), "retry", SendOptions{})
	if err != nil {
		t.Fatalf("retry Send() error = %v, want nil", err)
	}
	if closeErr := retry.Close(); closeErr != nil {
		t.Fatalf("retry Close() error = %v, want nil", closeErr)
	}
}

func TestSessionSendIncludesRenderingInstructionsWithoutPersistingThem(t *testing.T) {
	client := dummy.NewClient(
		dummy.Turn{TextChunks: []string{"first answer"}},
		dummy.Turn{TextChunks: []string{"second answer"}},
	)
	session := NewService(client).NewSession()

	first, err := session.Send(context.Background(), "first", SendOptions{
		RenderingInstructions: "  render for web  ",
	})
	if err != nil {
		t.Fatalf("first Send() error = %v, want nil", err)
	}
	collectEvents(t, first)

	second, err := session.Send(context.Background(), "second", SendOptions{
		RenderingInstructions: "render for web",
	})
	if err != nil {
		t.Fatalf("second Send() error = %v, want nil", err)
	}
	collectEvents(t, second)

	requests := client.Requests()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	for i, request := range requests {
		if request.Instructions != "render for web" {
			t.Fatalf("request %d instructions = %q, want trimmed rendering instructions", i, request.Instructions)
		}
		for _, message := range request.Messages {
			if message.Role == llm.RoleSystem {
				t.Fatalf("request %d messages = %#v, did not expect persisted system message", i, request.Messages)
			}
		}
	}
	if len(requests[1].Messages) != 3 {
		t.Fatalf("second request message count = %d, want prior user, assistant, next user", len(requests[1].Messages))
	}
	if messages := session.Messages(); len(messages) != 4 {
		t.Fatalf("stored message count = %d, want only two user/assistant turns", len(messages))
	}
}

func TestWebRenderingInstructionsDescribeSupportedOutputWithoutOverpromising(t *testing.T) {
	prompt := WebRenderingInstructions()
	for _, want := range []string{
		"sanitized Markdown",
		"GFM tables",
		"fenced code blocks",
		"language identifiers",
		"\\(...\\)",
		"$$...$$",
		"```mermaid",
		":::artifact title=\"Short title\" type=\"text/markdown\"",
		"text/markdown",
		"text/md",
		"application/vnd.mermaid",
		"application/vnd.code",
		"preserve them",
		"do not invent them",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("WebRenderingInstructions() = %q, want substring %q", prompt, want)
		}
	}

	for _, unsupportedClaim := range []string{
		"text/html",
		"React components are supported",
		"SVG rendering is supported",
		"single-dollar inline math is supported",
	} {
		if strings.Contains(prompt, unsupportedClaim) {
			t.Fatalf("WebRenderingInstructions() = %q, did not expect unsupported claim %q", prompt, unsupportedClaim)
		}
	}
}

func TestSessionSendRejectsEmptyPrompt(t *testing.T) {
	session := NewService(dummy.NewClient()).NewSession()

	_, err := session.Send(context.Background(), "  ", SendOptions{})
	if !errors.Is(err, ErrEmptyPrompt) {
		t.Fatalf("Send() error = %v, want %v", err, ErrEmptyPrompt)
	}
}

func TestSessionDoesNotStoreFailedTurn(t *testing.T) {
	session := NewService(failingClient{}).NewSession()

	stream, err := session.Send(context.Background(), "hello", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	_, err = stream.Next()
	if err == nil {
		t.Fatalf("Next() error = nil, want failure")
	}

	if got := len(session.Messages()); got != 0 {
		t.Fatalf("message count after failed stream = %d, want 0", got)
	}
}

func TestSessionReleasesTurnWhenClientStreamFailsToStart(t *testing.T) {
	session := NewService(streamStartFailingClient{}).NewSession()

	_, err := session.Send(context.Background(), "hello", SendOptions{})
	if err == nil {
		t.Fatalf("Send() error = nil, want stream start failure")
	}

	session.client = eventClient{events: []llm.Event{{Type: llm.EventCompleted}}}
	stream, err := session.Send(context.Background(), "retry", SendOptions{})
	if err != nil {
		t.Fatalf("retry Send() error = %v, want nil", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
}

func TestSessionReturnsEventErrorsAndDoesNotStoreTurn(t *testing.T) {
	session := NewService(eventClient{
		events: []llm.Event{{Type: llm.EventError, Err: errors.New("model failed")}},
	}).NewSession()

	stream, err := session.Send(context.Background(), "hello", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	event, err := stream.Next()
	if err == nil || event.Type != llm.EventError {
		t.Fatalf("Next() = %#v, %v; want event error", event, err)
	}
	if got := len(session.Messages()); got != 0 {
		t.Fatalf("message count after event error = %d, want 0", got)
	}
}

func TestSessionRejectsConcurrentTurnsUntilStreamCloses(t *testing.T) {
	session := NewService(eventClient{
		events: []llm.Event{{Type: llm.EventCompleted}},
	}).NewSession()

	first, err := session.Send(context.Background(), "first", SendOptions{})
	if err != nil {
		t.Fatalf("first Send() error = %v, want nil", err)
	}

	_, err = session.Send(context.Background(), "second", SendOptions{})
	if !errors.Is(err, ErrTurnInProgress) {
		t.Fatalf("concurrent Send() error = %v, want %v", err, ErrTurnInProgress)
	}

	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v, want nil", closeErr)
	}

	second, err := session.Send(context.Background(), "second", SendOptions{})
	if err != nil {
		t.Fatalf("second Send() after close error = %v, want nil", err)
	}
	if closeErr := second.Close(); closeErr != nil {
		t.Fatalf("second Close() error = %v, want nil", closeErr)
	}
}

func TestSessionMergesCompletedTextPartWithoutDuplicatingDeltas(t *testing.T) {
	session := NewService(eventClient{
		events: []llm.Event{
			{Type: llm.EventTextDelta, Delta: "hel"},
			{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartText, Text: "hello"}},
			{Type: llm.EventCompleted},
		},
	}).NewSession()

	stream, err := session.Send(context.Background(), "prompt", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	collectEvents(t, stream)

	messages := session.Messages()
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	if got := messages[1].Text(); got != "hello" {
		t.Fatalf("assistant text = %q, want final completed text without duplicated delta", got)
	}
}

func TestSessionAppendsCompletedTextAfterNonTextPart(t *testing.T) {
	session := NewService(eventClient{
		events: []llm.Event{
			{Type: llm.EventTextDelta, Delta: "intro"},
			{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartError, Text: "model warning"}},
			{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartText, Text: "outro"}},
			{Type: llm.EventCompleted},
		},
	}).NewSession()

	stream, err := session.Send(context.Background(), "prompt", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	collectEvents(t, stream)

	messages := session.Messages()
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	parts := messages[1].Parts
	if len(parts) != 3 {
		t.Fatalf("assistant parts = %#v, want text, error, text", parts)
	}
	if parts[0].Type != llm.PartText || parts[0].Text != "intro" {
		t.Fatalf("first assistant part = %#v, want intro text", parts[0])
	}
	if parts[1].Type != llm.PartError || parts[1].Text != "model warning" {
		t.Fatalf("second assistant part = %#v, want error", parts[1])
	}
	if parts[2].Type != llm.PartText || parts[2].Text != "outro" {
		t.Fatalf("third assistant part = %#v, want outro text", parts[2])
	}
	if got := messages[1].Text(); got != "introoutro" {
		t.Fatalf("assistant text = %q, want introoutro", got)
	}
}

func TestSessionStoresCompletedPartsWithoutPriorDeltas(t *testing.T) {
	session := NewService(eventClient{
		events: []llm.Event{
			{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartReasoning, ID: "rs_1", Text: "thinking", Summary: []string{"summary"}}},
			{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartText, Text: "answer"}},
			{Type: llm.EventCompleted},
		},
	}).NewSession()

	stream, err := session.Send(context.Background(), "prompt", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	collectEvents(t, stream)

	messages := session.Messages()
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	if len(messages[1].Parts) != 2 {
		t.Fatalf("assistant parts = %#v, want reasoning and text parts", messages[1].Parts)
	}
	if messages[1].Parts[0].ID != "rs_1" || messages[1].Text() != "answer" {
		t.Fatalf("assistant message = %#v, want completed reasoning and text", messages[1])
	}
}

func TestSessionSkipsEmptyDeltasAndCompletedParts(t *testing.T) {
	session := NewService(eventClient{
		events: []llm.Event{
			{Type: llm.EventReasoningDelta},
			{Type: llm.EventTextDelta},
			{Type: llm.EventOutputItemDone, Part: llm.Part{}},
			{Type: llm.EventCompleted},
		},
	}).NewSession()

	stream, err := session.Send(context.Background(), "prompt", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	collectEvents(t, stream)

	messages := session.Messages()
	if len(messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(messages))
	}
	if len(messages[1].Parts) != 0 {
		t.Fatalf("assistant parts = %#v, want none", messages[1].Parts)
	}
}

func TestSessionMergesReasoningAfterTrailingText(t *testing.T) {
	session := NewService(eventClient{
		events: []llm.Event{
			{Type: llm.EventReasoningDelta, Delta: "rough"},
			{Type: llm.EventTextDelta, Delta: "answer"},
			{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartReasoning, ID: "rs_1", Text: "final", Summary: []string{"summary"}}},
			{Type: llm.EventCompleted},
		},
	}).NewSession()

	stream, err := session.Send(context.Background(), "prompt", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	collectEvents(t, stream)

	parts := session.Messages()[1].Parts
	if len(parts) != 2 {
		t.Fatalf("assistant parts = %#v, want reasoning and text", parts)
	}
	if parts[0].Type != llm.PartReasoning || parts[0].Text != "final" || parts[0].ID != "rs_1" {
		t.Fatalf("reasoning part = %#v, want merged completed metadata", parts[0])
	}
	if parts[1].Type != llm.PartText || parts[1].Text != "answer" {
		t.Fatalf("text part = %#v, want trailing text preserved", parts[1])
	}
}

func TestTurnStreamFinalizeAndClonePartsGuards(t *testing.T) {
	session := NewService(dummy.NewClient()).NewSession()
	turn := &TurnStream{
		session:     session,
		userMessage: llm.NewTextMessage(llm.RoleUser, "hello"),
	}

	if err := turn.finalize(false); err != nil {
		t.Fatalf("finalize before completion error = %v, want nil", err)
	}
	if got := len(session.Messages()); got != 0 {
		t.Fatalf("messages before completion = %d, want 0", got)
	}

	turn.completed = true
	if err := turn.finalize(false); err != nil {
		t.Fatalf("finalize after completion error = %v, want nil", err)
	}
	if err := turn.finalize(false); err != nil {
		t.Fatalf("second finalize error = %v, want nil", err)
	}
	if got := len(session.Messages()); got != 2 {
		t.Fatalf("messages after double finalize = %d, want exactly 2", got)
	}

	if cloned := cloneParts(nil); cloned != nil {
		t.Fatalf("cloneParts(nil) = %#v, want nil", cloned)
	}
}

func TestTurnStreamRecordErrorTreatsContextCancellationAsCancelled(t *testing.T) {
	cancelled := &TurnStream{}
	cancelled.recordError(context.Canceled)
	if !cancelled.recorded {
		t.Fatalf("cancelled stream was not recorded")
	}

	failed := &TurnStream{}
	failed.recordError(errors.New("stream failed"))
	if !failed.recorded {
		t.Fatalf("failed stream was not recorded")
	}
	failed.recordError(context.Canceled)
	if !failed.recorded {
		t.Fatalf("second recordError changed recorded state")
	}
}

func TestSessionMessagesReturnsDeepCopy(t *testing.T) {
	session := NewService(eventClient{
		events: []llm.Event{
			{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartReasoning, Summary: []string{"summary"}}},
			{Type: llm.EventCompleted},
		},
	}).NewSession()

	stream, err := session.Send(context.Background(), "prompt", SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	collectEvents(t, stream)

	messages := session.Messages()
	messages[1].Parts[0].Summary[0] = "changed"

	if got := session.Messages()[1].Parts[0].Summary[0]; got != "summary" {
		t.Fatalf("stored summary = %q, want unchanged copy", got)
	}
}

type failingClient struct{}

func (failingClient) Stream(context.Context, llm.Request) (llm.Stream, error) {
	return failingStream{}, nil
}

type failingStream struct{}

func (failingStream) Next() (llm.Event, error) {
	return llm.Event{}, errors.New("stream failed")
}

func (failingStream) Close() error {
	return nil
}

type streamStartFailingClient struct{}

func (streamStartFailingClient) Stream(context.Context, llm.Request) (llm.Stream, error) {
	return nil, errors.New("stream start failed")
}

type eventClient struct {
	events []llm.Event
}

func (c eventClient) Stream(context.Context, llm.Request) (llm.Stream, error) {
	return &eventStream{events: append([]llm.Event(nil), c.events...)}, nil
}

type eventStream struct {
	events []llm.Event
	index  int
}

func (s *eventStream) Next() (llm.Event, error) {
	if s.index >= len(s.events) {
		return llm.Event{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (*eventStream) Close() error {
	return nil
}

type chatStore struct {
	messages    []llm.Message
	messagesErr error
	appended    []appendedTurn
	replaced    []replacedTurn
	appendErr   error
}

type appendedTurn struct {
	conversationID int64
	user           llm.Message
	assistant      llm.Message
}

type replacedTurn struct {
	conversationID int64
	keepMessages   int
	user           llm.Message
	assistant      llm.Message
}

func (s *chatStore) Messages(context.Context, int64) ([]llm.Message, error) {
	if s.messagesErr != nil {
		return nil, s.messagesErr
	}
	return llm.CloneMessages(s.messages), nil
}

func (s *chatStore) AppendTurn(_ context.Context, conversationID int64, user, assistant llm.Message) error {
	if s.appendErr != nil {
		return s.appendErr
	}
	s.appended = append(s.appended, appendedTurn{
		conversationID: conversationID,
		user:           user.Clone(),
		assistant:      assistant.Clone(),
	})
	return nil
}

func (s *chatStore) ReplaceTailAndAppendTurn(_ context.Context, conversationID int64, keepMessages int, user, assistant llm.Message) error {
	if s.appendErr != nil {
		return s.appendErr
	}
	s.replaced = append(s.replaced, replacedTurn{
		conversationID: conversationID,
		keepMessages:   keepMessages,
		user:           user.Clone(),
		assistant:      assistant.Clone(),
	})
	return nil
}

func collectEvents(t *testing.T, stream *TurnStream) []llm.Event {
	t.Helper()
	defer stream.Close()

	var events []llm.Event
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return events
		}
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		events = append(events, event)
	}
}
