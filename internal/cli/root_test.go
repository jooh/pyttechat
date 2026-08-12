package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/llm-chat-web/internal/auth"
	"example.com/llm-chat-web/internal/chat"
	"example.com/llm-chat-web/internal/llm"
	"example.com/llm-chat-web/internal/llm/openresponses/fakeprovider"
	"example.com/llm-chat-web/internal/storage"
	"example.com/llm-chat-web/internal/web"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
)

func runCommand(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")
	t.Setenv("PYTTECHAT_DATABASE_URL", "sqlite://"+t.TempDir()+"/pyttechat.db")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "true")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")
	t.Setenv("PYTTECHAT_USERNAME", "")
	t.Setenv("PYTTECHAT_PASSWORD", "")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", "")
	t.Setenv("PYTTECHAT_DATABASE_URL", "sqlite://"+t.TempDir()+"/pyttechat.db")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "true")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")
	t.Setenv("PYTTECHAT_USERNAME", "")
	t.Setenv("PYTTECHAT_PASSWORD", "")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", "")
	t.Setenv("PYTTECHAT_DATABASE_URL", "sqlite://"+t.TempDir()+"/pyttechat.db")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "true")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")
	t.Setenv("PYTTECHAT_USERNAME", "")
	t.Setenv("PYTTECHAT_PASSWORD", "")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", "")
	t.Setenv("PYTTECHAT_DATABASE_URL", "")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")
	t.Setenv("PYTTECHAT_USERNAME", "")
	t.Setenv("PYTTECHAT_PASSWORD", "")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", "")

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
	for i, body := range requestBodies {
		if body["instructions"] != chat.WebRenderingInstructions() {
			t.Fatalf("request %d instructions = %v, want shared web rendering instructions", i, body["instructions"])
		}
	}

	input := requireSlice(t, requestBodies[1]["input"], "input")
	if len(input) != 4 {
		t.Fatalf("second request input count = %d, want prior user, reasoning, assistant, next user: %#v", len(input), input)
	}
	firstUser := requireMap(t, input[0], "input[0]")
	priorReasoning := requireMap(t, input[1], "input[1]")
	priorAssistant := requireMap(t, input[2], "input[2]")
	secondUser := requireMap(t, input[3], "input[3]")
	if firstUser["role"] != "user" || firstUser["content"] != "first" {
		t.Fatalf("first input = %#v, want first user turn", firstUser)
	}
	if priorReasoning["type"] != "reasoning" || priorReasoning["id"] == "" {
		t.Fatalf("prior reasoning input = %#v, want reasoning item", priorReasoning)
	}
	if priorAssistant["role"] != "assistant" || priorAssistant["content"] != "Echo: first" {
		t.Fatalf("prior assistant input = %#v, want first assistant answer", priorAssistant)
	}
	if secondUser["role"] != "user" || secondUser["content"] != "second" {
		t.Fatalf("second input = %#v, want second user turn", secondUser)
	}
}

