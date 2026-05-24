package chat

import (
	"context"
	"errors"
	"io"
	"testing"

	"example.com/llm-chat-web/internal/llm"
	"example.com/llm-chat-web/internal/llm/dummy"
)

func TestSessionSendStreamsAndStoresCompletedTurn(t *testing.T) {
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

	stream, err := session.Send(context.Background(), "  hello  ", SendOptions{Model: "test-model"})
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
