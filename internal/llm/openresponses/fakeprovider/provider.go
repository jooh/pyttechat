package fakeprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"example.com/llm-chat-web/internal/observability"
)

const (
	defaultModel = "dummy-responses"
	chunkRunes   = 8
)

type Options struct {
	StreamDelay time.Duration
}

type provider struct {
	opts Options
}

// NewHandler returns a deterministic OpenResponses-compatible fake provider.
func NewHandler() http.Handler {
	return NewHandlerWithOptions(Options{})
}

func NewHandlerWithOptions(opts Options) http.Handler {
	return provider{opts: opts}
}

func (p provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, span := observability.StartSpan(r.Context(), "fake_provider.responses.create", observability.ComponentFakeProvider)
	defer span.End()
	r = r.WithContext(ctx)

	if r.URL.Path != "/v1/responses" {
		writeJSONError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	req, err := decodeRequest(r)
	if err != nil {
		observability.RecordSpanError(span, err)
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "invalid JSON")
		return
	}

	resp := buildResponse(req)
	if !req.Stream {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if err := writeStreamingResponseWithOptions(w, r, resp, p.opts); err != nil {
		if !errors.Is(err, r.Context().Err()) {
			observability.RecordSpanError(span, err)
		}
		return
	}
}

type requestBody struct {
	Model  string          `json:"model"`
	Input  json.RawMessage `json:"input"`
	Stream bool            `json:"stream"`
}

func decodeRequest(r *http.Request) (requestBody, error) {
	defer r.Body.Close()

	var req requestBody
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		return requestBody{}, err
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return requestBody{}, errors.New("unexpected trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return requestBody{}, err
	}
	return req, nil
}

type responseObject struct {
	ID         string       `json:"id"`
	Object     string       `json:"object"`
	CreatedAt  int64        `json:"created_at"`
	Status     string       `json:"status"`
	Model      string       `json:"model"`
	Output     []outputItem `json:"output"`
	OutputText string       `json:"output_text"`
	Usage      usage        `json:"usage"`
}

type outputItem struct {
	ID               string        `json:"id"`
	Type             string        `json:"type"`
	Status           string        `json:"status"`
	Role             string        `json:"role,omitempty"`
	Content          []contentPart `json:"content,omitempty"`
	Summary          []summaryPart `json:"summary,omitempty"`
	EncryptedContent string        `json:"encrypted_content,omitempty"`
}

type contentPart struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

type summaryPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func buildResponse(req requestBody) responseObject {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = defaultModel
	}
	inputText := normalizedInputText(req.Input)
	outputText := "Echo: " + inputText
	fingerprint := model + "\n" + inputText + "\n" + outputText
	item := outputItem{
		ID:     deterministicID("msg", fingerprint),
		Type:   "message",
		Status: "completed",
		Role:   "assistant",
		Content: []contentPart{
			{
				Type:        "output_text",
				Text:        outputText,
				Annotations: []any{},
			},
		},
	}
	inputTokens := tokenCount(inputText)
	outputTokens := tokenCount(outputText)

	return responseObject{
		ID:         deterministicID("resp", fingerprint),
		Object:     "response",
		CreatedAt:  0,
		Status:     "completed",
		Model:      model,
		Output:     []outputItem{item},
		OutputText: outputText,
		Usage: usage{
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  inputTokens + outputTokens,
		},
	}
}

func normalizedInputText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return normalizeText(latestText(value))
}

func latestText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var last string
		for _, item := range typed {
			if text := latestText(item); text != "" {
				last = text
			}
		}
		return last
	case map[string]any:
		if text := textFromContent(typed["content"]); text != "" {
			return text
		}
		if text, _ := typed["text"].(string); text != "" {
			return text
		}
		if text, _ := typed["input_text"].(string); text != "" {
			return text
		}
	}
	return ""
}

func textFromContent(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var out strings.Builder
		for _, item := range typed {
			switch part := item.(type) {
			case string:
				out.WriteString(part)
			case map[string]any:
				if text, _ := part["text"].(string); text != "" {
					out.WriteString(text)
				} else if text, _ := part["input_text"].(string); text != "" {
					out.WriteString(text)
				}
			}
		}
		return out.String()
	}
	return ""
}

func normalizeText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func deterministicID(prefix, seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return prefix + "_" + hex.EncodeToString(sum[:])[:16]
}