func TestAskAndChatUsePersistedAuthenticatedDefaultConversation(t *testing.T) {
	databaseURL := createCLIAuthUser(t, "cli-user", "correct horse")

	var requestBodies []map[string]any
	handler := fakeprovider.NewHandler()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer proxy.Close()

	t.Setenv("PYTTECHAT_DATABASE_URL", databaseURL)
	t.Setenv("PYTTECHAT_USERNAME", "cli-user")
	t.Setenv("PYTTECHAT_PASSWORD", "correct horse")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", proxy.URL)
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"ask", "from ask"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("ask exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if stdout.String() != "Echo: from ask\n" {
		t.Fatalf("ask stdout = %q, want echo", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute(context.Background(), []string{"chat"}, strings.NewReader("from chat\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("chat exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if stdout.String() != "Echo: from chat\n" {
		t.Fatalf("chat stdout = %q, want echo", stdout.String())
	}

	if len(requestBodies) != 2 {
		t.Fatalf("request count = %d, want ask and chat requests", len(requestBodies))
	}
	input := requireSlice(t, requestBodies[1]["input"], "chat input")
	if len(input) != 4 {
		t.Fatalf("chat input count = %d, want persisted ask user/reasoning/assistant plus chat user: %#v", len(input), input)
	}
	if requireMap(t, input[0], "input[0]")["content"] != "from ask" ||
		requireMap(t, input[2], "input[2]")["content"] != "Echo: from ask" ||
		requireMap(t, input[3], "input[3]")["content"] != "from chat" {
		t.Fatalf("chat input = %#v, want ask history before chat prompt", input)
	}

	store, err := storage.OpenSQLite(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("OpenSQLite web store error = %v, want nil", err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate web store error = %v, want nil", err)
	}
	webServer := httptest.NewServer(web.NewServer(web.Options{
		Client: cliEventClient{},
		Store:  store,
		Auth: auth.NewService(auth.Options{
			Store:      store,
			BCryptCost: bcrypt.MinCost,
		}),
		RegistrationEnabled: true,
	}))
	defer webServer.Close()

	webClient := newCookieClient(t)
	loginCSRF := fetchServedLoginCSRFToken(t, webClient, webServer.URL)
	response, body := doServedRequest(t, webClient, newServedFormRequest(t, http.MethodPost, webServer.URL+"/login", map[string]string{
		"csrf_token": loginCSRF,
		"username":   "cli-user",
		"password":   "correct horse",
	}))
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST /login final status = %d, want 200; body = %q", response.StatusCode, body)
	}
	for _, want := range []string{"from ask", "Echo: from ask", "from chat", "Echo: from chat"} {
		if !strings.Contains(body, want) {
			t.Fatalf("web body = %q, want persisted CLI history %q", body, want)
		}
	}
}

func TestAskCommandAuthenticatesWithPasswordFile(t *testing.T) {
	databaseURL := createCLIAuthUser(t, "file-user", "correct horse")
	passwordFile := filepath.Join(t.TempDir(), "password.txt")
	if err := os.WriteFile(passwordFile, []byte("correct horse\n"), 0o600); err != nil {
		t.Fatalf("WriteFile password error = %v, want nil", err)
	}

	t.Setenv("PYTTECHAT_DATABASE_URL", databaseURL)
	t.Setenv("PYTTECHAT_USERNAME", "file-user")
	t.Setenv("PYTTECHAT_PASSWORD", "")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", passwordFile)
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"ask", "from file"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if stdout.String() != "This is a dummy LLM response.\n" {
		t.Fatalf("stdout = %q, want dummy response", stdout.String())
	}

	store, err := storage.OpenSQLite(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("OpenSQLite error = %v, want nil", err)
	}
	defer store.Close()
	if migrateErr := store.Migrate(context.Background()); migrateErr != nil {
		t.Fatalf("Migrate error = %v, want nil", migrateErr)
	}
	authService := auth.NewService(auth.Options{Store: store, BCryptCost: bcrypt.MinCost})
	user, err := authService.Authenticate(context.Background(), "file-user", "correct horse")
	if err != nil {
		t.Fatalf("Authenticate stored user error = %v, want nil", err)
	}
	conversation, err := store.DefaultConversationForUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("DefaultConversationForUser error = %v, want nil", err)
	}
	messages, err := store.Messages(context.Background(), conversation.ID)
	if err != nil {
		t.Fatalf("Messages error = %v, want nil", err)
	}
	if len(messages) != 2 || messages[0].Text() != "from file" || messages[1].Text() != "This is a dummy LLM response." {
		t.Fatalf("messages = %#v, want persisted password-file ask turn", messages)
	}
}

func TestAskCommandRequiresPasswordWhenUsernameIsSet(t *testing.T) {
	databaseURL := createCLIAuthUser(t, "missing-password-user", "correct horse")
	t.Setenv("PYTTECHAT_DATABASE_URL", databaseURL)
	t.Setenv("PYTTECHAT_USERNAME", "missing-password-user")
	t.Setenv("PYTTECHAT_PASSWORD", "")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"ask", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "PYTTECHAT_PASSWORD or PYTTECHAT_PASSWORD_FILE is required") {
		t.Fatalf("stderr = %q, want missing password error", stderr.String())
	}
}

func TestAskCommandRejectsInvalidCLIAuthCredentials(t *testing.T) {
	databaseURL := createCLIAuthUser(t, "wrong-password-user", "correct horse")
	t.Setenv("PYTTECHAT_DATABASE_URL", databaseURL)
	t.Setenv("PYTTECHAT_USERNAME", "wrong-password-user")
	t.Setenv("PYTTECHAT_PASSWORD", "wrong password")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"ask", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "invalid credentials") {
		t.Fatalf("stderr = %q, want invalid credentials", stderr.String())
	}
}

func TestAskCommandSendsRenderingInstructionsToProxy(t *testing.T) {
	var requestBody map[string]any
	handler := fakeprovider.NewHandler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll request body error = %v", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(rawBody))

		if err := json.Unmarshal(rawBody, &requestBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}

		handler.ServeHTTP(w, r)
	}))
	defer server.Close()

	t.Setenv("PYTTECHAT_LLM_PROXY_URL", server.URL)
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"ask", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if requestBody["instructions"] != chat.WebRenderingInstructions() {
		t.Fatalf("request instructions = %v, want shared web rendering instructions", requestBody["instructions"])
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

func TestChatCommandRejectsBlankInputLine(t *testing.T) {
	code, _, stderr := runCommand(t, "\n", "chat")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "prompt must not be empty") {
		t.Fatalf("stderr = %q, want empty prompt error", stderr)
	}
}

