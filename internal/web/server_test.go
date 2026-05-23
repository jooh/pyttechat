package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/llm-chat-web/internal/llm"
	"example.com/llm-chat-web/internal/llm/dummy"
)

func TestRootRendersChatPageAndSetsSessionCookie(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{Client: dummy.NewClient()}))
	defer server.Close()

	client := testHTTPClient(t)
	response, body := get(t, client, server.URL+"/")
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if !strings.Contains(body, `<meta name="csrf-token"`) {
		t.Fatalf("GET / body does not contain CSRF meta tag: %q", body)
	}
	if !strings.Contains(body, "Pyttechat") {
		t.Fatalf("GET / body = %q, want app shell", body)
	}

	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName {
		t.Fatalf("cookie name = %q, want %q", cookie.Name, sessionCookieName)
	}
	if !cookie.HttpOnly {
		t.Fatalf("session cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie SameSite = %v, want Lax", cookie.SameSite)
	}
}

func TestCreateTurnRequiresCSRF(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{Client: dummy.NewClient()}))
	defer server.Close()

	client := testHTTPClient(t)
	response, _ := get(t, client, server.URL+"/")
	response.Body.Close()

	request := newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns", map[string]string{
		"prompt": "hello",
	})
	response, body := do(t, client, request)
	defer response.Body.Close()

	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /chat/turns status = %d, want 403; body = %q", response.StatusCode, body)
	}
}

func TestCreateTurnRejectsEmptyPrompt(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{Client: dummy.NewClient()}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)

	request := newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns", map[string]string{
		"prompt": "  ",
	})
	request.Header.Set(csrfHeaderName, csrfToken)
	response, body := do(t, client, request)
	defer response.Body.Close()

	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /chat/turns status = %d, want 400; body = %q", response.StatusCode, body)
	}
}

