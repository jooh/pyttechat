package openresponses

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/llm-chat-web/internal/llm"
	"example.com/llm-chat-web/internal/llm/openresponses/fakeprovider"
)

func TestNewClientSetsDefaultTimeout(t *testing.T) {
	client := NewClient("http://example.test")

	if client.httpClient.Timeout <= 0 {
		t.Fatalf("http client timeout = %s, want bounded default timeout", client.httpClient.Timeout)
	}
}

func TestClientStreamsFromFakeProvider(t *testing.T) {
	server := httptest.NewServer(fakeprovider.NewHandler())
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Model:    "dummy-responses",
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}

	events := drainEvents(t, stream)
	var text string
	var completed llm.Event
	for _, event := range events {
		switch event.Type {
		case llm.EventTextDelta:
			text += event.Delta
		case llm.EventCompleted:
			completed = event
		}
	}
	if text != "Echo: hello" {
		t.Fatalf("streamed text = %q, want fake provider echo", text)
	}
	if completed.ResponseID == "" {
		t.Fatalf("completed response id is empty")
	}
	if completed.Usage == nil || completed.Usage.TotalTokens == 0 {
		t.Fatalf("completed usage = %#v, want deterministic usage", completed.Usage)
	}
}

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
			Summary: "auto",
			Effort:  "low",
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
	include := requireSlice(t, requestBody["include"], "include")
	if include[0] != "reasoning.encrypted_content" {
		t.Fatalf("request include = %#v, want reasoning.encrypted_content", include)
	}
	reasoning := requireMap(t, requestBody["reasoning"], "reasoning")
	if reasoning["summary"] != "auto" || reasoning["effort"] != "low" {
		t.Fatalf("request reasoning = %#v, want summary auto and effort low", reasoning)
	}
}

func TestClientBuildsResponsesURLWithExistingPath(t *testing.T) {
	client := NewClient("https://proxy.example/base/")

	if got := client.responsesURL(); got != "https://proxy.example/base/v1/responses" {
		t.Fatalf("responsesURL() = %q, want base path preserved", got)
	}
}

func TestClientReturnsNonSuccessStatusAsStreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "proxy unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	_, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err == nil || !errors.Is(err, ErrStreamFailed) {
		t.Fatalf("Stream() error = %v, want ErrStreamFailed", err)
	}
	if !strings.Contains(err.Error(), "status 502") || !strings.Contains(err.Error(), "proxy unavailable") {
		t.Fatalf("Stream() error = %q, want status and body", err)
	}
}

func TestClientReturnsMarshalRequestAndHTTPClientErrors(t *testing.T) {
	t.Run("marshal error", func(t *testing.T) {
		original := marshalJSON
		marshalJSON = func(any) ([]byte, error) {
			return nil, errors.New("marshal failed")
		}
		t.Cleanup(func() {
			marshalJSON = original
		})

		_, err := NewClient("http://example.test").Stream(context.Background(), llm.Request{})
		if err == nil || !strings.Contains(err.Error(), "marshal failed") {
			t.Fatalf("Stream() error = %v, want marshal failure", err)
		}
	})

	t.Run("request construction error", func(t *testing.T) {
		_, err := NewClient("http://[::1").Stream(context.Background(), llm.Request{})
		if err == nil {
			t.Fatalf("Stream() error = nil, want request construction error")
		}
	})

	t.Run("http client error", func(t *testing.T) {
		client := NewClient("http://example.test")
		client.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport failed")
		})}

		_, err := client.Stream(context.Background(), llm.Request{})
		if err == nil || !strings.Contains(err.Error(), "transport failed") {
			t.Fatalf("Stream() error = %v, want transport failure", err)
		}
	})
}

func TestClientBuildsResponsesURLForRelativeBase(t *testing.T) {
	client := NewClient("relative/proxy")

	if got := client.responsesURL(); got != "relative/proxy/v1/responses" {
		t.Fatalf("responsesURL() = %q, want relative fallback", got)
	}
}

func TestClientIgnoresCommentsAndMapsMultilineFailedEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": keepalive\n")
		_, _ = io.WriteString(w, "event: response.output_text.delta\n")
		_, _ = io.WriteString(w, `data: {"type":"response.output_text.delta",`+"\n")
		_, _ = io.WriteString(w, `data: "delta":"Hi"}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.failed","response":{"error":{"message":"proxy failed late"}}}`+"\n\n")
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v, want nil", err)
	}
	if event.Type != llm.EventTextDelta || event.Delta != "Hi" {
		t.Fatalf("first event = %#v, want text delta Hi", event)
	}

	_, err = stream.Next()
	if err == nil || !errors.Is(err, ErrStreamFailed) || !strings.Contains(err.Error(), "proxy failed late") {
		t.Fatalf("second Next() error = %v, want response.failed stream error", err)
	}
}