func TestChatCommandReturnsScannerError(t *testing.T) {
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"chat"}, failingReader{}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unexpected EOF") {
		t.Fatalf("stderr = %q, want scanner read failure", stderr.String())
	}
}

func TestAskAndChatReturnUpstreamFailures(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "proxy failed", http.StatusBadGateway)
	}))
	defer proxy.Close()

	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{name: "ask", args: []string{"ask", "hello"}},
		{name: "chat", args: []string{"chat"}, stdin: "hello\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PYTTECHAT_LLM_PROXY_URL", proxy.URL)
			t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
			t.Setenv("PYTTECHAT_MODEL", "")
			t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
			t.Setenv("PYTTECHAT_WEB_ADDR", "")
			t.Setenv("PYTTECHAT_SECURE_COOKIES", "")

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Execute(context.Background(), tc.args, strings.NewReader(tc.stdin), &stdout, &stderr)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), "status 502") {
				t.Fatalf("stderr = %q, want upstream status", stderr.String())
			}
		})
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

func TestAskCommandReturnsUsageWriteFailure(t *testing.T) {
	original := printUsage
	printUsage = func(*cobra.Command) error {
		return io.ErrClosedPipe
	}
	t.Cleanup(func() {
		printUsage = original
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"ask"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "closed pipe") {
		t.Fatalf("stderr = %q, want usage write failure", stderr.String())
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

func TestEnvironmentHelpersAndProxyTimeoutDefault(t *testing.T) {
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "2s")
	t.Setenv("PYTTECHAT_WEB_ADDR", "127.0.0.1:3001")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "yes")
	t.Setenv("PYTTECHAT_DATABASE_URL", "sqlite:///tmp/pyttechat-test.db")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "off")
	t.Setenv("PYTTECHAT_SESSION_TTL", "2h")

	command := NewRootCommand(strings.NewReader(""), io.Discard, io.Discard)
	timeoutFlag := command.PersistentFlags().Lookup("proxy-timeout")
	if timeoutFlag == nil || timeoutFlag.DefValue != "2s" {
		t.Fatalf("proxy-timeout default = %#v, want 2s", timeoutFlag)
	}
	databaseFlag := command.PersistentFlags().Lookup("database-url")
	if databaseFlag == nil || databaseFlag.DefValue != "sqlite:///tmp/pyttechat-test.db" {
		t.Fatalf("database-url default = %#v, want env database URL", databaseFlag)
	}
	sessionTTLFlag := command.PersistentFlags().Lookup("session-ttl")
	if sessionTTLFlag == nil || sessionTTLFlag.DefValue != "2h0m0s" {
		t.Fatalf("session-ttl default = %#v, want 2h", sessionTTLFlag)
	}
	serveCommand, _, err := command.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("Find serve command error = %v, want nil", err)
	}
	registrationFlag := serveCommand.Flags().Lookup("registration-enabled")
	if registrationFlag == nil || registrationFlag.DefValue != "false" {
		t.Fatalf("registration-enabled default = %#v, want false from env", registrationFlag)
	}

	if got := envString("PYTTECHAT_WEB_ADDR", "fallback"); got != "127.0.0.1:3001" {
		t.Fatalf("envString = %q, want configured address", got)
	}
	if !envBool("PYTTECHAT_SECURE_COOKIES") {
		t.Fatalf("envBool yes = false, want true")
	}

	t.Setenv("PYTTECHAT_SECURE_COOKIES", "off")
	if envBool("PYTTECHAT_SECURE_COOKIES") {
		t.Fatalf("envBool off = true, want false")
	}
}

