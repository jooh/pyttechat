package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/llm-chat-web/internal/auth"
	"example.com/llm-chat-web/internal/chat"
	"example.com/llm-chat-web/internal/llm"
	"example.com/llm-chat-web/internal/llm/dummy"
	"example.com/llm-chat-web/internal/storage"

	"golang.org/x/crypto/bcrypt"
)

func TestRootRendersChatPageAndSetsSessionCookie(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{
		Client: dummy.NewClient(),
		Model:  "gpt-example",
	}))
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
	if !strings.Contains(body, `<meta name="color-scheme" content="light dark">`) {
		t.Fatalf("GET / body does not contain color scheme meta tag: %q", body)
	}
	themeScript := `<script src="/assets/theme-init.js"></script>`
	firstStylesheet := `<link rel="stylesheet" href="/assets/vendor/pico.min.css">`
	themeScriptIndex := strings.Index(body, themeScript)
	stylesheetIndex := strings.Index(body, firstStylesheet)
	if themeScriptIndex < 0 {
		t.Fatalf("GET / body does not load early theme init script: %q", body)
	}
	if stylesheetIndex < 0 {
		t.Fatalf("GET / body does not load vendored Pico CSS: %q", body)
	}
	if themeScriptIndex > stylesheetIndex {
		t.Fatalf("GET / body loads theme init script after stylesheet: %q", body)
	}
	if !strings.Contains(body, `<link rel="stylesheet" href="/assets/vendor/pico.min.css">`) {
		t.Fatalf("GET / body does not load vendored Pico CSS: %q", body)
	}
	if !strings.Contains(body, `<link rel="stylesheet" href="/assets/vendor/katex/katex.min.css">`) {
		t.Fatalf("GET / body does not load vendored KaTeX CSS: %q", body)
	}
	for _, want := range []string{
		`<script defer src="/assets/vendor/katex/katex.min.js"></script>`,
		`<script defer src="/assets/vendor/katex/auto-render.min.js"></script>`,
		`<script defer src="/assets/vendor/mermaid.min.js"></script>`,
		`<script defer src="/assets/app.js"></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET / body = %q, want static renderer asset %q", body, want)
		}
	}
	if !strings.Contains(body, "Pyttechat") {
		t.Fatalf("GET / body = %q, want app shell", body)
	}
	for _, want := range []string{
		`class="model-chip"`,
		`gpt-example`,
		`class="chat-panel"`,
		`id="scroll-bottom"`,
		`id="composer-status"`,
		`aria-label="Send message"`,
		`aria-label="Stop response"`,
		`id="theme-toggle"`,
		`data-theme-toggle`,
		`aria-label="Current theme: system preference"`,
		`data-theme-icon="light"`,
		`data-theme-icon="dark"`,
		`<circle cx="12" cy="12" r="4"></circle>`,
		`<path d="M21 12.8A8 8 0 1 1 11.2 3 6.2 6.2 0 0 0 21 12.8z"></path>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET / body = %q, want rendered shell substring %q", body, want)
		}
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

func TestAuthProtectedRoutesRedirectAndPublicPagesSetSecureCookie(t *testing.T) {
	handler := newAuthTestHandler(t, dummy.NewClient(), true, true)
	server := httptest.NewServer(handler)
	defer server.Close()

	client := testNoRedirectHTTPClient(t)
	response, body := get(t, client, server.URL+"/")
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET / status = %d, want 303; body = %q", response.StatusCode, body)
	}
	if location := response.Header.Get("Location"); location != "/login" {
		t.Fatalf("GET / Location = %q, want /login", location)
	}

	response, body = get(t, client, server.URL+"/login")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if !strings.Contains(body, "Sign in") || csrfFromHTML(t, body) == "" {
		t.Fatalf("GET /login body = %q, want login form with CSRF token", body)
	}
	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v, want secure HttpOnly Lax session cookie", cookie)
	}
	if !strings.Contains(cookie.Value, ".") {
		t.Fatalf("session cookie value = %q, want sessionID.secret format", cookie.Value)
	}
}

func TestAuthRegisterLoginLogoutAndCSRF(t *testing.T) {
	handler := newAuthTestHandler(t, dummy.NewClient(dummy.Turn{TextChunks: []string{"answer"}}), true, false)
	server := httptest.NewServer(handler)
	defer server.Close()

	client := testHTTPClient(t)
	registerCSRF := fetchAuthCSRFToken(t, client, server.URL, "/register")

	badRequest := newFormRequest(t, http.MethodPost, server.URL+"/register", map[string]string{
		"username": "alice",
		"password": "correct horse",
	})
	response, body := do(t, client, badRequest)
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /register without csrf status = %d, want 403; body = %q", response.StatusCode, body)
	}

	rootCSRF := submitAuthForm(t, client, server.URL+"/register", map[string]string{
		"csrf_token": registerCSRF,
		"username":   "alice",
		"password":   "correct horse",
	})
	turn := createTurn(t, client, server.URL, rootCSRF, "hello")
	response, body = get(t, client, server.URL+turn.StreamURL)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	logoutRequest := newFormRequest(t, http.MethodPost, server.URL+"/logout", nil)
	response, body = do(t, client, logoutRequest)
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /logout without csrf status = %d, want 403; body = %q", response.StatusCode, body)
	}

	logoutRequest = newFormRequest(t, http.MethodPost, server.URL+"/logout", map[string]string{"csrf_token": rootCSRF})
	response, body = do(t, client, logoutRequest)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST /logout final status = %d, want 200 after redirect; body = %q", response.StatusCode, body)
	}
	if !strings.Contains(body, "Sign in") {
		t.Fatalf("POST /logout body = %q, want login page after redirect", body)
	}

	loginCSRF := csrfFromHTML(t, body)
	rootCSRF = submitAuthForm(t, client, server.URL+"/login", map[string]string{
		"csrf_token": loginCSRF,
		"username":   "ALICE",
		"password":   "correct horse",
	})
	response, body = get(t, client, server.URL+"/")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET / after login status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if rootCSRF == "" || !strings.Contains(body, "hello") || !strings.Contains(body, "answer") {
		t.Fatalf("GET / after login body = %q, want persisted history and csrf", body)
	}
}