func tokenCount(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

type streamEvent struct {
	Type string
	Data map[string]any
}

func writeStreamingResponse(w http.ResponseWriter, r *http.Request, resp responseObject) error {
	return writeStreamingResponseWithOptions(w, r, resp, Options{})
}

func writeStreamingResponseWithOptions(w http.ResponseWriter, r *http.Request, resp responseObject, opts Options) error {
	ctx, span := observability.StartSpan(r.Context(), "fake_provider.stream.write", observability.ComponentFakeProvider)
	defer span.End()
	r = r.WithContext(ctx)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming unsupported")
		return nil
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	for i, event := range buildStreamEvents(resp) {
		if err := r.Context().Err(); err != nil {
			observability.RecordSpanError(span, err)
			return err
		}
		if i > 0 {
			if err := waitStreamDelay(r.Context(), opts.StreamDelay); err != nil {
				observability.RecordSpanError(span, err)
				return err
			}
		}
		if err := writeSSEEvent(w, flusher, event.Type, event.Data); err != nil {
			observability.RecordSpanError(span, err)
			_ = writeSSEEvent(w, flusher, "error", map[string]any{
				"type":            "error",
				"sequence_number": nextSequence(event.Data),
				"code":            "write_failed",
				"message":         "stream write failed",
			})
			_ = writeSSEDone(w, flusher)
			return err
		}
		observability.LLMStreamEvent(ctx, observability.ComponentFakeProvider, event.Type)
	}
	if err := r.Context().Err(); err != nil {
		observability.RecordSpanError(span, err)
		return err
	}
	if err := writeSSEDone(w, flusher); err != nil {
		observability.RecordSpanError(span, err)
		return err
	}
	observability.LLMStreamEvent(ctx, observability.ComponentFakeProvider, "done")
	return nil
}

func waitStreamDelay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func nextSequence(data map[string]any) int {
	switch value := data["sequence_number"].(type) {
	case int:
		return value + 1
	case float64:
		return int(value) + 1
	default:
		return 0
	}
}

func buildStreamEvents(resp responseObject) []streamEvent {
	events := make([]streamEvent, 0, 9+len(chunkText(resp.OutputText)))
	sequence := 0
	next := func(eventType string, data map[string]any) {
		data["type"] = eventType
		data["sequence_number"] = sequence
		sequence++
		events = append(events, streamEvent{Type: eventType, Data: data})
	}

	inProgressResponse := resp
	inProgressResponse.Status = "in_progress"
	inProgressResponse.Output = []outputItem{}
	inProgressResponse.OutputText = ""
	next("response.created", map[string]any{"response": inProgressResponse})
	next("response.in_progress", map[string]any{"response": inProgressResponse})

	reasoningText := fakeReasoningText(resp.OutputText)
	reasoning := outputItem{
		ID:     deterministicID("rs", resp.ID+"\n"+reasoningText),
		Type:   "reasoning",
		Status: "completed",
		Summary: []summaryPart{
			{Type: "summary_text", Text: reasoningText},
		},
		EncryptedContent: deterministicID("encrypted", resp.ID),
	}
	inProgressReasoning := reasoning
	inProgressReasoning.Status = "in_progress"
	inProgressReasoning.Summary = nil
	inProgressReasoning.EncryptedContent = ""
	next("response.output_item.added", map[string]any{
		"output_index": 0,
		"item":         inProgressReasoning,
	})
	for _, delta := range chunkText(reasoningText) {
		next("response.reasoning.delta", map[string]any{
			"item_id":      reasoning.ID,
			"output_index": 0,
			"delta":        delta,
		})
	}
	next("response.output_item.done", map[string]any{
		"output_index": 0,
		"item":         reasoning,
	})

	item := resp.Output[0]
	inProgressItem := item
	inProgressItem.Status = "in_progress"
	inProgressItem.Content = []contentPart{}
	next("response.output_item.added", map[string]any{
		"output_index": 1,
		"item":         inProgressItem,
	})

	part := item.Content[0]
	inProgressPart := part
	inProgressPart.Text = ""
	next("response.content_part.added", map[string]any{
		"item_id":       item.ID,
		"output_index":  1,
		"content_index": 0,
		"part":          inProgressPart,
	})

	for _, delta := range chunkText(resp.OutputText) {
		next("response.output_text.delta", map[string]any{
			"item_id":       item.ID,
			"output_index":  1,
			"content_index": 0,
			"delta":         delta,
		})
	}
	next("response.output_text.done", map[string]any{
		"item_id":       item.ID,
		"output_index":  1,
		"content_index": 0,
		"text":          resp.OutputText,
	})
	next("response.content_part.done", map[string]any{
		"item_id":       item.ID,
		"output_index":  1,
		"content_index": 0,
		"part":          part,
	})
	next("response.output_item.done", map[string]any{
		"output_index": 1,
		"item":         item,
	})
	completedResponse := resp
	completedResponse.Output = []outputItem{reasoning, item}
	next("response.completed", map[string]any{
		"response": completedResponse,
	})

	return events
}

func fakeReasoningText(outputText string) string {
	if strings.TrimSpace(outputText) == "" {
		return "Preparing a concise fake response."
	}
	return "Preparing a concise fake response before streaming the answer."
}

func chunkText(text string) []string {
	runes := []rune(text)
	if len(runes) == 0 {
		return []string{""}
	}
	chunks := make([]string, 0, (len(runes)+chunkRunes-1)/chunkRunes)
	for start := 0; start < len(runes); start += chunkRunes {
		end := start + chunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", eventType); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeSSEDone(w http.ResponseWriter, flusher http.Flusher) error {
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