func TestPrintStreamHandlesReasoningOnlyAndWriterErrors(t *testing.T) {
	t.Run("reasoning only", func(t *testing.T) {
		stream := newCLITestTurnStream(t, []llm.Event{
			{Type: llm.EventReasoningDelta, Delta: "thinking"},
			{Type: llm.EventCompleted},
		})
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		if err := printStream(stream, &stdout, &stderr); err != nil {
			t.Fatalf("printStream error = %v, want nil", err)
		}
		if stdout.String() != "" {
			t.Fatalf("stdout = %q, want no newline without text", stdout.String())
		}
		if stderr.String() != "thinking" {
			t.Fatalf("stderr = %q, want reasoning", stderr.String())
		}
	})

	t.Run("stdout write failure", func(t *testing.T) {
		stream := newCLITestTurnStream(t, []llm.Event{
			{Type: llm.EventTextDelta, Delta: "answer"},
		})

		if err := printStream(stream, failingWriter{}, io.Discard); err == nil {
			t.Fatalf("printStream error = nil, want stdout writer error")
		}
	})

	t.Run("stderr write failure", func(t *testing.T) {
		stream := newCLITestTurnStream(t, []llm.Event{
			{Type: llm.EventReasoningDelta, Delta: "thinking"},
		})

		if err := printStream(stream, io.Discard, failingWriter{}); err == nil {
			t.Fatalf("printStream error = nil, want stderr writer error")
		}
	})

	t.Run("stream failure", func(t *testing.T) {
		session := chat.NewService(cliErrorClient{err: io.ErrUnexpectedEOF}).NewSession()
		stream, err := session.Send(context.Background(), "hello", chat.SendOptions{})
		if err != nil {
			t.Fatalf("Send() error = %v, want nil", err)
		}
		if err := printStream(stream, io.Discard, io.Discard); err == nil {
			t.Fatalf("printStream error = nil, want stream error")
		}
	})

	t.Run("newline write failure", func(t *testing.T) {
		stream := newCLITestTurnStream(t, []llm.Event{
			{Type: llm.EventTextDelta, Delta: "answer"},
			{Type: llm.EventCompleted},
		})
		writer := &failAfterWriter{failAt: 1}

		if err := printStream(stream, writer, io.Discard); err == nil {
			t.Fatalf("printStream error = nil, want newline writer error")
		}
	})
}

func TestChatCommandReturnsPrintStreamFailure(t *testing.T) {
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")

	code := Execute(context.Background(), []string{"chat"}, strings.NewReader("hello\n"), failingWriter{}, io.Discard)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestCLIPasswordReturnsFileReadFailure(t *testing.T) {
	t.Setenv("PYTTECHAT_USERNAME", "alice")
	t.Setenv("PYTTECHAT_PASSWORD", "")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", filepath.Join(t.TempDir(), "missing-password"))
	t.Setenv("PYTTECHAT_DATABASE_URL", "sqlite://"+t.TempDir()+"/pyttechat.db")
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"ask", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "missing-password") {
		t.Fatalf("stderr = %q, want missing password file path", stderr.String())
	}
}

func TestNoArgsReturnsHelpWriteFailure(t *testing.T) {
	original := commandHelp
	commandHelp = func(*cobra.Command) error {
		return io.ErrClosedPipe
	}
	t.Cleanup(func() {
		commandHelp = original
	})

	var stderr bytes.Buffer
	code := Execute(context.Background(), nil, strings.NewReader(""), io.Discard, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "closed pipe") {
		t.Fatalf("stderr = %q, want help write error", stderr.String())
	}
}

