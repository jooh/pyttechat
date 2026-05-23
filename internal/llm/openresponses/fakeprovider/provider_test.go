package fakeprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNonStreamingResponseReturnsJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"dummy-responses","input":"hello world"}`))
	request.Header.Set("Content-Type", "application/json")

	NewHandler().ServeHTTP(recorder, request)

	response := recorder.Result()
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode response error = %v", err)
	}
	if payload["object"] != "response" || payload["status"] != "completed" {
		t.Fatalf("response = %#v, want completed response object", payload)
	}
	if payload["id"] == "" || payload["created_at"] != float64(0) || payload["model"] != "dummy-responses" {
		t.Fatalf("response metadata = %#v, want deterministic id, created_at, and model", payload)
	}
	if payload["output_text"] != "Echo: hello world" {
		t.Fatalf("output_text = %v, want echo text", payload["output_text"])
	}
	output := payload["output"].([]any)
	if got := outputItemText(output[0].(map[string]any)); got != "Echo: hello world" {
		t.Fatalf("output item text = %q, want echo text", got)
	}
	usage := payload["usage"].(map[string]any)
	if usage["total_tokens"] == float64(0) {
		t.Fatalf("usage = %#v, want deterministic non-zero usage", usage)
	}
}

func TestStreamingResponseReturnsFullResponsesLifecycle(t *testing.T) {
	body := streamResponse(t, `{"model":"dummy-responses","input":"hello world","stream":true}`)
	frames := parseSSE(t, body)

	if len(frames) < 10 {
		t.Fatalf("frame count = %d, want full lifecycle: %#v", len(frames), frames)
	}

	wantPrefix := []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
	}
	for i, want := range wantPrefix {
		if frames[i].Event != want {
			t.Fatalf("frame[%d] event = %q, want %q", i, frames[i].Event, want)
		}
	}

	deltaStart := len(wantPrefix)
	deltaEnd := deltaStart
	for deltaEnd < len(frames) && frames[deltaEnd].Event == "response.output_text.delta" {
		deltaEnd++
	}
	if deltaEnd == deltaStart {
		t.Fatalf("stream has no output_text.delta frames: %#v", frames)
	}

	wantSuffix := []string{
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
		"",
	}
	if got := len(frames) - deltaEnd; got != len(wantSuffix) {
		t.Fatalf("suffix frame count = %d, want %d: %#v", got, len(wantSuffix), frames[deltaEnd:])
	}
	for i, want := range wantSuffix {
		frame := frames[deltaEnd+i]
		if frame.Event != want {
			t.Fatalf("suffix frame[%d] event = %q, want %q", i, frame.Event, want)
		}
		if want == "" && frame.Data != "[DONE]" {
			t.Fatalf("terminal data = %q, want [DONE]", frame.Data)
		}
	}
}

func TestStreamingResponseHasSSEHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello","stream":true}`))

	NewHandler().ServeHTTP(recorder, request)

	response := recorder.Result()
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	if got := response.Header.Get("Connection"); got != "keep-alive" {
		t.Fatalf("Connection = %q, want keep-alive", got)
	}
	if got := response.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}
}

func TestStreamingEventsHaveMatchingTypesAndIncreasingSequenceNumbers(t *testing.T) {
	frames := parseSSE(t, streamResponse(t, `{"input":"hello world","stream":true}`))

	previous := -1
	for _, frame := range frames {
		if frame.Data == "[DONE]" {
			continue
		}
		if frame.Event == "" {
			t.Fatalf("frame missing event line: %#v", frame)
		}
		if got := frame.Payload["type"]; got != frame.Event {
			t.Fatalf("payload type = %v, want event %q", got, frame.Event)
		}

		number, ok := frame.Payload["sequence_number"].(float64)
		if !ok {
			t.Fatalf("sequence_number = %#v, want number", frame.Payload["sequence_number"])
		}
		if int(number) <= previous {
			t.Fatalf("sequence_number = %d after %d, want strictly increasing", int(number), previous)
		}
		previous = int(number)
	}
}