func TestAuthPerUserHistoryIsolationAndPersistedReload(t *testing.T) {
	store := newWebTestStore(t)
	llmClient := dummy.NewClient(dummy.Turn{TextChunks: []string{"first answer"}})
	handler := newAuthTestHandlerForStore(t, store, llmClient, true, false)
	server := httptest.NewServer(handler)

	alice := testHTTPClient(t)
	aliceCSRF := registerAuthUser(t, alice, server.URL, "alice", "correct horse")
	turn := createTurn(t, alice, server.URL, aliceCSRF, "alice prompt")
	response, body := get(t, alice, server.URL+turn.StreamURL)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("alice events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	bob := testHTTPClient(t)
	bobCSRF := registerAuthUser(t, bob, server.URL, "bob", "correct horse")
	response, body = get(t, bob, server.URL+"/")
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bob GET / status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if bobCSRF == "" || strings.Contains(body, "alice prompt") || strings.Contains(body, "first answer") {
		t.Fatalf("bob body = %q, did not expect alice history", body)
	}

	server.Close()
	restarted := httptest.NewServer(newAuthTestHandlerForStore(t, store, dummy.NewClient(), true, false))
	defer restarted.Close()

	response, body = get(t, alice, restarted.URL+"/")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("alice restarted GET / status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if !strings.Contains(body, "alice prompt") || !strings.Contains(body, "first answer") {
		t.Fatalf("alice restarted body = %q, want persisted history", body)
	}
}

func TestAssetsRouteAndDefaultNotFound(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{Client: dummy.NewClient()}))
	defer server.Close()

	client := testHTTPClient(t)
	response, body := get(t, client, server.URL+"/favicon.ico")
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("GET favicon status = %d, want 204; body = %q", response.StatusCode, body)
	}

	response, body = get(t, client, server.URL+"/assets/app.css")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET asset status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "text/css") {
		t.Fatalf("asset Content-Type = %q, want text/css", got)
	}
	if !strings.Contains(body, `.markdown-body .chroma`) || !strings.Contains(body, `.chroma .k`) {
		t.Fatalf("app CSS = %q, want Chroma syntax highlight styles", body)
	}
	if !strings.Contains(body, `.composer-box:focus-within`) || !strings.Contains(body, `var(--pico-primary-focus)`) {
		t.Fatalf("app CSS = %q, want composer focus-within indicator", body)
	}
	for _, want := range []string{
		`.theme-toggle`,
		`:root[data-theme="dark"]`,
		`:root:not([data-theme])`,
		`--chroma-color: #c9d1d9;`,
		`--status-sweep-low: rgb(32 32 32);`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("app CSS = %q, want theme style %q", body, want)
		}
	}
	for _, want := range []string{
		`status-sweep`,
		`@keyframes status-sweep`,
		`.message-status`,
		`animation: status-sweep 2.2s`,
		`--status-sweep-low: rgb(32 32 32);`,
		`min-height: 2.1rem;`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("app CSS = %q, want streaming UI style %q", body, want)
		}
	}
	for _, unwanted := range []string{
		`@keyframes blink`,
		`message-text:empty::after`,
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("app CSS = %q, did not expect removed streaming cursor style %q", body, unwanted)
		}
	}

	response, body = get(t, client, server.URL+"/assets/app.js")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET JS asset status = %d, want 200; body = %q", response.StatusCode, body)
	}
	for _, want := range []string{
		`const themeStorageKey = 'pyttechat.theme';`,
		`window.localStorage.getItem(themeStorageKey)`,
		`window.matchMedia('(prefers-color-scheme: dark)')`,
		`document.documentElement.dataset.theme`,
		`data-theme-toggle`,
		`refreshMermaidThemes`,
		`window.mermaid.initialize({`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("app JS = %q, want theme behavior %q", body, want)
		}
	}
	if !strings.Contains(body, `message-error-detail`) || !strings.Contains(body, `replaceChildren(error)`) {
		t.Fatalf("app JS = %q, want failed stream messages to replace partial output with an inline error", body)
	}
	for _, want := range []string{
		`ensureThinkingStatus`,
		`completeThinkingStatus`,
		`replaceFinalStatuses`,
		`enhanceMath`,
		`enhanceMermaidBlocks`,
		`data-mermaid-toggle`,
		`aria-expanded`,
		`thinking...`,
		`const nearBottomThreshold = 32;`,
		`const wasNearBottom = isNearBottom();`,
		`scrollToBottom(false, wasNearBottom);`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("app JS = %q, want streaming UI behavior %q", body, want)
		}
	}
	if strings.Contains(body, `prompt.focus()`) {
		t.Fatalf("app JS = %q, did not expect turn completion to focus composer", body)
	}

	response, body = get(t, client, server.URL+"/assets/theme-init.js")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET theme init asset status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "text/javascript") && !strings.Contains(got, "application/javascript") {
		t.Fatalf("theme init Content-Type = %q, want JavaScript", got)
	}
	for _, want := range []string{
		`pyttechat.theme`,
		`localStorage.getItem`,
		`document.documentElement.dataset.theme`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("theme init JS = %q, want early theme behavior %q", body, want)
		}
	}

	response, body = get(t, client, server.URL+"/assets/vendor/pico.min.css")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET vendored Pico asset status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if got := response.Header.Get("Content-Type"); !strings.Contains(got, "text/css") {
		t.Fatalf("vendored Pico Content-Type = %q, want text/css", got)
	}
	if !strings.Contains(body, "Pico CSS") {
		t.Fatalf("vendored Pico asset body = %q, want Pico CSS", body)
	}

	response, body = get(t, client, server.URL+"/assets/vendor/katex/katex.min.css")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET vendored KaTeX CSS status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if !strings.Contains(body, "KaTeX") {
		t.Fatalf("vendored KaTeX CSS body = %q, want KaTeX asset", body)
	}

	response, body = get(t, client, server.URL+"/assets/vendor/mermaid.min.js")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET vendored Mermaid JS status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if !strings.Contains(body, "mermaid") {
		t.Fatalf("vendored Mermaid JS body = %q, want Mermaid asset", body)
	}

	response, body = get(t, client, server.URL+"/missing")
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing status = %d, want 404; body = %q", response.StatusCode, body)
	}
}

func TestSessionCreationFailuresReturnErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   io.Reader
	}{
		{name: "index", method: http.MethodGet, path: "/"},
		{name: "create turn", method: http.MethodPost, path: "/chat/turns", body: strings.NewReader(`{"prompt":"hello"}`)},
		{name: "events", method: http.MethodGet, path: "/chat/turns/missing/events"},
		{name: "abort", method: http.MethodPost, path: "/chat/turns/missing/abort"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withRandomReader(t, &sequenceRandomReader{failAt: 1})
			server := NewServer(Options{Client: dummy.NewClient()})
			request := httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, tc.body)
			if tc.body != nil {
				request.Header.Set("Content-Type", "application/json")
			}
			recorder := httptest.NewRecorder()

			server.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("%s %s status = %d, want 500; body = %q", tc.method, tc.path, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestSessionCreationReturnsCSRFIDFailure(t *testing.T) {
	withRandomReader(t, &sequenceRandomReader{failAt: 2})
	server := NewServer(Options{Client: dummy.NewClient()})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("GET / status = %d, want 500; body = %q", recorder.Code, recorder.Body.String())
	}
}

func TestIndexTemplateExecutionError(t *testing.T) {
	server := NewServer(Options{Client: dummy.NewClient()})
	server.template = template.Must(template.New("index.html").Parse(`{{template "missing" .}}`))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("GET / status = %d, want 500; body = %q", recorder.Code, recorder.Body.String())
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

func TestCreateTurnRejectsMalformedAndTrailingJSON(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{Client: dummy.NewClient()}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"prompt":`},
		{name: "trailing", body: `{"prompt":"hello"} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/chat/turns", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("NewRequest error = %v", err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(csrfHeaderName, csrfToken)

			response, body := do(t, client, request)
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("POST /chat/turns status = %d, want 400; body = %q", response.StatusCode, body)
			}
		})
	}
}

func TestCreateTurnRejectsTrailingJSONReadError(t *testing.T) {
	server := NewServer(Options{Client: dummy.NewClient()})
	server.sessions["sess_test"] = &browserSession{
		id:    "sess_test",
		csrf:  "csrf_test",
		chat:  chat.NewService(dummy.NewClient()).NewSession(),
		turns: map[string]*turnJob{},
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/chat/turns", nil)
	request.Body = &webErrAfterJSONBody{data: []byte(`{"prompt":"hello"}`)}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(csrfHeaderName, "csrf_test")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess_test"})
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("POST /chat/turns status = %d, want 400; body = %q", recorder.Code, recorder.Body.String())
	}
}