func TestCreateTurnStartsJobAndStreamsReplayableEvents(t *testing.T) {
	llmClient := dummy.NewClient(dummy.Turn{
		ReasoningChunks: []string{"think"},
		TextChunks:      []string{"hel", "lo"},
	})
	server := httptest.NewServer(NewServer(Options{
		Client:          llmClient,
		Model:           "test-model",
		ReasoningEffort: "medium",
	}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")

	response, body := get(t, client, server.URL+turn.StreamURL)
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	frames := parseSSE(t, body)
	if !hasFrame(frames, "reasoning", `"delta":"think"`) {
		t.Fatalf("SSE frames = %#v, want reasoning frame", frames)
	}
	if !hasFrame(frames, "text", `"delta":"hel"`) || !hasFrame(frames, "text", `"delta":"lo"`) {
		t.Fatalf("SSE frames = %#v, want streamed text chunks", frames)
	}
	if !hasFrame(frames, "done", `"response_id":"dummy-response-1"`) {
		t.Fatalf("SSE frames = %#v, want done frame", frames)
	}

	requests := llmClient.Requests()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Model != "test-model" {
		t.Fatalf("request model = %q, want test-model", requests[0].Model)
	}
	if requests[0].Reasoning.Effort != "medium" || requests[0].Reasoning.Summary != "auto" {
		t.Fatalf("request reasoning = %#v, want medium effort with auto summary", requests[0].Reasoning)
	}
}

func TestSubscriberDisconnectDoesNotCancelTurnJob(t *testing.T) {
	llmClient := newControlledClient()
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")
	ctx := llmClient.waitForContext(t)

	response, err := client.Get(server.URL + turn.StreamURL)
	if err != nil {
		t.Fatalf("GET events error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, raw)
	}

	llmClient.events <- llm.Event{Type: llm.EventTextDelta, Delta: "partial"}
	frame := readSSEFrame(t, bufio.NewReader(response.Body))
	if frame.Event != "text" {
		t.Fatalf("first frame = %#v, want text", frame)
	}
	if frame.ID == "" {
		t.Fatalf("first frame = %#v, want event id", frame)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("closing subscriber body: %v", err)
	}

	select {
	case <-ctx.Done():
		t.Fatalf("LLM context was canceled when subscriber disconnected: %v", ctx.Err())
	case <-time.After(50 * time.Millisecond):
	}

	llmClient.events <- llm.Event{Type: llm.EventCompleted, ResponseID: "resp_done"}
	close(llmClient.events)

	request, err := http.NewRequest(http.MethodGet, server.URL+turn.StreamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest replay error = %v", err)
	}
	request.Header.Set("Last-Event-ID", frame.ID)
	response, body := do(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body = %q", response.StatusCode, body)
	}
	frames := parseSSE(t, body)
	if hasFrame(frames, "text", `"delta":"partial"`) {
		t.Fatalf("replay body = %q, did not expect already acknowledged text event", body)
	}
	if !hasFrame(frames, "done", `"response_id":"resp_done"`) {
		t.Fatalf("replay body = %q, want done event after disconnected subscriber", body)
	}
}

func TestTurnJobDisconnectsSlowSubscriberWithoutLosingReplay(t *testing.T) {
	turn, err := newTurnJob("hello")
	if err != nil {
		t.Fatalf("newTurnJob error = %v", err)
	}
	_, updates, terminal := turn.subscribe(0)
	if terminal {
		t.Fatalf("new turn is terminal")
	}

	for i := 0; i < 130; i++ {
		turn.emit("text", deltaEvent{
			TurnID:             turn.id,
			AssistantMessageID: turn.assistantMessageID,
			Delta:              itoa(i),
		})
	}

	turn.mu.Lock()
	subscriberCount := len(turn.subscribers)
	turn.mu.Unlock()
	if subscriberCount != 0 {
		t.Fatalf("subscriber count = %d, want slow subscriber disconnected", subscriberCount)
	}
	turn.unsubscribe(updates)

	replay, replayUpdates, replayTerminal := turn.subscribe(0)
	if replayUpdates == nil {
		t.Fatalf("replay subscriber channel is nil")
	}
	defer turn.unsubscribe(replayUpdates)
	if replayTerminal {
		t.Fatalf("turn unexpectedly terminal")
	}
	if len(replay) != 130 {
		t.Fatalf("replay event count = %d, want all emitted events", len(replay))
	}
	if string(replay[129].Data) == "" || replay[129].ID == 0 {
		t.Fatalf("last replay event = %#v, want encoded event with id", replay[129])
	}
}

func TestAbortCancelsTurnJobAndStreamsAbortedEvent(t *testing.T) {
	llmClient := newControlledClient()
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")
	ctx := llmClient.waitForContext(t)

	response, err := client.Get(server.URL + turn.StreamURL)
	if err != nil {
		t.Fatalf("GET events error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, raw)
	}

	request := newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+turn.TurnID+"/abort", nil)
	request.Header.Set(csrfHeaderName, csrfToken)
	abortResponse, abortBody := do(t, client, request)
	defer abortResponse.Body.Close()
	if abortResponse.StatusCode != http.StatusOK {
		t.Fatalf("abort status = %d, want 200; body = %q", abortResponse.StatusCode, abortBody)
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatalf("abort did not cancel LLM context")
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll events error = %v", err)
	}
	if !hasFrame(parseSSE(t, string(body)), "aborted", `"turn_id":"`+turn.TurnID+`"`) {
		t.Fatalf("SSE body = %q, want aborted event", body)
	}
}

func TestCompletedTurnsUseSameChatSessionForFollowUp(t *testing.T) {
	llmClient := &recordingClient{}
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)

	first := createTurn(t, client, server.URL, csrfToken, "first")
	response, body := get(t, client, server.URL+first.StreamURL)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("first events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	second := createTurn(t, client, server.URL, csrfToken, "second")
	response, body = get(t, client, server.URL+second.StreamURL)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("second events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	requests := llmClient.Requests()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if len(requests[1].Messages) != 3 {
		t.Fatalf("second request messages = %#v, want prior user, assistant, next user", requests[1].Messages)
	}
	if requests[1].Messages[0].Text() != "first" || requests[1].Messages[1].Text() != "answer 1" || requests[1].Messages[2].Text() != "second" {
		t.Fatalf("second request messages = %#v, want first conversation context", requests[1].Messages)
	}
}

type turnResponse struct {
	TurnID             string `json:"turn_id"`
	UserMessageID      string `json:"user_message_id"`
	AssistantMessageID string `json:"assistant_message_id"`
	StreamURL          string `json:"stream_url"`
}

func testHTTPClient(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New error = %v", err)
	}
	return &http.Client{Jar: jar}
}

func fetchCSRFToken(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()

	response, body := get(t, client, baseURL+"/")
	response.Body.Close()
	token := csrfFromHTML(t, body)
	if token == "" {
		t.Fatalf("CSRF token is empty in body %q", body)
	}
	return token
}

func createTurn(t *testing.T, client *http.Client, baseURL, csrfToken, prompt string) turnResponse {
	t.Helper()

	request := newJSONRequest(t, http.MethodPost, baseURL+"/chat/turns", map[string]string{
		"prompt": prompt,
	})
	request.Header.Set(csrfHeaderName, csrfToken)
	response, body := do(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /chat/turns status = %d, want 201; body = %q", response.StatusCode, body)
	}

	var payload turnResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode turn response error = %v; body = %q", err, body)
	}
	if payload.TurnID == "" || payload.UserMessageID == "" || payload.AssistantMessageID == "" || payload.StreamURL == "" {
		t.Fatalf("turn response = %#v, want stable ids and stream URL", payload)
	}
	return payload
}

func newJSONRequest(t *testing.T, method, url string, payload any) *http.Request {
	t.Helper()

	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatalf("Encode request body error = %v", err)
		}
	}
	request, err := http.NewRequest(method, url, &body)
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	return request
}