func TestClientDispatchesPartialFrameAtEOFAndPostDoneEOF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.output_text.delta","delta":"partial"}`)
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v, want nil", err)
	}
	if event.Type != llm.EventTextDelta || event.Delta != "partial" {
		t.Fatalf("first event = %#v, want partial text", event)
	}
	if _, nextErr := stream.Next(); !errors.Is(nextErr, io.EOF) {
		t.Fatalf("second Next() error = %v, want EOF", nextErr)
	}

	doneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer doneServer.Close()

	doneStream, err := NewClient(doneServer.URL).Stream(context.Background(), llm.Request{})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	defer doneStream.Close()
	if _, err := doneStream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("first done Next() error = %v, want EOF", err)
	}
	if _, err := doneStream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("post-done Next() error = %v, want EOF", err)
	}
}

func TestClientReturnsInvalidStreamJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {bad json}\n\n")
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	defer stream.Close()

	if _, err := stream.Next(); err == nil {
		t.Fatalf("Next() error = nil, want JSON error")
	}
}

func TestClientSkipsEmptyFinalTextAndMalformedCompletedItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.output_text.done","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"text":""}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"user","content":[{"type":"output_text","text":"skip"}]}}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"id":"msg_2","type":"message","status":"completed","role":"assistant","content":"not-array"}}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","sequence_number":4,"response":{"id":"resp_1"}}`+"\n\n")
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}
	if event.Type != llm.EventCompleted {
		t.Fatalf("event = %#v, want only completed event", event)
	}
}

func TestClientSendsBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization header = %q, want bearer token", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp_1"}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	stream, err := NewClientWithOptions(server.URL, Options{BearerToken: "test-token"}).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	drainEvents(t, stream)
}

func TestClientStreamsOutputTextDoneWhenNoDeltaArrived(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.output_text.done","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"text":"Hello"}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","sequence_number":2,"response":{"id":"resp_1"}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}

	events := drainEvents(t, stream)
	if len(events) != 2 {
		t.Fatalf("event count = %d, want text and completed events: %#v", len(events), events)
	}
	if events[0].Type != llm.EventTextDelta || events[0].Delta != "Hello" {
		t.Fatalf("first event = %#v, want text delta Hello", events[0])
	}
}

func TestClientStreamsMessageOutputItemDoneWhenNoTextEventsArrived(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.output_item.done","sequence_number":1,"output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello from item"}]}}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","sequence_number":2,"response":{"id":"resp_1"}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}

	events := drainEvents(t, stream)
	if len(events) != 2 {
		t.Fatalf("event count = %d, want text and completed events: %#v", len(events), events)
	}
	if events[0].Type != llm.EventTextDelta || events[0].Delta != "Hello from item" {
		t.Fatalf("first event = %#v, want text from completed message item", events[0])
	}
}

func TestClientDoesNotDuplicateFinalTextAfterDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hello"}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.output_text.done","sequence_number":2,"item_id":"msg_1","output_index":0,"content_index":0,"text":"Hello"}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","sequence_number":4,"response":{"id":"resp_1"}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}

	events := drainEvents(t, stream)
	var text string
	for _, event := range events {
		if event.Type == llm.EventTextDelta {
			text += event.Delta
		}
	}
	if text != "Hello" {
		t.Fatalf("streamed text = %q, want exactly one final answer", text)
	}
}

func TestClientSkipsSeenAndInvalidMessageOutputItemContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.output_text.done","sequence_number":1,"item_id":"msg_1","output_index":0,"content_index":3,"text":"Hello"}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"id":"msg_1","type":"message","status":"completed","role":"assistant","content":["ignored",{"type":"input_text","text":"skip"},{"type":"output_text","text":""},{"type":"output_text","text":"Hello"},{"type":"text","text":"!"}]}}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp_1"}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}

	events := drainEvents(t, stream)
	var text string
	for _, event := range events {
		if event.Type == llm.EventTextDelta {
			text += event.Delta
		}
	}
	if text != "Hello!" {
		t.Fatalf("streamed text = %q, want deduplicated fallback text", text)
	}
}