func TestCreateTurnReturnsTurnIDCreationFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt int
	}{
		{name: "turn id", failAt: 1},
		{name: "user message id", failAt: 2},
		{name: "assistant message id", failAt: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withRandomReader(t, &sequenceRandomReader{failAt: tc.failAt})
			server := NewServer(Options{Client: dummy.NewClient()})
			server.sessions["sess_test"] = &browserSession{
				id:    "sess_test",
				csrf:  "csrf_test",
				chat:  chat.NewService(dummy.NewClient()).NewSession(),
				turns: map[string]*turnJob{},
			}
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/chat/turns", strings.NewReader(`{"prompt":"hello"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(csrfHeaderName, "csrf_test")
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess_test"})
			recorder := httptest.NewRecorder()

			server.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("POST /chat/turns status = %d, want 500; body = %q", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCreateTurnRejectsConcurrentTurn(t *testing.T) {
	llmClient := newControlledClient()
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	first := createTurn(t, client, server.URL, csrfToken, "first")
	_ = llmClient.waitForContext(t)

	request := newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns", map[string]string{
		"prompt": "second",
	})
	request.Header.Set(csrfHeaderName, csrfToken)
	response, body := do(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("POST /chat/turns status = %d, want 409; body = %q", response.StatusCode, body)
	}

	request = newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+first.TurnID+"/abort", nil)
	request.Header.Set(csrfHeaderName, csrfToken)
	abortResponse, abortBody := do(t, client, request)
	defer abortResponse.Body.Close()
	if abortResponse.StatusCode != http.StatusOK {
		t.Fatalf("cleanup abort status = %d, want 200; body = %q", abortResponse.StatusCode, abortBody)
	}
}

func TestCreateTurnStartsJobAndStreamsReplayableEvents(t *testing.T) {
	completedAt := time.Date(2026, 5, 25, 12, 34, 56, 0, time.UTC)
	withTimeNow(t, func() time.Time { return completedAt })
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
	assertFrameEvents(t, frames, []string{"reasoning", "preview", "preview", "done"})
	if !hasFrame(frames, "reasoning", `"delta":"think"`) {
		t.Fatalf("SSE frames = %#v, want reasoning frame", frames)
	}
	if hasEvent(frames, "text") {
		t.Fatalf("SSE frames = %#v, did not expect assistant text events", frames)
	}
	firstPreview := decodePreviewFrame(t, frames[1])
	if !strings.Contains(firstPreview.HTML, "<p>hel</p>") {
		t.Fatalf("first preview frame = %#v, want rendered partial paragraph", firstPreview)
	}
	secondPreview := decodePreviewFrame(t, frames[2])
	if !strings.Contains(secondPreview.HTML, "<p>hello</p>") {
		t.Fatalf("second preview frame = %#v, want rendered complete paragraph", secondPreview)
	}
	if !hasFrame(frames, "done", `"response_id":"dummy-response-1"`) {
		t.Fatalf("SSE frames = %#v, want done frame", frames)
	}
	done := decodeDoneFrame(t, frames[3])
	if !strings.Contains(done.HTML, "<p>hello</p>") {
		t.Fatalf("done frame = %#v, want final rendered HTML", done)
	}
	if done.CompletedAt != completedAt.Format(time.RFC3339) {
		t.Fatalf("done completed_at = %q, want %q", done.CompletedAt, completedAt.Format(time.RFC3339))
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
	if requests[0].Instructions != chat.WebRenderingInstructions() {
		t.Fatalf("request instructions = %q, want shared web rendering instructions", requests[0].Instructions)
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

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+turn.StreamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest events error = %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET events error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, raw)
	}

	llmClient.events <- llm.Event{Type: llm.EventTextDelta, Delta: "partial\n\n"}
	frame := readSSEFrame(t, bufio.NewReader(response.Body))
	if frame.Event != "preview" {
		t.Fatalf("first frame = %#v, want preview", frame)
	}
	if frame.ID == "" {
		t.Fatalf("first frame = %#v, want event id", frame)
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatalf("closing subscriber body: %v", closeErr)
	}

	select {
	case <-ctx.Done():
		t.Fatalf("LLM context was canceled when subscriber disconnected: %v", ctx.Err())
	case <-time.After(50 * time.Millisecond):
	}

	llmClient.events <- llm.Event{Type: llm.EventCompleted, ResponseID: "resp_done"}
	close(llmClient.events)

	request, err = http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+turn.StreamURL, nil)
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
	assertFrameEvents(t, frames, []string{"done"})
	if hasEvent(frames, "text") || hasFrame(frames, "preview", "partial") {
		t.Fatalf("replay body = %q, did not expect already acknowledged assistant content event", body)
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
		turn.emit("preview", htmlEvent{
			TurnID:             turn.id,
			AssistantMessageID: turn.assistantMessageID,
			HTML:               template.HTML(itoa(i)),
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

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+turn.StreamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest events error = %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET events error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, raw)
	}

	request = newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+turn.TurnID+"/abort", nil)
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

func TestAbortRequiresCSRFAndRejectsFinishedTurn(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{Client: dummy.NewClient()}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")

	response, body := get(t, client, server.URL+turn.StreamURL)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	request := newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+turn.TurnID+"/abort", nil)
	response, body = do(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("abort without csrf status = %d, want 403; body = %q", response.StatusCode, body)
	}

	request = newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+turn.TurnID+"/abort", nil)
	request.Header.Set(csrfHeaderName, csrfToken)
	response, body = do(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("abort finished status = %d, want 409; body = %q", response.StatusCode, body)
	}
}

func TestAbortResponseWaitsForTurnToReleaseSession(t *testing.T) {
	llmClient := newAbortBlockingClient()
	defer llmClient.releaseCanceledStream()
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")
	llmClient.waitForStreamStart(t)

	request := newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+turn.TurnID+"/abort", nil)
	request.Header.Set(csrfHeaderName, csrfToken)
	abortResult := make(chan httpResult, 1)
	go func() {
		response, body := do(t, client, request)
		response.Body.Close()
		abortResult <- httpResult{response: response, body: body}
	}()

	llmClient.waitForCancel(t)
	select {
	case result := <-abortResult:
		t.Fatalf("abort returned before turn released session; status = %d body = %q", result.response.StatusCode, result.body)
	case <-time.After(50 * time.Millisecond):
	}

	llmClient.releaseCanceledStream()
	result := waitHTTPResult(t, abortResult)
	abortResponse, abortBody := result.response, result.body
	defer abortResponse.Body.Close()
	if abortResponse.StatusCode != http.StatusOK {
		t.Fatalf("abort status = %d, want 200; body = %q", abortResponse.StatusCode, abortBody)
	}

	next := createTurn(t, client, server.URL, csrfToken, "follow up")
	request = newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+next.TurnID+"/abort", nil)
	request.Header.Set(csrfHeaderName, csrfToken)
	cleanupResponse, cleanupBody := do(t, client, request)
	defer cleanupResponse.Body.Close()
	if cleanupResponse.StatusCode != http.StatusOK {
		t.Fatalf("cleanup abort status = %d, want 200; body = %q", cleanupResponse.StatusCode, cleanupBody)
	}
}

func TestCompletedEventFinishesTurnWithoutWaitingForEOF(t *testing.T) {
	llmClient := newControlledClient()
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")
	_ = llmClient.waitForContext(t)

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+turn.StreamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest events error = %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET events error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, raw)
	}

	llmClient.events <- llm.Event{Type: llm.EventCompleted, ResponseID: "resp_done"}
	frame := readSSEFrame(t, bufio.NewReader(response.Body))
	if frame.Event != "done" {
		t.Fatalf("completion frame = %#v, want done", frame)
	}

	next := createTurn(t, client, server.URL, csrfToken, "follow up")
	request = newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+next.TurnID+"/abort", nil)
	request.Header.Set(csrfHeaderName, csrfToken)
	abortResponse, abortBody := do(t, client, request)
	defer abortResponse.Body.Close()
	if abortResponse.StatusCode != http.StatusOK {
		t.Fatalf("cleanup abort status = %d, want 200; body = %q", abortResponse.StatusCode, abortBody)
	}
}

