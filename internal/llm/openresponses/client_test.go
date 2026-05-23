package openresponses

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/llm-chat-web/internal/llm"
)

func TestClientStreamsOpenResponsesEvents(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.reasoning.delta\n")
		_, _ = io.WriteString(w, `data: {"type":"response.reasoning.delta","sequence_number":1,"item_id":"rs_1","output_index":0,"content_index":0,"delta":"Think"}`+"\n\n")
		_, _ = io.WriteString(w, "event: response.output_item.done\n")
		_, _ = io.WriteString(w, `data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Think"}],"encrypted_content":"encrypted"}}`+"\n\n")
		_, _ = io.WriteString(w, "event: response.output_text.delta\n")
		_, _ = io.WriteString(w, `data: {"type":"response.output_text.delta","sequence_number":3,"item_id":"msg_1","output_index":1,"content_index":0,"delta":"Hello"}`+"\n\n")
		_, _ = io.WriteString(w, "event: response.completed\n")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","sequence_number":4,"response":{"id":"resp_1","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3,"output_tokens_details":{"reasoning_tokens":4}}}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(server.URL)
	stream, err := client.Stream(context.Background(), llm.Request{
		Model: "test-model",
		Reasoning: llm.ReasoningOptions{
			Effort: "low",
		},
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	defer stream.Close()

	events := drainEvents(t, stream)
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4: %#v", len(events), events)
	}
	if events[0].Type != llm.EventReasoningDelta || events[0].Delta != "Think" {
		t.Fatalf("first event = %#v, want reasoning delta Think", events[0])
	}
	if events[1].Part.ID != "rs_1" || events[1].Part.EncryptedContent != "encrypted" {
		t.Fatalf("reasoning part = %#v, want id and encrypted content", events[1].Part)
	}
	if events[2].Type != llm.EventTextDelta || events[2].Delta != "Hello" {
		t.Fatalf("third event = %#v, want text delta Hello", events[2])
	}
	if events[3].ResponseID != "resp_1" {
		t.Fatalf("completed response id = %q, want resp_1", events[3].ResponseID)
	}
	if events[3].Usage == nil || events[3].Usage.ReasoningTokens != 4 {
		t.Fatalf("usage = %#v, want reasoning tokens", events[3].Usage)
	}

	if requestBody["model"] != "test-model" {
		t.Fatalf("request model = %v, want test-model", requestBody["model"])
	}
	if requestBody["stream"] != true {
		t.Fatalf("request stream = %v, want true", requestBody["stream"])
	}
	if requestBody["store"] != false {
		t.Fatalf("request store = %v, want false", requestBody["store"])
	}
	include := requestBody["include"].([]any)
	if include[0] != "reasoning.encrypted_content" {
		t.Fatalf("request include = %#v, want reasoning.encrypted_content", include)
	}
	reasoning := requestBody["reasoning"].(map[string]any)
	if reasoning["summary"] != "auto" || reasoning["effort"] != "low" {
		t.Fatalf("request reasoning = %#v, want summary auto and effort low", reasoning)
	}
}

func TestClientMapsPriorReasoningIntoInput(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp_1"}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(server.URL)
	stream, err := client.Stream(context.Background(), llm.Request{
		Messages: []llm.Message{
			llm.NewTextMessage(llm.RoleUser, "first"),
			{
				Role: llm.RoleAssistant,
				Parts: []llm.Part{
					{
						Type:             llm.PartReasoning,
						ID:               "rs_1",
						Summary:          []string{"reasoned"},
						EncryptedContent: "encrypted",
					},
					{Type: llm.PartText, Text: "answer"},
				},
			},
			llm.NewTextMessage(llm.RoleUser, "second"),
		},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	drainEvents(t, stream)

	input := requestBody["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("input count = %d, want 4: %#v", len(input), input)
	}
	reasoning := input[1].(map[string]any)
	if reasoning["type"] != "reasoning" || reasoning["id"] != "rs_1" {
		t.Fatalf("reasoning input = %#v, want reasoning item", reasoning)
	}
	if reasoning["encrypted_content"] != "encrypted" {
		t.Fatalf("encrypted_content = %v, want encrypted", reasoning["encrypted_content"])
	}
	summary := reasoning["summary"].([]any)[0].(map[string]any)
	if summary["text"] != "reasoned" {
		t.Fatalf("summary = %#v, want reasoned", summary)
	}
	assistant := input[2].(map[string]any)
	if assistant["role"] != "assistant" || assistant["content"] != "answer" {
		t.Fatalf("assistant input = %#v, want assistant answer", assistant)
	}
}

func TestClientReturnsStreamErrorEventsAsErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"error","error":{"message":"proxy failed"}}`+"\n\n")
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	defer stream.Close()

	_, err = stream.Next()
	if err == nil || !errors.Is(err, ErrStreamFailed) {
		t.Fatalf("Next() error = %v, want ErrStreamFailed", err)
	}
}

func drainEvents(t *testing.T, stream llm.Stream) []llm.Event {
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
