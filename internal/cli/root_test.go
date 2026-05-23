package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/llm-chat-web/internal/llm/openresponses/fakeprovider"
)

func runCommand(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Execute(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)

	return code, stdout.String(), stderr.String()
}

func TestAskCommandStreamsAnswerToStdoutAndReasoningToStderr(t *testing.T) {
	code, stdout, stderr := runCommand(t, "", "ask", "hello")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	wantStdout := "This is a dummy LLM response.\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}

	if !strings.Contains(stderr, "Thinking") {
		t.Fatalf("stderr = %q, want streamed reasoning", stderr)
	}
}

func TestChatCommandKeepsOneEphemeralSession(t *testing.T) {
	code, stdout, stderr := runCommand(t, "first\nsecond\n", "chat")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	if strings.Count(stdout, "This is a dummy LLM response.") != 2 {
		t.Fatalf("stdout = %q, want two streamed answers", stdout)
	}

	if strings.Count(stderr, "Thinking") != 2 {
		t.Fatalf("stderr = %q, want two streamed reasoning blocks", stderr)
	}
}

func TestChatCommandSendsPriorTurnToProxy(t *testing.T) {
	var requestBodies []map[string]any
	handler := fakeprovider.NewHandler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer proxy-token" {
			t.Fatalf("Authorization header = %q, want proxy bearer token", got)
		}

		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll request body error = %v", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(rawBody))

		var body map[string]any
		if err := json.Unmarshal(rawBody, &body); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		requestBodies = append(requestBodies, body)

		handler.ServeHTTP(w, r)
	}))
	defer server.Close()

	t.Setenv("PYTTECHAT_LLM_PROXY_URL", server.URL)
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "proxy-token")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"chat"}, strings.NewReader("first\nsecond\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); got != "Echo: first\nEcho: second\n" {
		t.Fatalf("stdout = %q, want both proxy answers", got)
	}
	if len(requestBodies) != 2 {
		t.Fatalf("request count = %d, want 2", len(requestBodies))
	}

	input := requestBodies[1]["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("second request input count = %d, want prior user, assistant, next user: %#v", len(input), input)
	}
	firstUser := input[0].(map[string]any)
	priorAssistant := input[1].(map[string]any)
	secondUser := input[2].(map[string]any)
	if firstUser["role"] != "user" || firstUser["content"] != "first" {
		t.Fatalf("first input = %#v, want first user turn", firstUser)
	}
	if priorAssistant["role"] != "assistant" || priorAssistant["content"] != "Echo: first" {
		t.Fatalf("prior assistant input = %#v, want first assistant answer", priorAssistant)
	}
	if secondUser["role"] != "user" || secondUser["content"] != "second" {
		t.Fatalf("second input = %#v, want second user turn", secondUser)
	}
}

func TestChatCommandRejectsArgs(t *testing.T) {
	code, _, stderr := runCommand(t, "", "chat", "unexpected")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `unknown command "unexpected"`) && !strings.Contains(stderr, "accepts 0 arg(s)") {
		t.Fatalf("stderr = %q, want argument error", stderr)
	}
}

func TestAskCommandRequiresPrompt(t *testing.T) {
	code, _, stderr := runCommand(t, "", "ask")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}

	if !strings.Contains(stderr, "prompt is required") {
		t.Fatalf("stderr = %q, want missing prompt error", stderr)
	}

	if !strings.Contains(stderr, "Usage:") {
		t.Fatalf("stderr = %q, want usage", stderr)
	}
}

func TestAskCommandRejectsBlankPrompt(t *testing.T) {
	code, _, stderr := runCommand(t, "", "ask", "  ")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}

	if !strings.Contains(stderr, "prompt must not be empty") {
		t.Fatalf("stderr = %q, want empty prompt error", stderr)
	}
}

func TestVersionCommandPrintsBuildInfo(t *testing.T) {
	code, stdout, stderr := runCommand(t, "", "version")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	want := "version=dev commit=unknown date=unknown\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestServeCommandHelpShowsWebOptions(t *testing.T) {
	code, stdout, stderr := runCommand(t, "", "serve", "--help")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "Start the web chat server") {
		t.Fatalf("stdout = %q, want serve command help", stdout)
	}
	if !strings.Contains(stdout, "--addr") {
		t.Fatalf("stdout = %q, want listen address option", stdout)
	}
}