func TestTerminalEventAllowsImmediateFollowUpBeforeStreamClose(t *testing.T) {
	llmClient := newCloseBlockingClient()
	defer llmClient.unblockClose()
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+turn.StreamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest events error = %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET events error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, raw)
	}

	frame := readSSEFrame(t, bufio.NewReader(response.Body))
	if frame.Event != "done" {
		t.Fatalf("terminal frame = %#v, want done", frame)
	}
	llmClient.waitForBlockedClose(t)

	next := createTurn(t, client, server.URL, csrfToken, "follow up")
	request = newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+next.TurnID+"/abort", nil)
	request.Header.Set(csrfHeaderName, csrfToken)
	abortResponse, abortBody := do(t, client, request)
	defer abortResponse.Body.Close()
	if abortResponse.StatusCode != http.StatusOK {
		t.Fatalf("cleanup abort status = %d, want 200; body = %q", abortResponse.StatusCode, abortBody)
	}

	llmClient.unblockClose()
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

func TestIndexRendersCompletedMessagesAndReusesSessionCookie(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{
		Client: dummy.NewClient(dummy.Turn{TextChunks: []string{"**answer**"}}),
		Model:  "gpt-actions",
	}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "<hello>")
	response, body := get(t, client, server.URL+turn.StreamURL)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	response, body = get(t, client, server.URL+"/")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if len(response.Cookies()) != 0 {
		t.Fatalf("Set-Cookie count = %d, want existing session reused without a new cookie", len(response.Cookies()))
	}
	if !strings.Contains(body, `message-user`) || !strings.Contains(body, `&lt;hello&gt;`) {
		t.Fatalf("GET / body = %q, want escaped user message", body)
	}
	if strings.Contains(body, `<hello>`) {
		t.Fatalf("GET / body = %q, did not expect raw user HTML", body)
	}
	if !strings.Contains(body, `message-assistant`) || !strings.Contains(body, `<strong>answer</strong>`) {
		t.Fatalf("GET / body = %q, want rendered assistant markdown", body)
	}
	for _, want := range []string{
		`class="message-actions"`,
		`data-copy-message`,
		`aria-label="Copy message"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET / body = %q, want completed message affordance %q", body, want)
		}
	}
	for _, unwanted := range []string{
		`class="message-header"`,
		`class="message-model">gpt-actions</span>`,
		`<span>You</span>`,
		`<span>Assistant</span>`,
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("GET / body = %q, did not expect per-message chrome %q", body, unwanted)
		}
	}
}

func TestModelDisplayLabelDefaultsWhenModelUnset(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{Client: dummy.NewClient()}))
	defer server.Close()

	client := testHTTPClient(t)
	response, body := get(t, client, server.URL+"/")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if !strings.Contains(body, `Proxy default`) {
		t.Fatalf("GET / body = %q, want default model label", body)
	}
}

func TestTurnStreamsPreviewsAndFinalFullRender(t *testing.T) {
	llmClient := dummy.NewClient(dummy.Turn{
		TextChunks: []string{"first\n\n", "second"},
	})
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")

	response, body := get(t, client, server.URL+turn.StreamURL)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	frames := parseSSE(t, body)
	assertFrameEvents(t, frames, []string{"preview", "preview", "done"})
	first := decodePreviewFrame(t, frames[0])
	second := decodePreviewFrame(t, frames[1])
	done := decodeDoneFrame(t, frames[2])
	if !strings.Contains(first.HTML, "<p>first</p>") {
		t.Fatalf("first preview frame = %#v, want first paragraph", first)
	}
	if !strings.Contains(second.HTML, "<p>first</p>") || !strings.Contains(second.HTML, "<p>second</p>") {
		t.Fatalf("second preview frame = %#v, want full rendered preview", second)
	}
	if !strings.Contains(done.HTML, "<p>first</p>") || !strings.Contains(done.HTML, "<p>second</p>") {
		t.Fatalf("done frame = %#v, want complete rendered message", done)
	}
	if hasEvent(frames, "text") {
		t.Fatalf("frames = %#v, did not expect assistant text events", frames)
	}
}

func TestTurnStreamsPreviewForSingleParagraphWithoutBlockBoundary(t *testing.T) {
	llmClient := newControlledClient()
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")
	_ = llmClient.waitForContext(t)

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+turn.StreamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest events error = %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET events error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, raw)
	}

	reader := bufio.NewReader(response.Body)
	llmClient.events <- llm.Event{Type: llm.EventTextDelta, Delta: "partial"}
	frame := readSSEFrame(t, reader)
	if frame.Event != "preview" {
		t.Fatalf("first frame = %#v, want preview", frame)
	}
	preview := decodePreviewFrame(t, frame)
	if !strings.Contains(preview.HTML, "<p>partial</p>") {
		t.Fatalf("preview frame = %#v, want rendered partial paragraph", preview)
	}

	llmClient.events <- llm.Event{Type: llm.EventCompleted, ResponseID: "resp_done"}
	frame = readSSEFrame(t, reader)
	if frame.Event != "done" {
		t.Fatalf("terminal frame = %#v, want done", frame)
	}
}

func TestTurnStreamingPreviewsFenceBeforeClosed(t *testing.T) {
	llmClient := newControlledClient()
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")
	_ = llmClient.waitForContext(t)

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+turn.StreamURL, nil)
	if err != nil {
		t.Fatalf("NewRequest events error = %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET events error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, raw)
	}

	reader := bufio.NewReader(response.Body)
	llmClient.events <- llm.Event{Type: llm.EventTextDelta, Delta: "```go\nfmt.Println(1)\n"}
	frame := readSSEFrame(t, reader)
	if frame.Event != "preview" {
		t.Fatalf("frame before closing fence = %#v, want preview", frame)
	}
	html := decodePreviewFrame(t, frame)
	if !strings.Contains(html.HTML, "fmt") {
		t.Fatalf("preview frame = %#v, want rendered fenced code preview", html)
	}

	llmClient.events <- llm.Event{Type: llm.EventTextDelta, Delta: "```\n"}
	frame = readSSEFrame(t, reader)
	if frame.Event != "preview" {
		t.Fatalf("frame after closing fence = %#v, want preview", frame)
	}
	html = decodePreviewFrame(t, frame)
	if !strings.Contains(html.HTML, `class="chroma"`) || !strings.Contains(html.HTML, "fmt") {
		t.Fatalf("preview frame = %#v, want highlighted fenced code", html)
	}

	llmClient.events <- llm.Event{Type: llm.EventCompleted, ResponseID: "resp_done"}
	frame = readSSEFrame(t, reader)
	if frame.Event != "done" {
		t.Fatalf("terminal frame = %#v, want done", frame)
	}
}