func TestClientOmitsReasoningByDefault(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp_1"}}`+"\n\n")
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	drainEvents(t, stream)

	if _, ok := requestBody["reasoning"]; ok {
		t.Fatalf("request reasoning = %#v, want omitted by default", requestBody["reasoning"])
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

	input := requireSlice(t, requestBody["input"], "input")
	if len(input) != 4 {
		t.Fatalf("input count = %d, want 4: %#v", len(input), input)
	}
	reasoning := requireMap(t, input[1], "input[1]")
	if reasoning["type"] != "reasoning" || reasoning["id"] != "rs_1" {
		t.Fatalf("reasoning input = %#v, want reasoning item", reasoning)
	}
	if reasoning["encrypted_content"] != "encrypted" {
		t.Fatalf("encrypted_content = %v, want encrypted", reasoning["encrypted_content"])
	}
	summaryValues := requireSlice(t, reasoning["summary"], "reasoning.summary")
	summary := requireMap(t, summaryValues[0], "reasoning.summary[0]")
	if summary["text"] != "reasoned" {
		t.Fatalf("summary = %#v, want reasoned", summary)
	}
	assistant := requireMap(t, input[2], "input[2]")
	if assistant["role"] != "assistant" || assistant["content"] != "answer" {
		t.Fatalf("assistant input = %#v, want assistant answer", assistant)
	}
}

func TestOpenResponsesInputSkipsEmptyNonAssistantMessages(t *testing.T) {
	input := openResponsesInput([]llm.Message{
		{Role: llm.RoleUser, Parts: []llm.Part{{Type: llm.PartReasoning, Text: "hidden"}}},
		llm.NewTextMessage(llm.RoleUser, "visible"),
	})

	if len(input) != 1 {
		t.Fatalf("input count = %d, want only visible message: %#v", len(input), input)
	}
	if input[0]["content"] != "visible" {
		t.Fatalf("input = %#v, want visible message", input)
	}
}

func TestOutputItemPartExtractsReasoningContentAndSkipsMalformedSummary(t *testing.T) {
	part := outputItemPart(map[string]any{
		"item": map[string]any{
			"type":              "reasoning",
			"id":                "rs_1",
			"encrypted_content": "encrypted",
			"content": []any{
				"ignored",
				map[string]any{"text": "reasoning text"},
			},
			"summary": []any{
				"ignored",
				map[string]any{"text": ""},
				map[string]any{"text": "summary text"},
			},
		},
	})

	if part.Type != llm.PartReasoning || part.ID != "rs_1" || part.EncryptedContent != "encrypted" {
		t.Fatalf("part metadata = %#v, want reasoning metadata", part)
	}
	if part.Text != "reasoning text" {
		t.Fatalf("part text = %q, want reasoning text", part.Text)
	}
	if len(part.Summary) != 1 || part.Summary[0] != "summary text" {
		t.Fatalf("summary = %#v, want one summary text", part.Summary)
	}
}

func TestOutputItemPartHandlesNonArraySummary(t *testing.T) {
	part := outputItemPart(map[string]any{
		"item": map[string]any{
			"type":    "reasoning",
			"summary": "not-array",
		},
	})

	if part.Type != llm.PartReasoning {
		t.Fatalf("part type = %q, want reasoning", part.Type)
	}
	if part.Summary != nil {
		t.Fatalf("summary = %#v, want nil", part.Summary)
	}
}

func TestStreamInitializesNilTextSeenAndHandlesIntFields(t *testing.T) {
	s := &stream{}

	event, ok, err := s.mapPayload(map[string]any{
		"type":          "response.output_text.done",
		"item_id":       "msg_1",
		"output_index":  1,
		"content_index": 2,
		"text":          "Hello",
	})
	if err != nil || !ok {
		t.Fatalf("mapPayload() = %#v, %v, %v; want event", event, ok, err)
	}
	if event.Type != llm.EventTextDelta || event.Delta != "Hello" {
		t.Fatalf("event = %#v, want text delta", event)
	}
	if !s.textSeen["msg_1/1/2"] {
		t.Fatalf("textSeen = %#v, want int-valued content key marked", s.textSeen)
	}
}

func TestStreamNextReturnsReadErrors(t *testing.T) {
	s := &stream{
		body:   io.NopCloser(strings.NewReader("")),
		reader: bufio.NewReader(errReader{}),
	}

	_, err := s.Next()
	if err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("Next() error = %v, want read failure", err)
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

func TestClientStreamsLongSSEDataLines(t *testing.T) {
	longDelta := strings.Repeat("x", 1024*1024+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		payload, err := json.Marshal(map[string]any{
			"type":  "response.output_text.delta",
			"delta": longDelta,
		})
		if err != nil {
			t.Fatalf("Marshal payload error = %v", err)
		}
		_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
	}))
	defer server.Close()

	stream, err := NewClient(server.URL).Stream(context.Background(), llm.Request{
		Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}
	if event.Type != llm.EventTextDelta || len(event.Delta) != len(longDelta) {
		t.Fatalf("event = %q delta length %d, want text delta length %d", event.Type, len(event.Delta), len(longDelta))
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

func requireSlice(t *testing.T, value any, name string) []any {
	t.Helper()

	typed, ok := value.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want []any", name, value)
	}
	return typed
}

func requireMap(t *testing.T, value any, name string) map[string]any {
	t.Helper()

	typed, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want map[string]any", name, value)
	}
	return typed
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}