func TestServeCommandStartsWebHandler(t *testing.T) {
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout bytes.Buffer
	stderr := &safeBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- Execute(ctx, []string{"serve", "--addr", "127.0.0.1:0", "--secure-cookies"}, strings.NewReader(""), &stdout, stderr)
	}()

	addr := waitForListenAddr(t, stderr)
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET / error = %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("GET / status = %d, want 200; body = %q", response.StatusCode, raw)
	}
	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie count = %d, want 1", len(cookies))
	}
	if !cookies[0].Secure {
		t.Fatalf("session cookie Secure = false, want true when --secure-cookies is set")
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve exit code = %d, want 0; stderr = %q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatalf("serve command did not stop after context cancellation")
	}
}

func TestServeCommandSubmitsChatThroughServedWebHandler(t *testing.T) {
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "proxy-token")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")

	var proxyMu sync.Mutex
	var proxyAuth []string
	var proxyBodies []map[string]any
	fakeProxy := fakeprovider.NewHandler()
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll proxy request body error = %v", err)
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(rawBody))

		var body map[string]any
		if err := json.Unmarshal(rawBody, &body); err != nil {
			t.Errorf("Decode proxy request body error = %v", err)
			http.Error(w, "decode error", http.StatusBadRequest)
			return
		}

		proxyMu.Lock()
		proxyAuth = append(proxyAuth, r.Header.Get("Authorization"))
		proxyBodies = append(proxyBodies, body)
		proxyMu.Unlock()

		fakeProxy.ServeHTTP(w, r)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	var stdout bytes.Buffer
	stderr := &safeBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- Execute(ctx, []string{
			"--proxy-url", proxyServer.URL,
			"--model", "served-model",
			"--reasoning-effort", "medium",
			"serve",
			"--addr", "127.0.0.1:0",
		}, strings.NewReader(""), &stdout, stderr)
	}()
	defer stopServeCommand(t, cancel, done, stderr)

	addr := waitForListenAddr(t, stderr)
	client := newCookieClient(t)
	baseURL := "http://" + addr
	csrfToken := fetchServedCSRFToken(t, client, baseURL)
	turn := createServedTurn(t, client, baseURL, csrfToken, "hello from browser")

	eventsResponse, eventsBody := doServedRequest(t, client, newServedRequest(t, http.MethodGet, baseURL+turn.StreamURL, nil))
	defer eventsResponse.Body.Close()
	if eventsResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET turn events status = %d, want 200; body = %q", eventsResponse.StatusCode, eventsBody)
	}
	if got := eventsResponse.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("GET turn events Content-Type = %q, want text/event-stream", got)
	}
	if got := textFromServedSSE(t, eventsBody); got != "Echo: hello from browser" {
		t.Fatalf("streamed text = %q, want fake proxy echo", got)
	}
	if len(servedSSEData(t, eventsBody, "done")) == 0 {
		t.Fatalf("SSE body = %q, want done event", eventsBody)
	}

	proxyMu.Lock()
	auth := append([]string(nil), proxyAuth...)
	bodies := append([]map[string]any(nil), proxyBodies...)
	proxyMu.Unlock()
	if len(auth) != 1 || len(bodies) != 1 {
		t.Fatalf("proxy request count = auth:%d bodies:%d, want 1 each", len(auth), len(bodies))
	}
	if auth[0] != "Bearer proxy-token" {
		t.Fatalf("Authorization header = %q, want proxy bearer token", auth[0])
	}

	body := bodies[0]
	if body["model"] != "served-model" {
		t.Fatalf("proxy request model = %v, want served-model", body["model"])
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("proxy request reasoning = %#v, want object", body["reasoning"])
	}
	if reasoning["summary"] != "auto" || reasoning["effort"] != "medium" {
		t.Fatalf("proxy request reasoning = %#v, want summary auto and effort medium", reasoning)
	}
	input := body["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("proxy request input = %#v, want one browser user message", input)
	}
	message := input[0].(map[string]any)
	if message["role"] != "user" || message["content"] != "hello from browser" {
		t.Fatalf("proxy request input message = %#v, want submitted browser prompt", message)
	}
}

func TestServeCommandReportsBindFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen error = %v", err)
	}
	defer listener.Close()

	code, _, stderr := runCommand(t, "", "serve", "--addr", listener.Addr().String())
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "listen") {
		t.Fatalf("stderr = %q, want listen failure", stderr)
	}
}

func TestNoArgsPrintsHelp(t *testing.T) {
	code, stdout, stderr := runCommand(t, "")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	if !strings.Contains(stdout, "Minimal LLM chat backend CLI") {
		t.Fatalf("stdout = %q, want help text", stdout)
	}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForListenAddr(t *testing.T, stderr *safeBuffer) string {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		output := stderr.String()
		const prefix = "pyttechat web listening on "
		if index := strings.LastIndex(output, prefix); index >= 0 {
			line := strings.TrimSpace(output[index+len(prefix):])
			if newline := strings.IndexByte(line, '\n'); newline >= 0 {
				line = line[:newline]
			}
			if line != "" {
				return line
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("serve command did not report a listen address; stderr = %q", stderr.String())
	return ""
}

type servedTurnResponse struct {
	TurnID             string `json:"turn_id"`
	UserMessageID      string `json:"user_message_id"`
	AssistantMessageID string `json:"assistant_message_id"`
	StreamURL          string `json:"stream_url"`
}

func stopServeCommand(t *testing.T, cancel context.CancelFunc, done <-chan int, stderr *safeBuffer) {
	t.Helper()

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve exit code = %d, want 0; stderr = %q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatalf("serve command did not stop after context cancellation")
	}
}

func newCookieClient(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New error = %v", err)
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 2 * time.Second,
	}
}

func fetchServedCSRFToken(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()

	response, body := doServedRequest(t, client, newServedRequest(t, http.MethodGet, baseURL+"/", nil))
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200; body = %q", response.StatusCode, body)
	}
	token := csrfFromServedHTML(body)
	if token == "" {
		t.Fatalf("CSRF token is empty in body %q", body)
	}
	return token
}

func createServedTurn(t *testing.T, client *http.Client, baseURL, csrfToken, prompt string) servedTurnResponse {
	t.Helper()

	request := newServedRequest(t, http.MethodPost, baseURL+"/chat/turns", map[string]string{
		"prompt": prompt,
	})
	request.Header.Set("X-CSRF-Token", csrfToken)
	response, body := doServedRequest(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /chat/turns status = %d, want 201; body = %q", response.StatusCode, body)
	}

	var payload servedTurnResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode turn response error = %v; body = %q", err, body)
	}
	if payload.TurnID == "" || payload.UserMessageID == "" || payload.AssistantMessageID == "" || payload.StreamURL == "" {
		t.Fatalf("turn response = %#v, want stable ids and stream URL", payload)
	}
	return payload
}

func newServedRequest(t *testing.T, method, url string, payload any) *http.Request {
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
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func doServedRequest(t *testing.T, client *http.Client, request *http.Request) (*http.Response, string) {
	t.Helper()

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s error = %v", request.Method, request.URL, err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		response.Body.Close()
		t.Fatalf("ReadAll response body error = %v", err)
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, string(body)
}

func csrfFromServedHTML(body string) string {
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

func textFromServedSSE(t *testing.T, body string) string {
	t.Helper()

	var text strings.Builder
	for _, data := range servedSSEData(t, body, "text") {
		var payload struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("decode text SSE data error = %v; data = %q", err, data)
		}
		text.WriteString(payload.Delta)
	}
	return text.String()
}

func servedSSEData(t *testing.T, body, eventName string) []string {
	t.Helper()

	var matches []string
	for _, raw := range strings.Split(strings.TrimSpace(body), "\n\n") {
		var event string
		var data strings.Builder
		for _, line := range strings.Split(raw, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data.WriteString(strings.TrimPrefix(line, "data: "))
			}
		}
		if event == eventName {
			matches = append(matches, data.String())
		}
	}
	return matches
}