func TestTurnStreamingSanitizesUnsafeModelHTML(t *testing.T) {
	llmClient := dummy.NewClient(dummy.Turn{
		TextChunks: []string{`<script>alert(1)</script>

[ok](https://example.com)

<a href="javascript:alert(1)" onclick="bad">bad</a>`},
	})
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")
	response, body := get(t, client, server.URL+turn.StreamURL)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	frames := parseSSE(t, body)
	for _, frame := range frames {
		if frame.Event != "preview" {
			continue
		}
		payload := decodePreviewFrame(t, frame)
		assertSafeRenderedHTML(t, payload.HTML)
	}
	done := decodeDoneFrame(t, frames[len(frames)-1])
	assertSafeRenderedHTML(t, done.HTML)
	if !strings.Contains(done.HTML, `href="https://example.com"`) {
		t.Fatalf("done HTML = %q, want safe markdown link", done.HTML)
	}
}

func TestTurnDoneRendersStructuredOutputParts(t *testing.T) {
	llmClient := webSequenceClient{events: []llm.Event{
		{Type: llm.EventTextDelta, Delta: "# Answer"},
		{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartSummary, Text: "brief summary"}},
		{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartError, Text: "partial refusal"}},
		{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartText, Text: "Follow-up"}},
		{Type: llm.EventOutputItemDone, Part: llm.Part{
			Type:     llm.PartImage,
			URL:      "/assets/app.css",
			Filename: "plot.png",
			Alt:      "Plot",
			Width:    640,
			Height:   480,
		}},
		{Type: llm.EventOutputItemDone, Part: llm.Part{
			Type:     llm.PartAttachment,
			URL:      "/assets/app.css",
			Filename: "notes.txt",
			MimeType: "text/plain",
			Size:     12,
			Text:     "attachment preview",
		}},
		{Type: llm.EventCompleted, ResponseID: "resp_done"},
	}}
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")
	response, body := get(t, client, server.URL+turn.StreamURL)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	frames := parseSSE(t, body)
	done := decodeDoneFrame(t, frames[len(frames)-1])
	for _, want := range []string{
		`<h1>Answer</h1>`,
		`class="message-part-error"`,
		`partial refusal`,
		`<p>Follow-up</p>`,
		`class="message-image"`,
		`src="/assets/app.css"`,
		`class="message-attachment"`,
		`notes.txt`,
		`attachment preview`,
	} {
		if !strings.Contains(done.HTML, want) {
			t.Fatalf("done HTML = %q, want structured output substring %q", done.HTML, want)
		}
	}
	if strings.Index(done.HTML, `<h1>Answer</h1>`) > strings.Index(done.HTML, `<p>Follow-up</p>`) {
		t.Fatalf("done HTML = %q, want initial text before later completed text part", done.HTML)
	}
	if len(done.Statuses) != 1 || done.Statuses[0].Kind != "summary" || done.Statuses[0].Text != "brief summary" {
		t.Fatalf("done statuses = %#v, want summary status", done.Statuses)
	}
}

func TestTurnDoneIncludesStatusesForSummaryOnlyOutput(t *testing.T) {
	llmClient := webSequenceClient{events: []llm.Event{
		{Type: llm.EventOutputItemDone, Part: llm.Part{Type: llm.PartSummary, Text: "brief summary"}},
		{Type: llm.EventCompleted, ResponseID: "resp_done"},
	}}
	server := httptest.NewServer(NewServer(Options{Client: llmClient}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "hello")
	response, body := get(t, client, server.URL+turn.StreamURL)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	frames := parseSSE(t, body)
	done := decodeDoneFrame(t, frames[len(frames)-1])
	if done.HTML != "" {
		t.Fatalf("done HTML = %q, want empty body for summary-only output", done.HTML)
	}
	if len(done.Statuses) != 1 || done.Statuses[0].Kind != "summary" || done.Statuses[0].Text != "brief summary" {
		t.Fatalf("done statuses = %#v, want visible summary status", done.Statuses)
	}
}

func TestTurnRoutesReturnNotFound(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{Client: dummy.NewClient()}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)

	response, body := get(t, client, server.URL+"/chat/turns/")
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET malformed turn route status = %d, want 404; body = %q", response.StatusCode, body)
	}

	response, body = get(t, client, server.URL+"/chat/turns/missing/events")
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing turn events status = %d, want 404; body = %q", response.StatusCode, body)
	}

	request := newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/missing/abort", nil)
	request.Header.Set(csrfHeaderName, csrfToken)
	response, body = do(t, client, request)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("POST missing turn abort status = %d, want 404; body = %q", response.StatusCode, body)
	}

	response, body = get(t, client, server.URL+"/chat/turns/missing/unknown")
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET unknown turn action status = %d, want 404; body = %q", response.StatusCode, body)
	}
}

func TestTurnEventsRequiresFlusher(t *testing.T) {
	server := NewServer(Options{Client: dummy.NewClient()})
	turn, err := newTurnJob("hello")
	if err != nil {
		t.Fatalf("newTurnJob error = %v", err)
	}
	server.sessions["sess_test"] = &browserSession{
		id:    "sess_test",
		csrf:  "csrf_test",
		chat:  chat.NewService(dummy.NewClient()).NewSession(),
		turns: map[string]*turnJob{turn.id: turn},
	}

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/chat/turns/"+turn.id+"/events", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess_test"})
	writer := &nonFlushingHTTPWriter{header: http.Header{}}

	server.handleTurnEvents(writer, request, turn.id)

	if writer.status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", writer.status)
	}
	if !strings.Contains(writer.body.String(), "streaming_unsupported") {
		t.Fatalf("body = %q, want streaming_unsupported", writer.body.String())
	}
}

func TestTurnEventsStopsOnReplayAndUpdateWriteErrors(t *testing.T) {
	t.Run("replay write error", func(t *testing.T) {
		server := NewServer(Options{Client: dummy.NewClient()})
		turn := newTestTurnJob(t)
		turn.emit("html", htmlEvent{TurnID: turn.id, AssistantMessageID: turn.assistantMessageID, HTML: "hello"})
		server.sessions["sess_test"] = &browserSession{
			id:    "sess_test",
			csrf:  "csrf_test",
			chat:  chat.NewService(dummy.NewClient()).NewSession(),
			turns: map[string]*turnJob{turn.id: turn},
		}
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/chat/turns/"+turn.id+"/events", nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess_test"})
		writer := &failingHTTPWriter{header: http.Header{}, failAt: 0}

		server.handleTurnEvents(writer, request, turn.id)

		if writer.status != http.StatusOK {
			t.Fatalf("status = %d, want stream started", writer.status)
		}
	})

	t.Run("update write error", func(t *testing.T) {
		server := NewServer(Options{Client: dummy.NewClient()})
		turn := newTestTurnJob(t)
		server.sessions["sess_test"] = &browserSession{
			id:    "sess_test",
			csrf:  "csrf_test",
			chat:  chat.NewService(dummy.NewClient()).NewSession(),
			turns: map[string]*turnJob{turn.id: turn},
		}
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/chat/turns/"+turn.id+"/events", nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess_test"})
		writer := &failingHTTPWriter{header: http.Header{}, failAt: 0}
		done := make(chan struct{})

		go func() {
			server.handleTurnEvents(writer, request, turn.id)
			close(done)
		}()
		waitForSubscriber(t, turn)
		turn.emit("html", htmlEvent{TurnID: turn.id, AssistantMessageID: turn.assistantMessageID, HTML: "hello"})

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("handleTurnEvents did not return after update write failure")
		}
	})
}