func get(t *testing.T, client *http.Client, url string) (*http.Response, string) {
	t.Helper()

	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll GET body error = %v", err)
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, string(body)
}

func do(t *testing.T, client *http.Client, request *http.Request) (*http.Response, string) {
	t.Helper()

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s error = %v", request.Method, request.URL, err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll response body error = %v", err)
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, string(body)
}

func csrfFromHTML(t *testing.T, body string) string {
	t.Helper()

	const prefix = `<meta name="csrf-token" content="`
	start := strings.Index(body, prefix)
	if start < 0 {
		return ""
	}
	start += len(prefix)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		return ""
	}
	return body[start : start+end]
}

type sseFrame struct {
	ID    string
	Event string
	Data  string
}

func parseSSE(t *testing.T, body string) []sseFrame {
	t.Helper()

	rawFrames := strings.Split(strings.TrimSpace(body), "\n\n")
	frames := make([]sseFrame, 0, len(rawFrames))
	for _, raw := range rawFrames {
		if raw == "" {
			continue
		}
		var frame sseFrame
		for _, line := range strings.Split(raw, "\n") {
			switch {
			case strings.HasPrefix(line, "id: "):
				frame.ID = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				frame.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				frame.Data += strings.TrimPrefix(line, "data: ")
			}
		}
		frames = append(frames, frame)
	}
	return frames
}

func hasFrame(frames []sseFrame, eventName, dataSubstring string) bool {
	for _, frame := range frames {
		if frame.Event == eventName && strings.Contains(frame.Data, dataSubstring) {
			return true
		}
	}
	return false
}

func readSSEFrame(t *testing.T, reader *bufio.Reader) sseFrame {
	t.Helper()

	var raw strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("ReadString frame error = %v", err)
		}
		if line == "\n" || line == "\r\n" {
			break
		}
		raw.WriteString(line)
	}
	frames := parseSSE(t, strings.TrimSpace(raw.String()))
	if len(frames) != 1 {
		t.Fatalf("parsed frames = %#v, want one frame", frames)
	}
	return frames[0]
}

type controlledClient struct {
	events chan llm.Event
	ctx    chan context.Context
}

func newControlledClient() *controlledClient {
	return &controlledClient{
		events: make(chan llm.Event),
		ctx:    make(chan context.Context, 1),
	}
}

func (c *controlledClient) Stream(ctx context.Context, _ llm.Request) (llm.Stream, error) {
	c.ctx <- ctx
	return &controlledStream{ctx: ctx, events: c.events}, nil
}

func (c *controlledClient) waitForContext(t *testing.T) context.Context {
	t.Helper()

	select {
	case ctx := <-c.ctx:
		return ctx
	case <-time.After(time.Second):
		t.Fatalf("LLM stream was not started")
		return nil
	}
}

type controlledStream struct {
	ctx    context.Context
	events <-chan llm.Event
}

func (s *controlledStream) Next() (llm.Event, error) {
	select {
	case <-s.ctx.Done():
		return llm.Event{}, s.ctx.Err()
	case event, ok := <-s.events:
		if !ok {
			return llm.Event{}, io.EOF
		}
		return event, nil
	}
}

func (*controlledStream) Close() error {
	return nil
}

type recordingClient struct {
	mu       sync.Mutex
	requests []llm.Request
}

func (c *recordingClient) Stream(_ context.Context, request llm.Request) (llm.Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.requests = append(c.requests, request.Clone())
	answer := "answer " + itoa(len(c.requests))
	return &recordingStream{
		events: []llm.Event{
			{Type: llm.EventTextDelta, Delta: answer},
			{Type: llm.EventCompleted, ResponseID: "resp_" + itoa(len(c.requests))},
		},
	}, nil
}

func (c *recordingClient) Requests() []llm.Request {
	c.mu.Lock()
	defer c.mu.Unlock()

	requests := make([]llm.Request, len(c.requests))
	for i, request := range c.requests {
		requests[i] = request.Clone()
	}
	return requests
}

type recordingStream struct {
	events []llm.Event
	index  int
}

func (s *recordingStream) Next() (llm.Event, error) {
	if s.index >= len(s.events) {
		return llm.Event{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (*recordingStream) Close() error {
	return nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}