func TestStreamingTextDoneFieldsMatchCompletedOutputText(t *testing.T) {
	frames := parseSSE(t, streamResponse(t, `{"input":"hello 🌍","stream":true}`))

	var deltaText string
	var textDone string
	var partDoneText string
	var itemDoneText string
	var completedText string
	for _, frame := range frames {
		switch frame.Event {
		case "response.output_text.delta":
			deltaText += stringField(frame.Payload, "delta")
		case "response.output_text.done":
			textDone = stringField(frame.Payload, "text")
		case "response.content_part.done":
			part, _ := frame.Payload["part"].(map[string]any)
			partDoneText = stringField(part, "text")
		case "response.output_item.done":
			item, _ := frame.Payload["item"].(map[string]any)
			itemDoneText = outputItemText(item)
		case "response.completed":
			response, _ := frame.Payload["response"].(map[string]any)
			completedText = stringField(response, "output_text")
		}
	}

	for name, got := range map[string]string{
		"delta text":        deltaText,
		"output_text.done":  textDone,
		"content_part.done": partDoneText,
		"output_item.done":  itemDoneText,
	} {
		if got != completedText {
			t.Fatalf("%s = %q, want completed output_text %q", name, got, completedText)
		}
	}
	if completedText != "Echo: hello 🌍" {
		t.Fatalf("completed output_text = %q, want deterministic echo", completedText)
	}
}

func TestStreamingResponseIsStableAcrossIdenticalRequests(t *testing.T) {
	body := `{"model":"dummy-responses","input":"hello world","stream":true}`

	first := streamResponse(t, body)
	second := streamResponse(t, body)

	if !bytes.Equal(first, second) {
		t.Fatalf("stream responses differ:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestInvalidJSONReturnsBadRequestJSONNotSSE(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true`))

	NewHandler().ServeHTTP(recorder, request)

	response := recorder.Result()
	defer response.Body.Close()

	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := response.Header.Get("Content-Type"); strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want non-SSE error response", got)
	}
}

func TestStreamingStopsCleanlyOnRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	writer := &cancelingResponseWriter{
		header: http.Header{},
		cancel: cancel,
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hello world","stream":true}`)).WithContext(ctx)

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("ServeHTTP panicked on canceled request: %v", recovered)
		}
	}()
	NewHandler().ServeHTTP(writer, request)

	if writer.flushes == 0 {
		t.Fatalf("flush count = 0, want at least one event before cancellation")
	}
	if strings.Contains(writer.body.String(), "[DONE]") {
		t.Fatalf("body contains terminal marker after cancellation:\n%s", writer.body.String())
	}
}

func streamResponse(t *testing.T, requestBody string) []byte {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")

	NewHandler().ServeHTTP(recorder, request)

	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d, want 200: %s", response.StatusCode, body)
	}
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll response body error = %v", err)
	}
	return body
}

type sseFrame struct {
	Event   string
	Data    string
	Payload map[string]any
}

func parseSSE(t *testing.T, body []byte) []sseFrame {
	t.Helper()

	rawFrames := strings.Split(strings.TrimSuffix(string(body), "\n\n"), "\n\n")
	frames := make([]sseFrame, 0, len(rawFrames))
	for _, raw := range rawFrames {
		if raw == "" {
			continue
		}
		var frame sseFrame
		for _, line := range strings.Split(raw, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				frame.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if frame.Data != "" {
					frame.Data += "\n"
				}
				frame.Data += strings.TrimPrefix(line, "data: ")
			default:
				t.Fatalf("unexpected SSE line %q in frame %q", line, raw)
			}
		}
		if frame.Data != "[DONE]" {
			if err := json.Unmarshal([]byte(frame.Data), &frame.Payload); err != nil {
				t.Fatalf("Unmarshal frame data %q error = %v", frame.Data, err)
			}
		}
		frames = append(frames, frame)
	}
	return frames
}

func stringField(payload map[string]any, name string) string {
	value, _ := payload[name].(string)
	return value
}

func outputItemText(item map[string]any) string {
	content, _ := item["content"].([]any)
	var text string
	for _, value := range content {
		part, _ := value.(map[string]any)
		if part["type"] == "output_text" {
			text += stringField(part, "text")
		}
	}
	return text
}

type cancelingResponseWriter struct {
	header  http.Header
	body    strings.Builder
	cancel  context.CancelFunc
	flushes int
}

func (w *cancelingResponseWriter) Header() http.Header {
	return w.header
}

func (w *cancelingResponseWriter) WriteHeader(int) {
}

func (w *cancelingResponseWriter) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

func (w *cancelingResponseWriter) Flush() {
	w.flushes++
	w.cancel()
}