func TestServeCommandStartsWebHandler(t *testing.T) {
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TOKEN", "")
	t.Setenv("PYTTECHAT_MODEL", "")
	t.Setenv("PYTTECHAT_LLM_PROXY_TIMEOUT", "")
	t.Setenv("PYTTECHAT_WEB_ADDR", "")
	t.Setenv("PYTTECHAT_SECURE_COOKIES", "")
	t.Setenv("PYTTECHAT_DATABASE_URL", "sqlite://"+t.TempDir()+"/pyttechat.db")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "true")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")
	t.Setenv("PYTTECHAT_USERNAME", "")
	t.Setenv("PYTTECHAT_PASSWORD", "")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", "")

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
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/login", nil)
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET / error = %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("GET /login status = %d, want 200; body = %q", response.StatusCode, raw)
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
	t.Setenv("PYTTECHAT_DATABASE_URL", "sqlite://"+t.TempDir()+"/pyttechat.db")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "true")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")
	t.Setenv("PYTTECHAT_USERNAME", "")
	t.Setenv("PYTTECHAT_PASSWORD", "")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", "")

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
	csrfToken := registerServedUser(t, client, baseURL, "served-user", "correct horse")
	turn := createServedTurn(t, client, baseURL, csrfToken, "hello from browser")

	eventsResponse, eventsBody := doServedRequest(t, client, newServedRequest(t, http.MethodGet, baseURL+turn.StreamURL, nil))
	defer eventsResponse.Body.Close()
	if eventsResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET turn events status = %d, want 200; body = %q", eventsResponse.StatusCode, eventsBody)
	}
	if got := eventsResponse.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("GET turn events Content-Type = %q, want text/event-stream", got)
	}
	if got := htmlFromServedSSE(t, eventsBody); !strings.Contains(got, "<p>Echo: hello from browser</p>") {
		t.Fatalf("streamed html = %q, want fake proxy echo", got)
	}
	if len(servedSSEData(t, eventsBody, "text")) != 0 {
		t.Fatalf("SSE body = %q, did not expect assistant text events", eventsBody)
	}
	doneHTML := doneHTMLFromServedSSE(t, eventsBody)
	if doneHTML == "" {
		t.Fatalf("SSE body = %q, want done event", eventsBody)
	}
	if !strings.Contains(doneHTML, "<p>Echo: hello from browser</p>") {
		t.Fatalf("done html = %q, want fake proxy echo", doneHTML)
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
	if body["instructions"] != chat.WebRenderingInstructions() {
		t.Fatalf("proxy request instructions = %v, want shared web rendering instructions", body["instructions"])
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("proxy request reasoning = %#v, want object", body["reasoning"])
	}
	if reasoning["summary"] != "auto" || reasoning["effort"] != "medium" {
		t.Fatalf("proxy request reasoning = %#v, want summary auto and effort medium", reasoning)
	}
	input := requireSlice(t, body["input"], "input")
	if len(input) != 1 {
		t.Fatalf("proxy request input = %#v, want one browser user message", input)
	}
	message := requireMap(t, input[0], "input[0]")
	if message["role"] != "user" || message["content"] != "hello from browser" {
		t.Fatalf("proxy request input message = %#v, want submitted browser prompt", message)
	}
}

