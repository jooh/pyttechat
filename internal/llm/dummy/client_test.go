package dummy

import (
	"context"
	"errors"
	"io"
	"testing"

	"example.com/llm-chat-web/internal/llm"
)

func TestStreamReturnsDeterministicReasoningAndTextEvents(t *testing.T) {
	client := NewClient(Turn{
		ReasoningChunks: []string{"think", "ing"},
		TextChunks:      []string{"hel", "lo"},
		ReasoningPart: llm.Part{
			Type:             llm.PartReasoning,
			ID:               "rs_dummy",
			Summary:          []string{"thinking"},
			EncryptedContent: "encrypted_dummy",
		},
	})

	stream, err := client.Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	defer stream.Close()

	var got []llm.Event
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
		got = append(got, event)
	}

	wantTypes := []llm.EventType{
		llm.EventReasoningDelta,
		llm.EventReasoningDelta,
		llm.EventOutputItemDone,
		llm.EventTextDelta,
		llm.EventTextDelta,
		llm.EventCompleted,
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %#v", len(got), len(wantTypes), got)
	}
	for i, want := range wantTypes {
		if got[i].Type != want {
			t.Fatalf("event %d type = %q, want %q", i, got[i].Type, want)
		}
	}
	if got[2].Part.ID != "rs_dummy" {
		t.Fatalf("reasoning part id = %q, want rs_dummy", got[2].Part.ID)
	}
	if got[2].Part.EncryptedContent != "encrypted_dummy" {
		t.Fatalf("reasoning encrypted content = %q, want encrypted_dummy", got[2].Part.EncryptedContent)
	}
	if got[5].ResponseID != "dummy-response-1" {
		t.Fatalf("completed response id = %q, want dummy-response-1", got[5].ResponseID)
	}
}

func TestStreamRecordsRequestsAcrossTurns(t *testing.T) {
	client := NewClient()

	first, err := client.Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "first")},
	})
	if err != nil {
		t.Fatalf("first Stream() error = %v, want nil", err)
	}
	drain(t, first)

	secondRequest := llm.Request{
		Messages: []llm.Message{
			llm.NewTextMessage(llm.RoleUser, "first"),
			llm.NewTextMessage(llm.RoleAssistant, "first answer"),
			llm.NewTextMessage(llm.RoleUser, "second"),
		},
	}
	second, err := client.Stream(context.Background(), secondRequest)
	if err != nil {
		t.Fatalf("second Stream() error = %v, want nil", err)
	}
	drain(t, second)

	requests := client.Requests()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if requests[1].Messages[2].Text() != "second" {
		t.Fatalf("second request last message text = %q, want second", requests[1].Messages[2].Text())
	}
}

func drain(t *testing.T, stream llm.Stream) {
	t.Helper()
	defer stream.Close()
	for {
		_, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("Next() error = %v, want nil", err)
		}
	}
}