func TestViewMessagesRendersStructuredAssistantParts(t *testing.T) {
	messages := viewMessages([]llm.Message{
		{
			Role: llm.RoleAssistant,
			Parts: []llm.Part{
				{Type: llm.PartReasoning, Text: "thinking"},
				{Type: llm.PartSummary, Text: "short summary"},
				{Type: llm.PartText, Text: "**answer**"},
				{Type: llm.PartError, Text: "model refused"},
				{
					Type:     llm.PartImage,
					URL:      "/assets/app.css",
					Filename: "plot.png",
					MimeType: "image/png",
					Alt:      "Plot",
					Width:    640,
					Height:   480,
				},
				{
					Type:     llm.PartAttachment,
					URL:      "/assets/app.css",
					Filename: "notes.txt",
					MimeType: "text/plain",
					Size:     42,
					Text:     "preview text",
				},
			},
		},
	}, NewServer(Options{Client: dummy.NewClient()}).markdown)

	if len(messages) != 1 || messages[0].Role != "assistant" {
		t.Fatalf("viewMessages = %#v, want visible assistant message", messages)
	}
	if len(messages[0].Statuses) != 2 {
		t.Fatalf("viewMessage statuses = %#v, want reasoning and summary statuses", messages[0].Statuses)
	}
	if messages[0].Statuses[0].Kind != "thinking" || !strings.Contains(messages[0].Statuses[0].Text, "thinking") {
		t.Fatalf("reasoning status = %#v, want thinking content", messages[0].Statuses[0])
	}
	if messages[0].Statuses[1].Kind != "summary" || !strings.Contains(messages[0].Statuses[1].Text, "short summary") {
		t.Fatalf("summary status = %#v, want summary content", messages[0].Statuses[1])
	}
	html := string(messages[0].HTML)
	for _, want := range []string{
		`<strong>answer</strong>`,
		`class="message-part-error"`,
		`model refused`,
		`class="message-image"`,
		`src="/assets/app.css"`,
		`alt="Plot"`,
		`class="message-attachment"`,
		`notes.txt`,
		`preview text`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("viewMessage HTML = %q, want structured part substring %q", html, want)
		}
	}
	if strings.Contains(html, `https://proxy.example`) {
		t.Fatalf("viewMessage HTML = %q, did not expect direct proxy URL", html)
	}
}

func TestViewMessagesKeepsVisibleNonTextMessages(t *testing.T) {
	messages := viewMessages([]llm.Message{
		{Role: llm.RoleAssistant, Parts: []llm.Part{{Type: llm.PartReasoning, Text: "visible thinking"}}},
		llm.NewTextMessage(llm.RoleUser, "visible"),
	}, NewServer(Options{Client: dummy.NewClient()}).markdown)

	if len(messages) != 2 {
		t.Fatalf("viewMessages = %#v, want reasoning-only assistant and user messages", messages)
	}
	if messages[0].Role != "assistant" || len(messages[0].Statuses) != 1 || messages[0].Statuses[0].Text != "visible thinking" {
		t.Fatalf("first viewMessage = %#v, want visible reasoning-only assistant", messages[0])
	}
	if messages[1].Role != "user" || messages[1].Text != "visible" {
		t.Fatalf("second viewMessage = %#v, want visible user message", messages[1])
	}
}

func TestSafeBFFURLRejectsUnservedChatFileRoutes(t *testing.T) {
	for _, raw := range []string{
		"/chat/attachments/file_1",
		"/chat/files/file_1",
		"/chat/images/image_1",
		"https://proxy.example/file_1",
	} {
		if got := safeBFFURL(raw); got != "" {
			t.Fatalf("safeBFFURL(%q) = %q, want empty for unserved or external URL", raw, got)
		}
	}
	if got := safeBFFURL("/assets/app.css"); got != "/assets/app.css" {
		t.Fatalf("safeBFFURL(/assets/app.css) = %q, want asset URL", got)
	}
}

