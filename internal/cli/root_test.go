package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/llm-chat-web/internal/llm/openresponses/fakeprovider"
)

func runCommand(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")

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

func TestNoArgsPrintsHelp(t *testing.T) {
	code, stdout, stderr := runCommand(t, "")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	if !strings.Contains(stdout, "Minimal LLM chat backend CLI") {
		t.Fatalf("stdout = %q, want help text", stdout)
	}
}