func TestServeCommandReportsBindFailure(t *testing.T) {
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
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

func TestServeCommandReportsInjectedShutdownAndServeErrors(t *testing.T) {
	t.Setenv("PYTTECHAT_DATABASE_URL", "sqlite://"+t.TempDir()+"/pyttechat.db")
	t.Setenv("PYTTECHAT_REGISTRATION_ENABLED", "true")
	t.Setenv("PYTTECHAT_SESSION_TTL", "")
	t.Setenv("PYTTECHAT_USERNAME", "")
	t.Setenv("PYTTECHAT_PASSWORD", "")
	t.Setenv("PYTTECHAT_PASSWORD_FILE", "")

	originalListen := listenTCP
	originalServer := newWebServer
	t.Cleanup(func() {
		listenTCP = originalListen
		newWebServer = originalServer
	})

	t.Run("shutdown failure", func(t *testing.T) {
		listener := newFakeListener("127.0.0.1:3000")
		server := newFakeWebServer(nil, errors.New("shutdown failed"))
		listenTCP = func(context.Context, string) (net.Listener, error) {
			return listener, nil
		}
		newWebServer = func(string, http.Handler) webServer {
			return server
		}

		ctx, cancel := context.WithCancel(context.Background())
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		result := make(chan int, 1)
		go func() {
			result <- Execute(ctx, []string{"serve"}, strings.NewReader(""), &stdout, &stderr)
		}()
		server.waitForServe(t)
		cancel()
		code := waitExitCode(t, result)
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "shutdown failed") {
			t.Fatalf("stderr = %q, want shutdown error", stderr.String())
		}
		if !listener.closed {
			t.Fatalf("listener was not closed")
		}
	})

	t.Run("serve failure", func(t *testing.T) {
		listener := newFakeListener("127.0.0.1:3001")
		server := newFakeWebServer(errors.New("serve failed"), nil)
		listenTCP = func(context.Context, string) (net.Listener, error) {
			return listener, nil
		}
		newWebServer = func(string, http.Handler) webServer {
			return server
		}

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := Execute(context.Background(), []string{"serve"}, strings.NewReader(""), &stdout, &stderr)
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "serve failed") {
			t.Fatalf("stderr = %q, want serve error", stderr.String())
		}
	})

	t.Run("server closed", func(t *testing.T) {
		listenTCP = func(context.Context, string) (net.Listener, error) {
			return newFakeListener("127.0.0.1:3002"), nil
		}
		newWebServer = func(string, http.Handler) webServer {
			return newFakeWebServer(http.ErrServerClosed, nil)
		}

		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := Execute(context.Background(), []string{"serve"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
		}
	})
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

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

type failAfterWriter struct {
	writes int
	failAt int
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAt {
		return 0, io.ErrClosedPipe
	}
	w.writes++
	return len(p), nil
}

type cliErrorClient struct {
	err error
}

func (c cliErrorClient) Stream(context.Context, llm.Request) (llm.Stream, error) {
	return cliErrorStream(c), nil
}

type cliErrorStream struct {
	err error
}

func (s cliErrorStream) Next() (llm.Event, error) {
	return llm.Event{}, s.err
}

func (cliErrorStream) Close() error {
	return nil
}

type cliEventClient struct {
	events []llm.Event
}

func (c cliEventClient) Stream(context.Context, llm.Request) (llm.Stream, error) {
	return &cliEventStream{events: append([]llm.Event(nil), c.events...)}, nil
}

type cliEventStream struct {
	events []llm.Event
	index  int
}

func (s *cliEventStream) Next() (llm.Event, error) {
	if s.index >= len(s.events) {
		return llm.Event{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (*cliEventStream) Close() error {
	return nil
}

func newCLITestTurnStream(t *testing.T, events []llm.Event) *chat.TurnStream {
	t.Helper()

	session := chat.NewService(cliEventClient{events: events}).NewSession()
	stream, err := session.Send(context.Background(), "hello", chat.SendOptions{})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	return stream
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

type fakeListener struct {
	addr   net.Addr
	closed bool
}

func newFakeListener(addr string) *fakeListener {
	return &fakeListener{addr: fakeAddr(addr)}
}

func (l *fakeListener) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}

func (l *fakeListener) Close() error {
	l.closed = true
	return nil
}

func (l *fakeListener) Addr() net.Addr {
	return l.addr
}

type fakeAddr string

func (a fakeAddr) Network() string {
	return "tcp"
}

func (a fakeAddr) String() string {
	return string(a)
}

type fakeWebServer struct {
	serveErr    error
	shutdownErr error
	started     chan struct{}
	done        chan struct{}
	closeOnce   sync.Once
	startOnce   sync.Once
}

func newFakeWebServer(serveErr, shutdownErr error) *fakeWebServer {
	return &fakeWebServer{
		serveErr:    serveErr,
		shutdownErr: shutdownErr,
		started:     make(chan struct{}),
		done:        make(chan struct{}),
	}
}

func (s *fakeWebServer) Serve(net.Listener) error {
	s.startOnce.Do(func() {
		close(s.started)
	})
	if s.serveErr != nil {
		return s.serveErr
	}
	<-s.done
	return http.ErrServerClosed
}

func (s *fakeWebServer) Shutdown(context.Context) error {
	s.closeOnce.Do(func() {
		close(s.done)
	})
	return s.shutdownErr
}

func (s *fakeWebServer) waitForServe(t *testing.T) {
	t.Helper()

	select {
	case <-s.started:
	case <-time.After(time.Second):
		t.Fatalf("fake web server did not start serving")
	}
}

func waitExitCode(t *testing.T, result <-chan int) int {
	t.Helper()

	select {
	case code := <-result:
		return code
	case <-time.After(time.Second):
		t.Fatalf("command did not exit")
		return 1
	}
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

func createCLIAuthUser(t *testing.T, username, password string) string {
	t.Helper()

	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "pyttechat.db")
	store, err := storage.OpenSQLite(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("OpenSQLite error = %v, want nil", err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate error = %v, want nil", err)
	}
	authService := auth.NewService(auth.Options{
		Store:      store,
		BCryptCost: bcrypt.MinCost,
	})
	if _, err := authService.Register(context.Background(), username, password); err != nil {
		t.Fatalf("Register error = %v, want nil", err)
	}
	return databaseURL
}

func fetchServedLoginCSRFToken(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()

	response, body := doServedRequest(t, client, newServedRequest(t, http.MethodGet, baseURL+"/login", nil))
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200; body = %q", response.StatusCode, body)
	}
	token := csrfFromServedHTML(body)
	if token == "" {
		t.Fatalf("login CSRF token is empty in body %q", body)
	}
	return token
}

func registerServedUser(t *testing.T, client *http.Client, baseURL, username, password string) string {
	t.Helper()

	response, body := doServedRequest(t, client, newServedRequest(t, http.MethodGet, baseURL+"/register", nil))
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /register status = %d, want 200; body = %q", response.StatusCode, body)
	}
	csrfToken := csrfFromServedHTML(body)
	if csrfToken == "" {
		t.Fatalf("register CSRF token is empty in body %q", body)
	}

	request := newServedFormRequest(t, http.MethodPost, baseURL+"/register", map[string]string{
		"csrf_token": csrfToken,
		"username":   username,
		"password":   password,
	})
	response, body = doServedRequest(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST /register final status = %d, want 200; body = %q", response.StatusCode, body)
	}
	rootCSRF := csrfFromServedHTML(body)
	if rootCSRF == "" {
		t.Fatalf("root CSRF token is empty after registration; body = %q", body)
	}
	return rootCSRF
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

func newServedFormRequest(t *testing.T, method, targetURL string, values map[string]string) *http.Request {
	t.Helper()

	form := make(url.Values, len(values))
	for key, value := range values {
		form.Set(key, value)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, targetURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func newServedRequest(t *testing.T, method, url string, payload any) *http.Request {
	t.Helper()

	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatalf("Encode request body error = %v", err)
		}
	}
	request, err := http.NewRequestWithContext(context.Background(), method, url, &body)
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

func htmlFromServedSSE(t *testing.T, body string) string {
	t.Helper()

	data := servedSSEData(t, body, "preview")
	if len(data) == 0 {
		return ""
	}
	var payload struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal([]byte(data[len(data)-1]), &payload); err != nil {
		t.Fatalf("decode preview SSE data error = %v; data = %q", err, data[len(data)-1])
	}
	return payload.HTML
}

func doneHTMLFromServedSSE(t *testing.T, body string) string {
	t.Helper()

	data := servedSSEData(t, body, "done")
	if len(data) == 0 {
		return ""
	}
	var payload struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal([]byte(data[len(data)-1]), &payload); err != nil {
		t.Fatalf("decode done SSE data error = %v; data = %q", err, data[len(data)-1])
	}
	return payload.HTML
}

func servedSSEData(t *testing.T, body, eventName string) []string {
	t.Helper()

	var matches []string
	for raw := range strings.SplitSeq(strings.TrimSpace(body), "\n\n") {
		var event string
		var data strings.Builder
		for line := range strings.SplitSeq(raw, "\n") {
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