func TestStreamHelpersHandleInvalidInputsAndWriterErrors(t *testing.T) {
	for _, value := range []string{"", "not-an-int", "-1"} {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events", nil)
		request.Header.Set("Last-Event-ID", value)
		if got := lastEventID(request); got != 0 {
			t.Fatalf("lastEventID(%q) = %d, want 0", value, got)
		}
	}

	name, data := encodeStreamEvent("bad", func() {})
	if name != "stream-error" || !strings.Contains(string(data), "could not encode") {
		t.Fatalf("encodeStreamEvent = %q %q, want stream-error fallback", name, data)
	}

	writer := &failingHTTPWriter{header: http.Header{}, failAt: 0}
	if err := writeSSE(writer, writer, streamEvent{ID: 1, Name: "html", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("writeSSE error = nil, want writer error")
	}

	writer = &failingHTTPWriter{header: http.Header{}, failAt: 1}
	if err := writeSSE(writer, writer, streamEvent{ID: 1, Name: "html", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("writeSSE event error = nil, want writer error")
	}

	writer = &failingHTTPWriter{header: http.Header{}, failAt: 1}
	if err := writeSSE(writer, writer, streamEvent{Name: "html", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("writeSSE data error = nil, want writer error")
	}
}

func TestTurnJobEmitsStreamErrorsAndIgnoresNilEventErrors(t *testing.T) {
	t.Run("stream start failure", func(t *testing.T) {
		turn := newTestTurnJob(t)
		session := chat.NewService(webSequenceClient{streamErr: io.ErrUnexpectedEOF}).NewSession()

		turn.run(session, chat.SendOptions{})

		replay, _, terminal := turn.subscribe(0)
		if !terminal || !hasReplayEvent(replay, "stream-error") {
			t.Fatalf("replay = %#v terminal=%v, want terminal stream-error", replay, terminal)
		}
	})

	t.Run("unexpected eof", func(t *testing.T) {
		turn := newTestTurnJob(t)
		session := chat.NewService(webSequenceClient{}).NewSession()

		turn.run(session, chat.SendOptions{})

		replay, _, terminal := turn.subscribe(0)
		if !terminal || !hasReplayEvent(replay, "stream-error") {
			t.Fatalf("replay = %#v terminal=%v, want terminal stream-error", replay, terminal)
		}
	})

	t.Run("nil event error falls through", func(t *testing.T) {
		turn := newTestTurnJob(t)
		session := chat.NewService(webSequenceClient{events: []llm.Event{
			{Type: llm.EventError},
			{Type: llm.EventCompleted, ResponseID: "resp_done"},
		}}).NewSession()

		turn.run(session, chat.SendOptions{})

		replay, _, terminal := turn.subscribe(0)
		if !terminal || !hasReplayEvent(replay, "done") {
			t.Fatalf("replay = %#v terminal=%v, want terminal done", replay, terminal)
		}
	})

	t.Run("event error with cause", func(t *testing.T) {
		turn := newTestTurnJob(t)
		session := chat.NewService(webSequenceClient{events: []llm.Event{
			{Type: llm.EventError, Err: errors.New("event failed")},
		}}).NewSession()

		turn.run(session, chat.SendOptions{})

		replay, _, terminal := turn.subscribe(0)
		if !terminal || !hasReplayEvent(replay, "stream-error") {
			t.Fatalf("replay = %#v terminal=%v, want terminal stream-error", replay, terminal)
		}
	})
}

func TestTurnJobFinishClosesSubscribersWithoutTerminalEvent(t *testing.T) {
	turn := newTestTurnJob(t)
	_, updates, terminal := turn.subscribe(0)
	if terminal {
		t.Fatalf("new turn is terminal")
	}

	turn.finish()

	if _, ok := <-updates; ok {
		t.Fatalf("subscriber channel is open, want closed")
	}
	if !turn.isTerminal() {
		t.Fatalf("turn is not terminal after finish")
	}

	turn.finish()
	if !turn.doneClosed {
		t.Fatalf("turn done is not marked closed after double finish")
	}
}

func TestTurnJobTerminalEmitNoOpsAndAbortContextDone(t *testing.T) {
	turn := newTestTurnJob(t)
	_, updates, terminal := turn.subscribe(0)
	if terminal {
		t.Fatalf("new turn is terminal")
	}
	for i := 0; i < cap(updates); i++ {
		updates <- streamEvent{}
	}
	turn.emitTerminal("done", doneEvent{TurnID: turn.id, AssistantMessageID: turn.assistantMessageID})
	turn.emit("html", htmlEvent{TurnID: turn.id, AssistantMessageID: turn.assistantMessageID, HTML: "ignored"})
	turn.emitTerminal("done", doneEvent{TurnID: turn.id, AssistantMessageID: turn.assistantMessageID})

	replay, _, terminal := turn.subscribe(0)
	if !terminal || len(replay) != 1 {
		t.Fatalf("replay = %#v terminal=%v, want one terminal event", replay, terminal)
	}

	turn = newTestTurnJob(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !turn.abort(ctx) {
		t.Fatalf("abort returned false, want true before terminal")
	}
	turn.finish()
}

type turnResponse struct {
	TurnID             string `json:"turn_id"`
	UserMessageID      string `json:"user_message_id"`
	AssistantMessageID string `json:"assistant_message_id"`
	StreamURL          string `json:"stream_url"`
}

type httpResult struct {
	response *http.Response
	body     string
}

func waitHTTPResult(t *testing.T, results <-chan httpResult) httpResult {
	t.Helper()

	select {
	case result := <-results:
		return result
	case <-time.After(time.Second):
		t.Fatalf("HTTP request did not finish")
		return httpResult{}
	}
}

func withRandomReader(t *testing.T, reader io.Reader) {
	t.Helper()

	original := randomReader
	randomReader = reader
	t.Cleanup(func() {
		randomReader = original
	})
}

func waitForSubscriber(t *testing.T, turn *turnJob) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		turn.mu.Lock()
		count := len(turn.subscribers)
		turn.mu.Unlock()
		if count > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscriber was not registered")
}

func testHTTPClient(t *testing.T) *http.Client {
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

func testNoRedirectHTTPClient(t *testing.T) *http.Client {
	t.Helper()

	client := testHTTPClient(t)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func newAuthTestHandler(t *testing.T, client llm.Client, registration, secureCookies bool) *Server {
	t.Helper()
	return newAuthTestHandlerForStore(t, newWebTestStore(t), client, registration, secureCookies)
}

func newAuthTestHandlerForStore(t *testing.T, store storage.Store, client llm.Client, registration, secureCookies bool) *Server {
	t.Helper()
	authService := auth.NewService(auth.Options{
		Store:      store,
		BCryptCost: bcrypt.MinCost,
	})
	return NewServer(Options{
		Client:              client,
		CookieSecure:        secureCookies,
		Store:               store,
		Auth:                authService,
		RegistrationEnabled: registration,
	})
}

func newWebTestStore(t *testing.T) storage.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pyttechat.db")
	store, err := storage.OpenSQLite(context.Background(), "sqlite://"+path)
	if err != nil {
		t.Fatalf("OpenSQLite error = %v, want nil", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close error = %v", err)
		}
	})
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate error = %v, want nil", err)
	}
	return store
}

func fetchAuthCSRFToken(t *testing.T, client *http.Client, baseURL, path string) string {
	t.Helper()

	response, body := get(t, client, baseURL+path)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200; body = %q", path, response.StatusCode, body)
	}
	token := csrfFromHTML(t, body)
	if token == "" {
		t.Fatalf("CSRF token is empty in body %q", body)
	}
	return token
}

func registerAuthUser(t *testing.T, client *http.Client, baseURL, username, password string) string {
	t.Helper()

	csrfToken := fetchAuthCSRFToken(t, client, baseURL, "/register")
	return submitAuthForm(t, client, baseURL+"/register", map[string]string{
		"csrf_token": csrfToken,
		"username":   username,
		"password":   password,
	})
}

func submitAuthForm(t *testing.T, client *http.Client, targetURL string, values map[string]string) string {
	t.Helper()

	request := newFormRequest(t, http.MethodPost, targetURL, values)
	response, body := do(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST %s final status = %d, want 200; body = %q", targetURL, response.StatusCode, body)
	}
	token := csrfFromHTML(t, body)
	if token == "" {
		t.Fatalf("CSRF token is empty after auth form; body = %q", body)
	}
	return token
}

func newFormRequest(t *testing.T, method, targetURL string, values map[string]string) *http.Request {
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
	request, err := http.NewRequestWithContext(context.Background(), method, url, &body)
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	return request
}

func get(t *testing.T, client *http.Client, url string) (*http.Response, string) {
	t.Helper()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest %s error = %v", url, err)
	}
	response, err := client.Do(request)
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

func hasEvent(frames []sseFrame, eventName string) bool {
	for _, frame := range frames {
		if frame.Event == eventName {
			return true
		}
	}
	return false
}

type testHTMLPayload struct {
	TurnID             string `json:"turn_id"`
	AssistantMessageID string `json:"assistant_message_id"`
	HTML               string `json:"html"`
}

type testDonePayload struct {
	TurnID             string       `json:"turn_id"`
	AssistantMessageID string       `json:"assistant_message_id"`
	ResponseID         string       `json:"response_id"`
	Usage              *llm.Usage   `json:"usage,omitempty"`
	HTML               string       `json:"html"`
	Statuses           []viewStatus `json:"statuses,omitempty"`
	CompletedAt        string       `json:"completed_at"`
}

func decodeHTMLFrame(t *testing.T, frame sseFrame) testHTMLPayload {
	t.Helper()

	if frame.Event != "html" {
		t.Fatalf("frame event = %q, want html: %#v", frame.Event, frame)
	}
	var payload testHTMLPayload
	if err := json.Unmarshal([]byte(frame.Data), &payload); err != nil {
		t.Fatalf("decode html frame error = %v; frame = %#v", err, frame)
	}
	if payload.TurnID == "" || payload.AssistantMessageID == "" {
		t.Fatalf("html payload = %#v, want ids", payload)
	}
	return payload
}

func decodePreviewFrame(t *testing.T, frame sseFrame) testHTMLPayload {
	t.Helper()

	if frame.Event != "preview" {
		t.Fatalf("frame event = %q, want preview: %#v", frame.Event, frame)
	}
	var payload testHTMLPayload
	if err := json.Unmarshal([]byte(frame.Data), &payload); err != nil {
		t.Fatalf("decode preview frame error = %v; frame = %#v", err, frame)
	}
	if payload.TurnID == "" || payload.AssistantMessageID == "" {
		t.Fatalf("preview payload = %#v, want ids", payload)
	}
	return payload
}

func decodeDoneFrame(t *testing.T, frame sseFrame) testDonePayload {
	t.Helper()

	if frame.Event != "done" {
		t.Fatalf("frame event = %q, want done: %#v", frame.Event, frame)
	}
	var payload testDonePayload
	if err := json.Unmarshal([]byte(frame.Data), &payload); err != nil {
		t.Fatalf("decode done frame error = %v; frame = %#v", err, frame)
	}
	if payload.TurnID == "" || payload.AssistantMessageID == "" {
		t.Fatalf("done payload = %#v, want ids", payload)
	}
	return payload
}

func withTimeNow(t *testing.T, clock func() time.Time) {
	t.Helper()

	original := timeNow
	timeNow = clock
	t.Cleanup(func() {
		timeNow = original
	})
}

func assertSafeRenderedHTML(t *testing.T, html string) {
	t.Helper()

	for _, unsafe := range []string{"<script", "alert(1)", "onclick", "javascript:"} {
		if strings.Contains(html, unsafe) {
			t.Fatalf("rendered HTML = %q, did not expect unsafe substring %q", html, unsafe)
		}
	}
}

func waitSSEFrame(t *testing.T, frames <-chan sseFrame) sseFrame {
	t.Helper()

	select {
	case frame := <-frames:
		return frame
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for SSE frame")
		return sseFrame{}
	}
}

func assertFrameEvents(t *testing.T, frames []sseFrame, want []string) {
	t.Helper()

	if len(frames) != len(want) {
		t.Fatalf("SSE event count = %d, want %d; frames = %#v", len(frames), len(want), frames)
	}
	for i, eventName := range want {
		if frames[i].Event != eventName {
			t.Fatalf("SSE event[%d] = %q, want %q; frames = %#v", i, frames[i].Event, eventName, frames)
		}
	}
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

type abortBlockingClient struct {
	mu         sync.Mutex
	streams    int
	started    chan struct{}
	cancelSeen chan struct{}
	release    chan struct{}
	startOnce  sync.Once
	cancelOnce sync.Once
}

func newAbortBlockingClient() *abortBlockingClient {
	return &abortBlockingClient{
		started:    make(chan struct{}),
		cancelSeen: make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (c *abortBlockingClient) Stream(ctx context.Context, _ llm.Request) (llm.Stream, error) {
	c.mu.Lock()
	c.streams++
	streamNumber := c.streams
	c.mu.Unlock()

	if streamNumber == 1 {
		c.startOnce.Do(func() {
			close(c.started)
		})
		return &abortBlockingStream{
			ctx:        ctx,
			cancelSeen: c.cancelSeen,
			release:    c.release,
			cancelOnce: &c.cancelOnce,
		}, nil
	}
	return &controlledStream{ctx: ctx, events: make(chan llm.Event)}, nil
}

func (c *abortBlockingClient) waitForStreamStart(t *testing.T) {
	t.Helper()

	select {
	case <-c.started:
	case <-time.After(time.Second):
		t.Fatalf("LLM stream was not started")
	}
}

func (c *abortBlockingClient) waitForCancel(t *testing.T) {
	t.Helper()

	select {
	case <-c.cancelSeen:
	case <-time.After(time.Second):
		t.Fatalf("LLM stream did not observe cancellation")
	}
}

func (c *abortBlockingClient) releaseCanceledStream() {
	select {
	case <-c.release:
	default:
		close(c.release)
	}
}

type abortBlockingStream struct {
	ctx        context.Context
	cancelSeen chan struct{}
	release    chan struct{}
	cancelOnce *sync.Once
}

func (s *abortBlockingStream) Next() (llm.Event, error) {
	<-s.ctx.Done()
	s.cancelOnce.Do(func() {
		close(s.cancelSeen)
	})
	<-s.release
	return llm.Event{}, s.ctx.Err()
}

func (*abortBlockingStream) Close() error {
	return nil
}

type closeBlockingClient struct {
	mu           sync.Mutex
	streams      int
	closeStarted chan struct{}
	closeRelease chan struct{}
	closeOnce    sync.Once
}

func newCloseBlockingClient() *closeBlockingClient {
	return &closeBlockingClient{
		closeStarted: make(chan struct{}),
		closeRelease: make(chan struct{}),
	}
}

func (c *closeBlockingClient) Stream(ctx context.Context, _ llm.Request) (llm.Stream, error) {
	c.mu.Lock()
	c.streams++
	streamNumber := c.streams
	c.mu.Unlock()

	if streamNumber == 1 {
		return &closeBlockingStream{
			ctx:          ctx,
			event:        llm.Event{Type: llm.EventCompleted, ResponseID: "resp_done"},
			closeStarted: c.closeStarted,
			closeRelease: c.closeRelease,
			closeOnce:    &c.closeOnce,
		}, nil
	}
	return &controlledStream{ctx: ctx, events: make(chan llm.Event)}, nil
}

func (c *closeBlockingClient) waitForBlockedClose(t *testing.T) {
	t.Helper()

	select {
	case <-c.closeStarted:
	case <-time.After(time.Second):
		t.Fatalf("first stream close did not block")
	}
}

func (c *closeBlockingClient) unblockClose() {
	select {
	case <-c.closeRelease:
	default:
		close(c.closeRelease)
	}
}

type closeBlockingStream struct {
	ctx          context.Context
	event        llm.Event
	sent         bool
	closeStarted chan struct{}
	closeRelease chan struct{}
	closeOnce    *sync.Once
}

func (s *closeBlockingStream) Next() (llm.Event, error) {
	select {
	case <-s.ctx.Done():
		return llm.Event{}, s.ctx.Err()
	default:
	}
	if s.sent {
		return llm.Event{}, io.EOF
	}
	s.sent = true
	return s.event, nil
}

func (s *closeBlockingStream) Close() error {
	s.closeOnce.Do(func() {
		close(s.closeStarted)
	})
	<-s.closeRelease
	return nil
}

type sequenceRandomReader struct {
	calls  int
	failAt int
}

func (r *sequenceRandomReader) Read(p []byte) (int, error) {
	r.calls++
	if r.failAt > 0 && r.calls >= r.failAt {
		return 0, errors.New("random failed")
	}
	for i := range p {
		p[i] = byte(r.calls)
	}
	return len(p), nil
}

type webErrAfterJSONBody struct {
	data []byte
	read bool
}

func (b *webErrAfterJSONBody) Read(p []byte) (int, error) {
	if !b.read {
		b.read = true
		return copy(p, b.data), nil
	}
	return 0, errors.New("trailing read failed")
}

func (*webErrAfterJSONBody) Close() error {
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

type nonFlushingHTTPWriter struct {
	header http.Header
	body   strings.Builder
	status int
}

func (w *nonFlushingHTTPWriter) Header() http.Header {
	return w.header
}

func (w *nonFlushingHTTPWriter) WriteHeader(status int) {
	w.status = status
}

func (w *nonFlushingHTTPWriter) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

type failingHTTPWriter struct {
	header http.Header
	body   strings.Builder
	failAt int
	writes int
	status int
}

func (w *failingHTTPWriter) Header() http.Header {
	return w.header
}

func (w *failingHTTPWriter) WriteHeader(status int) {
	w.status = status
}

func (w *failingHTTPWriter) Write(data []byte) (int, error) {
	if w.writes >= w.failAt {
		return 0, io.ErrClosedPipe
	}
	w.writes++
	return w.body.Write(data)
}

func (*failingHTTPWriter) Flush() {}

type webSequenceClient struct {
	events    []llm.Event
	streamErr error
}

func (c webSequenceClient) Stream(context.Context, llm.Request) (llm.Stream, error) {
	if c.streamErr != nil {
		return nil, c.streamErr
	}
	return &webSequenceStream{events: append([]llm.Event(nil), c.events...)}, nil
}

type webSequenceStream struct {
	events []llm.Event
	index  int
}

func (s *webSequenceStream) Next() (llm.Event, error) {
	if s.index >= len(s.events) {
		return llm.Event{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (*webSequenceStream) Close() error {
	return nil
}

func newTestTurnJob(t *testing.T) *turnJob {
	t.Helper()

	turn, err := newTurnJob("hello")
	if err != nil {
		t.Fatalf("newTurnJob error = %v", err)
	}
	return turn
}

func hasReplayEvent(events []streamEvent, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}
