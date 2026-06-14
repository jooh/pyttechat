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
		`title="Scroll to latest message"`,
		`id="composer-status"`,
		`<p id="composer-status" class="composer-status" aria-live="polite"></p>`,
		`id="composer-dock"`,
		`id="undo-button"`,
		`title="Undo prompt edit"`,
		`id="redo-button"`,
		`title="Redo prompt edit"`,
		`id="previous-button"`,
		`title="Previous prompt"`,
		`id="next-button"`,
		`title="Next prompt"`,
		`id="ffwd-button"`,
		`title="Latest prompt"`,
		`id="composer-action"`,
		`title="Send message"`,
		`data-action-state="send"`,
		`data-action-icon="play"`,
		`data-action-icon="stop"`,
		`id="composer-end-target"`,
		`class="message message-user message-end-target"`,
		`data-composer-end-target`,
		`data-end-prompt="true"`,
		`aria-label="Undo prompt edit"`,
		`aria-label="Redo prompt edit"`,
		`aria-label="Previous prompt"`,
		`aria-label="Next prompt"`,
		`aria-label="Latest prompt"`,
		`aria-label="Send message"`,
		`id="theme-toggle"`,
		`data-theme-toggle`,
		`aria-label="Current theme: system preference"`,
		`title="Switch theme"`,
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

func TestAuthOptionAcceptsInterfaceImplementation(t *testing.T) {
	store := newWebTestStore(t)
	authService := &webAuthAdapter{Service: auth.NewService(auth.Options{
		Store:      store,
		BCryptCost: bcrypt.MinCost,
	})}

	server := NewServer(Options{
		Client:              dummy.NewClient(),
		Store:               store,
		Auth:                authService,
		RegistrationEnabled: true,
	})

	if !server.authEnabled() {
		t.Fatalf("server auth is disabled, want interface-backed auth to enable protected routes")
	}
}

func TestRegistrationDisabledHidesAndRejectsRegistration(t *testing.T) {
	handler := newAuthTestHandler(t, dummy.NewClient(), false, false)
	server := httptest.NewServer(handler)
	defer server.Close()

	client := testHTTPClient(t)
	response, body := get(t, client, server.URL+"/login")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if strings.Contains(body, "/register") || strings.Contains(body, "Create account") {
		t.Fatalf("GET /login body = %q, did not expect registration link when disabled", body)
	}

	response, body = get(t, client, server.URL+"/register")
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /register status = %d, want 404 when registration is disabled; body = %q", response.StatusCode, body)
	}

	request := newFormRequest(t, http.MethodPost, server.URL+"/register", map[string]string{
		"csrf_token": "ignored",
		"username":   "alice",
		"password":   "correct horse",
	})
	response, body = do(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /register status = %d, want 404 when registration is disabled; body = %q", response.StatusCode, body)
	}
}

func TestAuthDisabledRoutesReturnNotFoundAndAuthenticatedFormsRedirect(t *testing.T) {
	authDisabled := NewServer(Options{Client: dummy.NewClient()})
	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/login"},
		{method: http.MethodPost, path: "/login"},
		{method: http.MethodGet, path: "/register"},
		{method: http.MethodPost, path: "/register"},
		{method: http.MethodPost, path: "/logout"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, nil)
			recorder := httptest.NewRecorder()

			authDisabled.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusNotFound {
				t.Fatalf("%s %s status = %d, want 404; body = %q", tc.method, tc.path, recorder.Code, recorder.Body.String())
			}
		})
	}

	handler := newAuthTestHandler(t, dummy.NewClient(), true, false)
	server := httptest.NewServer(handler)
	defer server.Close()

	client := testNoRedirectHTTPClient(t)
	registerCSRF := fetchAuthCSRFToken(t, client, server.URL, "/register")
	request := newFormRequest(t, http.MethodPost, server.URL+"/register", map[string]string{
		"csrf_token": registerCSRF,
		"username":   "alice",
		"password":   "correct horse",
	})
	response, body := do(t, client, request)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/" {
		t.Fatalf("POST /register status = %d location = %q, want 303 /; body = %q", response.StatusCode, response.Header.Get("Location"), body)
	}

	for _, path := range []string{"/login", "/register"} {
		response, body = get(t, client, server.URL+path)
		response.Body.Close()
		if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/" {
			t.Fatalf("GET %s status = %d location = %q, want 303 /; body = %q", path, response.StatusCode, response.Header.Get("Location"), body)
		}
	}
}

func TestAuthFormsRenderValidationAndParseErrors(t *testing.T) {
	store := newWebTestStore(t)
	authService := auth.NewService(auth.Options{
		Store:      store,
		BCryptCost: bcrypt.MinCost,
	})
	if _, err := authService.Register(context.Background(), "taken", "correct horse"); err != nil {
		t.Fatalf("precreate user error = %v, want nil", err)
	}
	handler := newAuthTestHandlerForStore(t, store, dummy.NewClient(), true, false)
	server := httptest.NewServer(handler)
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchAuthCSRFToken(t, client, server.URL, "/register")
	for _, tc := range []struct {
		name     string
		username string
		password string
		want     string
	}{
		{name: "invalid username", username: "!!", password: "correct horse", want: "Use 3-32 letters"},
		{name: "weak password", username: "shortpass", password: "short", want: "Password must be at least 8 characters."},
		{name: "username taken", username: "taken", password: "correct horse", want: "Username is already taken."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := newFormRequest(t, http.MethodPost, server.URL+"/register", map[string]string{
				"csrf_token": csrfToken,
				"username":   tc.username,
				"password":   tc.password,
			})
			response, body := do(t, client, request)
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("POST /register status = %d, want 400; body = %q", response.StatusCode, body)
			}
			if !strings.Contains(body, tc.want) {
				t.Fatalf("POST /register body = %q, want validation message %q", body, tc.want)
			}
			if token := csrfFromHTML(t, body); token != "" {
				csrfToken = token
			}
		})
	}

	loginCSRF := fetchAuthCSRFToken(t, client, server.URL, "/login")
	request := newFormRequest(t, http.MethodPost, server.URL+"/login", map[string]string{
		"csrf_token": loginCSRF,
		"username":   "taken",
		"password":   "wrong password",
	})
	response, body := do(t, client, request)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "Invalid username or password.") {
		t.Fatalf("POST /login status = %d body = %q, want invalid credentials page", response.StatusCode, body)
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "login", path: "/login"},
		{name: "register", path: "/register"},
	} {
		t.Run(tc.name+" parse error", func(t *testing.T) {
			token := fetchAuthCSRFToken(t, client, server.URL, tc.path)
			request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+tc.path, strings.NewReader("%"))
			if err != nil {
				t.Fatalf("NewRequest error = %v", err)
			}
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set(csrfHeaderName, token)
			response, body := do(t, client, request)
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest || !strings.Contains(body, "invalid form") {
				t.Fatalf("POST %s status = %d body = %q, want invalid form", tc.path, response.StatusCode, body)
			}
		})
	}
}

func TestLoginRejectsInvalidCSRF(t *testing.T) {
	server := httptest.NewServer(newAuthTestHandler(t, dummy.NewClient(), true, false))
	defer server.Close()
	client := testHTTPClient(t)

	response, body := do(t, client, newFormRequest(t, http.MethodPost, server.URL+"/login", map[string]string{
		"csrf_token": "wrong",
		"username":   "alice",
		"password":   "correct horse",
	}))
	defer response.Body.Close()

	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /login status = %d, want 403; body = %q", response.StatusCode, body)
	}
}

func TestAuthSessionExpiryInvalidCookieAndProtectedAPIs(t *testing.T) {
	store := newWebTestStore(t)
	now := time.Now().UTC().Add(time.Hour)
	authService := auth.NewService(auth.Options{
		Store:      store,
		Now:        func() time.Time { return now },
		SessionTTL: time.Second,
		BCryptCost: bcrypt.MinCost,
	})
	handler := NewServer(Options{
		Client:              dummy.NewClient(),
		Store:               store,
		Auth:                authService,
		RegistrationEnabled: true,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := testNoRedirectHTTPClient(t)
	response, body := get(t, client, server.URL+"/login")
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200; body = %q", response.StatusCode, body)
	}

	now = now.Add(2 * time.Second)
	response, body = get(t, client, server.URL+"/")
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/login" {
		t.Fatalf("GET / with expired session status = %d location = %q, want 303 /login; body = %q", response.StatusCode, response.Header.Get("Location"), body)
	}
	if len(response.Cookies()) != 1 || response.Cookies()[0].Value != "" || response.Cookies()[0].MaxAge >= 0 {
		t.Fatalf("expired session Set-Cookie = %#v, want clearing cookie", response.Cookies())
	}

	replacementClient := testHTTPClient(t)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/login", nil)
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "malformed"})
	response, body = do(t, replacementClient, request)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /login malformed cookie status = %d, want 200; body = %q", response.StatusCode, body)
	}
	if len(response.Cookies()) != 1 || response.Cookies()[0].Value == "" || response.Cookies()[0].Value == "malformed" {
		t.Fatalf("malformed cookie replacement = %#v, want new session cookie", response.Cookies())
	}

	for _, tc := range []struct {
		method string
		path   string
		body   io.Reader
	}{
		{method: http.MethodPost, path: "/chat/turns", body: strings.NewReader(`{"prompt":"hello"}`)},
		{method: http.MethodGet, path: "/chat/turns/missing/events"},
		{method: http.MethodPost, path: "/chat/turns/missing/abort"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, tc.body)
			if tc.body != nil {
				request.Header.Set("Content-Type", "application/json")
			}
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s status = %d, want 401; body = %q", tc.method, tc.path, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAuthRoutesSurfaceAuthServiceFailures(t *testing.T) {
	store := newWebTestStore(t)

	t.Run("anonymous session creation failure", func(t *testing.T) {
		handler := NewServer(Options{
			Client:              dummy.NewClient(),
			Store:               store,
			Auth:                &webFailingAuth{createAnonymousErr: errors.New("anonymous failed")},
			RegistrationEnabled: true,
		})
		for _, tc := range []struct {
			method string
			path   string
		}{
			{method: http.MethodGet, path: "/login"},
			{method: http.MethodPost, path: "/login"},
			{method: http.MethodGet, path: "/register"},
			{method: http.MethodPost, path: "/register"},
			{method: http.MethodPost, path: "/logout"},
		} {
			request := httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, nil)
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("%s %s status = %d, want 500; body = %q", tc.method, tc.path, recorder.Code, recorder.Body.String())
			}
		}
	})

	t.Run("cookie verify failure on public route", func(t *testing.T) {
		handler := NewServer(Options{
			Client: dummy.NewClient(),
			Store:  store,
			Auth:   &webFailingAuth{verifyErr: errors.New("verify failed")},
		})
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/login", nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess.secret"})
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("GET /login status = %d, want 500; body = %q", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("cookie verify failure on protected route", func(t *testing.T) {
		handler := NewServer(Options{
			Client: dummy.NewClient(),
			Store:  store,
			Auth:   &webFailingAuth{verifyErr: errors.New("verify failed")},
		})
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess.secret"})
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("GET / status = %d, want 500; body = %q", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("stored user session hydration failure", func(t *testing.T) {
		handler := NewServer(Options{
			Client: dummy.NewClient(),
			Store:  store,
			Auth: &webFailingAuth{verified: storage.Session{
				ID:        "sess",
				UserID:    999,
				CSRFToken: "csrf",
				ExpiresAt: time.Now().Add(time.Hour),
			}},
		})
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess.secret"})
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("GET / status = %d, want 500 from stored session hydration; body = %q", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("login rotation failure", func(t *testing.T) {
		handler := NewServer(Options{
			Client: dummy.NewClient(),
			Store:  store,
			Auth: &webFailingAuth{
				anonymous: storage.Session{ID: "anon", CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)},
				user:      storage.User{ID: 1, Username: "alice"},
				rotateErr: errors.New("rotate failed"),
			},
		})
		request := newFormRequest(t, http.MethodPost, "/login", map[string]string{
			"csrf_token": "csrf",
			"username":   "alice",
			"password":   "correct horse",
		})
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("POST /login status = %d, want 500; body = %q", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("register rotation failure", func(t *testing.T) {
		handler := NewServer(Options{
			Client:              dummy.NewClient(),
			Store:               store,
			RegistrationEnabled: true,
			Auth: &webFailingAuth{
				anonymous: storage.Session{ID: "anon", CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)},
				user:      storage.User{ID: 1, Username: "alice"},
				rotateErr: errors.New("rotate failed"),
			},
		})
		request := newFormRequest(t, http.MethodPost, "/register", map[string]string{
			"csrf_token": "csrf",
			"username":   "alice",
			"password":   "correct horse",
		})
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("POST /register status = %d, want 500; body = %q", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("logout failure", func(t *testing.T) {
		handler := NewServer(Options{
			Client: dummy.NewClient(),
			Store:  store,
			Auth: &webFailingAuth{
				verified:  storage.Session{ID: "sess", CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)},
				logoutErr: errors.New("logout failed"),
			},
		})
		request := newFormRequest(t, http.MethodPost, "/logout", map[string]string{"csrf_token": "csrf"})
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess.secret"})
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("POST /logout status = %d, want 500; body = %q", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("auth template failure", func(t *testing.T) {
		handler := NewServer(Options{Client: dummy.NewClient()})
		handler.template = template.Must(template.New("auth.html").Parse(`{{template "missing" .}}`))
		recorder := httptest.NewRecorder()

		handler.renderAuthPage(recorder, http.StatusOK, authPageData{Title: "Sign in"})

		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "template error") {
			t.Fatalf("renderAuthPage status = %d body = %q, want original status with template error body", recorder.Code, recorder.Body.String())
		}
	})
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
	if !strings.Contains(body, `aria-label="Sign out" title="Sign out"`) {
		t.Fatalf("GET / after login body = %q, want sign out tooltip", body)
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

func TestSessionHelpersCoverLegacyAnonymousAndPersistedLoadFailures(t *testing.T) {
	t.Run("public session falls back to legacy when auth disabled", func(t *testing.T) {
		handler := NewServer(Options{Client: dummy.NewClient()})
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		recorder := httptest.NewRecorder()

		session, err := handler.publicSession(recorder, request)
		if err != nil {
			t.Fatalf("publicSession error = %v, want nil", err)
		}
		if session == nil || session.id == "" {
			t.Fatalf("session = %#v, want legacy browser session", session)
		}
	})

	t.Run("authenticated session rejects anonymous stored session", func(t *testing.T) {
		handler := newAuthTestHandler(t, dummy.NewClient(), true, false)
		publicRecorder := httptest.NewRecorder()
		publicRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		if _, err := handler.publicSession(publicRecorder, publicRequest); err != nil {
			t.Fatalf("publicSession error = %v, want nil", err)
		}
		cookies := publicRecorder.Result().Cookies()
		if len(cookies) == 0 {
			t.Fatalf("publicSession did not set a cookie")
		}

		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		request.AddCookie(cookies[0])
		_, err := handler.authenticatedSession(httptest.NewRecorder(), request)
		if !errors.Is(err, auth.ErrInvalidSession) {
			t.Fatalf("authenticatedSession error = %v, want invalid session", err)
		}
	})

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
		`.message-edit-slot`,
		`.message-assistant:hover .message-actions`,
		`pointer-events: none;`,
		`padding-right: 2.75rem;`,
		`.action-button`,
		`.history-button`,
		`.composer-dock .composer-box`,
		`.composer-dock[data-end-active="true"] .composer-box`,
		`.message-end-target`,
		`.message-end-target:focus-visible`,
		`.messages[data-dirty-prompt="true"] .message-end-target`,
		`padding: 1.25rem 0.75rem 1.5rem;`,
		`padding: 0 0.75rem 1rem;`,
		`0 0 18px`,
		`0 8px 24px`,
		`0 2px 8px`,
		`.message-user[data-editing="true"]`,
		`.message-user[data-active-prompt="true"]`,
		`position: sticky;`,
		`top: 0.25rem;`,
		`bottom: 0.25rem;`,
		`.message[data-after-active-prompt="true"]`,
		`.messages[data-dirty-prompt="true"] .message[data-after-active-prompt="true"]`,
		`opacity: 0.56;`,
		`--icon-button-inverse-color:`,
		`--icon-button-inverse-hover-color:`,
		`.message-user .composer-box`,
		`border: 0;`,
		`.composer-status:empty`,
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
		`min-height: 1.75rem;`,
		`.message-user .message-action`,
		`.message-user .message-actions`,
		`.dirty-dialog`,
		`.composer-end-target`,
		`.composer-dock[data-composer-detached="true"] .composer-end-target`,
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
		`replace_from`,
		`markTurnStopped`,
		`requestNavigation`,
		`updatePromptHistoryState`,
		`data.activePrompt = 'true'`,
		`data.afterActivePrompt = 'true'`,
		`messageData.dirtyPrompt = 'true'`,
		`data.endActive = 'true'`,
		`previousButton`,
		`nextButton`,
		`ffwdButton`,
		`requestNavigation(null, 'end')`,
		`requestNavigation(next ? messageIndex(next) : null, 'start')`,
		`nextButton.disabled = busy || dirtySelectedPrompt || currentEditIndex === null`,
		`handleEditablePromptClick`,
		`requestNavigation(index, 'end')`,
		`handleHistoryShortcut`,
		`isUndoShortcut`,
		`isRedoShortcut`,
		`recordPromptHistory`,
		`applyPromptHistoryStep`,
		`canUndoPromptEdit`,
		`canSelectPrompt`,
		`assistantOutputStarted`,
		`completeThinkingStatus(assistant.article);`,
		`ArrowUp`,
		`ArrowDown`,
		`data-action-icon`,
		`toggleAttribute('hidden'`,
		`Stop response`,
		`Stopping response`,
		`focusPrompt('end')`,
		`prompt.focus({ preventScroll: true })`,
		`composerEndTarget.addEventListener('keydown'`,
		`createMessageActions(role)`,
		`role !== 'assistant'`,
		`copy.title = 'Copy message'`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("app JS = %q, want streaming UI behavior %q", body, want)
		}
	}
	for _, unwanted := range []string{
		`Generating response`,
		`Response complete`,
		`Response stopped`,
		`Resume response`,
		`resumeTurn`,
		`/resume`,
		`article.append(createThinkingStatus());`,
		`dirtyDialog`,
		`showModal`,
		`window.confirm`,
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("app JS = %q, did not expect redundant composer status %q", body, unwanted)
		}
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

func TestCreateTurnRejectsInvalidReplaceFrom(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{Client: dummy.NewClient(dummy.Turn{TextChunks: []string{"answer"}})}))
	defer server.Close()

	client := testHTTPClient(t)
	csrfToken := fetchCSRFToken(t, client, server.URL)
	turn := createTurn(t, client, server.URL, csrfToken, "first")
	response, body := get(t, client, server.URL+turn.StreamURL)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	for _, replaceFrom := range []int{-1, 1, 3} {
		request := newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns", map[string]any{
			"prompt":       "replacement",
			"replace_from": replaceFrom,
		})
		request.Header.Set(csrfHeaderName, csrfToken)
		response, body := do(t, client, request)
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("replace_from=%d status = %d, want 400; body = %q", replaceFrom, response.StatusCode, body)
		}
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

func TestAbortedTurnPersistsPartialOutputForFollowUp(t *testing.T) {
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

	select {
	case llmClient.events <- llm.Event{Type: llm.EventTextDelta, Delta: "partial"}:
	case <-time.After(time.Second):
		t.Fatalf("timed out sending partial event")
	}
	reader := bufio.NewReader(response.Body)
	frame := readSSEFrame(t, reader)
	if frame.Event != "preview" || !strings.Contains(string(frame.Data), "partial") {
		t.Fatalf("first frame = %#v, want partial preview", frame)
	}

	request = newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+turn.TurnID+"/abort", nil)
	request.Header.Set(csrfHeaderName, csrfToken)
	abortResponse, abortBody := do(t, client, request)
	defer abortResponse.Body.Close()
	if abortResponse.StatusCode != http.StatusOK {
		t.Fatalf("abort status = %d, want 200; body = %q", abortResponse.StatusCode, abortBody)
	}
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll remaining events error = %v", err)
	}
	if !hasFrame(parseSSE(t, string(remaining)), "aborted", `"turn_id":"`+turn.TurnID+`"`) {
		t.Fatalf("remaining SSE body = %q, want aborted event", remaining)
	}

	next := createTurn(t, client, server.URL, csrfToken, "follow up")
	_ = llmClient.waitForContext(t)
	request = newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/"+next.TurnID+"/abort", nil)
	request.Header.Set(csrfHeaderName, csrfToken)
	cleanupResponse, cleanupBody := do(t, client, request)
	defer cleanupResponse.Body.Close()
	if cleanupResponse.StatusCode != http.StatusOK {
		t.Fatalf("cleanup abort status = %d, want 200; body = %q", cleanupResponse.StatusCode, cleanupBody)
	}

	requests := llmClient.Requests()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if got := requests[1].Messages; len(got) != 3 || got[0].Text() != "hello" || got[1].Text() != "partial" || got[2].Text() != "follow up" {
		t.Fatalf("follow-up request messages = %#v, want stopped partial turn in context", requests[1].Messages)
	}
}

func TestResumeRouteIsNotSupported(t *testing.T) {
	server := httptest.NewServer(NewServer(Options{Client: dummy.NewClient()}))
	defer server.Close()

	client := testHTTPClient(t)
	request := newJSONRequest(t, http.MethodPost, server.URL+"/chat/turns/turn_missing/resume", nil)
	response, body := do(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("POST resume status = %d, want 404; body = %q", response.StatusCode, body)
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

func TestReplaceFromTruncatesConversationContextForFollowUp(t *testing.T) {
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

	replacement := createTurnWithPayload(t, client, server.URL, csrfToken, map[string]any{
		"prompt":       "edited second",
		"replace_from": 2,
	})
	response, body = get(t, client, server.URL+replacement.StreamURL)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("replacement events status = %d, want 200; body = %q", response.StatusCode, body)
	}

	requests := llmClient.Requests()
	if len(requests) != 3 {
		t.Fatalf("request count = %d, want 3", len(requests))
	}
	if got := requests[2].Messages; len(got) != 3 || got[0].Text() != "first" || got[1].Text() != "answer 1" || got[2].Text() != "edited second" {
		t.Fatalf("replacement request messages = %#v, want first turn plus edited prompt", requests[2].Messages)
	}
}

func TestIndexRendersCompletedMessagesAndReusesSessionCookie(t *testing.T) {
	completedAt := time.Date(2026, 6, 14, 20, 16, 13, 0, time.UTC)
	withTimeNow(t, func() time.Time { return completedAt })
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
		`data-message-index="0"`,
		`data-editable-prompt="true"`,
		`aria-label="Copy message"`,
		`title="Copy message"`,
		`class="message-completed-at"`,
		`datetime="` + completedAt.Format(time.RFC3339) + `"`,
		`Completed`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET / body = %q, want completed message affordance %q", body, want)
		}
	}
	if count := strings.Count(body, `data-copy-message`); count != 1 {
		t.Fatalf("GET / body contains %d copy buttons, want assistant-only copy button; body = %q", count, body)
	}
	userIndex := strings.Index(body, `message-user`)
	assistantIndex := strings.Index(body, `message-assistant`)
	copyIndex := strings.Index(body, `data-copy-message`)
	if userIndex < 0 || assistantIndex <= userIndex || copyIndex < assistantIndex {
		t.Fatalf("GET / body = %q, want copy button associated with assistant message only", body)
	}
	if strings.Contains(body[userIndex:assistantIndex], `data-copy-message`) {
		t.Fatalf("GET / body = %q, did not expect copy button in user message", body)
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

func TestIndexKeepsNextMessageIndexForHiddenEmptyAssistant(t *testing.T) {
	handler := NewServer(Options{Client: dummy.NewClient()})
	chatSession := chat.NewService(dummy.NewClient()).NewSession()
	if err := chatSession.CommitStopped(context.Background(), "stopped", chat.SendOptions{}); err != nil {
		t.Fatalf("CommitStopped error = %v, want nil", err)
	}
	handler.sessions["sess_test"] = &browserSession{
		id:    "sess_test",
		csrf:  "csrf_test",
		chat:  chatSession,
		turns: map[string]*turnJob{},
	}

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "sess_test"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200; body = %q", recorder.Code, body)
	}
	if !strings.Contains(body, `data-next-message-index="2"`) {
		t.Fatalf("GET / body = %q, want next message index to include hidden empty assistant", body)
	}
	if !strings.Contains(body, `data-message-index="0"`) || strings.Contains(body, `data-message-index="1"`) {
		t.Fatalf("GET / body = %q, want only visible user message indexed while preserving next index", body)
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

func TestViewMessagesFallsBackWhenRendererFails(t *testing.T) {
	views := viewMessages([]llm.Message{
		{
			Role: llm.RoleAssistant,
			Parts: []llm.Part{
				{Type: llm.PartText, Text: "<unsafe>"},
			},
		},
	}, errorRenderer{})

	if len(views) != 1 {
		t.Fatalf("view count = %d, want 1", len(views))
	}
	if !strings.Contains(string(views[0].HTML), "&lt;unsafe&gt;") {
		t.Fatalf("HTML = %q, want escaped fallback text", views[0].HTML)
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

func TestRenderingHelpersCoverBranchVariants(t *testing.T) {
	if got := renderErrorPart(llm.Part{}); got != "" {
		t.Fatalf("renderErrorPart empty = %q, want empty", got)
	}
	image := renderImagePart(llm.Part{URL: "/assets/app.css", Filename: "plot.png"})
	if !strings.Contains(image, `alt="plot.png"`) || strings.Contains(image, `width="`) || strings.Contains(image, `height="`) {
		t.Fatalf("renderImagePart filename fallback = %q, want filename alt without dimensions", image)
	}
	if got := renderImagePart(llm.Part{URL: "https://proxy.example/image.png"}); got != "" {
		t.Fatalf("renderImagePart unsafe URL = %q, want empty", got)
	}

	attachment := renderAttachmentPart(llm.Part{URL: "bad url", Text: ""})
	if !strings.Contains(attachment, `<span class="message-attachment">`) || !strings.Contains(attachment, `attachment`) || strings.Contains(attachment, `<a `) {
		t.Fatalf("renderAttachmentPart without safe URL/name = %q, want span fallback", attachment)
	}
	if got := attachmentMeta(llm.Part{MimeType: "text/plain", Size: 2048}); got != "text/plain - 2.0 KB" {
		t.Fatalf("attachmentMeta KB = %q, want text/plain - 2.0 KB", got)
	}
	if got := formatByteSize(7); got != "7 B" {
		t.Fatalf("formatByteSize bytes = %q, want 7 B", got)
	}
	if got := formatByteSize(2 * 1024 * 1024); got != "2.0 MB" {
		t.Fatalf("formatByteSize MB = %q, want 2.0 MB", got)
	}
	if got := formatByteSize(0); got != "" {
		t.Fatalf("formatByteSize zero = %q, want empty", got)
	}

	for _, raw := range []string{"", "/assets/app.css bad", `/assets/app.css"`, "/other/app.css"} {
		if got := safeBFFURL(raw); got != "" {
			t.Fatalf("safeBFFURL(%q) = %q, want empty", raw, got)
		}
	}
	if got := messageRoleLabel(llm.RoleAssistant); got != "Assistant" {
		t.Fatalf("messageRoleLabel assistant = %q, want Assistant", got)
	}
	if got := messageRoleLabel(llm.RoleSystem); got != "system" {
		t.Fatalf("messageRoleLabel system = %q, want raw role", got)
	}

	var parts []llm.Part
	appendOutputDelta(&parts, llm.PartText, "")
	if len(parts) != 0 {
		t.Fatalf("appendOutputDelta empty = %#v, want no parts", parts)
	}
	appendOutputDelta(&parts, llm.PartText, "hel")
	appendOutputDelta(&parts, llm.PartText, "lo")
	appendOutputDelta(&parts, llm.PartReasoning, "thinking")
	if len(parts) != 2 || parts[0].Text != "hello" || parts[1].Text != "thinking" {
		t.Fatalf("appendOutputDelta parts = %#v, want merged text then reasoning", parts)
	}
	mergeCompletedOutputPart(&parts, llm.Part{})
	mergeCompletedOutputPart(&parts, llm.Part{
		Type:             llm.PartReasoning,
		ID:               "rs_1",
		Text:             "final thinking",
		Summary:          []string{"summary"},
		EncryptedContent: "encrypted",
	})
	if parts[1].ID != "rs_1" || parts[1].Text != "final thinking" || len(parts[1].Summary) != 1 || parts[1].EncryptedContent != "encrypted" {
		t.Fatalf("mergeCompletedOutputPart reasoning = %#v, want metadata merged into existing reasoning", parts[1])
	}
	textOnly := []llm.Part{{Type: llm.PartText, Text: "hello"}}
	mergeCompletedOutputPart(&textOnly, llm.Part{Type: llm.PartText, Text: "hello world"})
	if len(textOnly) != 1 || textOnly[0].Text != "hello world" {
		t.Fatalf("mergeCompletedOutputPart text prefix = %#v, want text upgraded in place", textOnly)
	}
	before := len(textOnly)
	mergeCompletedOutputPart(&textOnly, llm.Part{Type: llm.PartText, Text: "replacement"})
	if len(textOnly) != before+1 || textOnly[len(textOnly)-1].Text != "replacement" {
		t.Fatalf("mergeCompletedOutputPart text replacement = %#v, want appended replacement", textOnly)
	}
	var reasoningOnly []llm.Part
	mergeCompletedOutputPart(&reasoningOnly, llm.Part{Type: llm.PartReasoning, Summary: []string{"late summary"}})
	if len(reasoningOnly) != 1 || len(reasoningOnly[0].Summary) != 1 {
		t.Fatalf("mergeCompletedOutputPart missing reasoning = %#v, want appended reasoning", reasoningOnly)
	}

	escaped := string(escapedPlainTextHTML("<a>\r\nb\rc"))
	if escaped != "&lt;a&gt;<br>\nb<br>\nc" {
		t.Fatalf("escapedPlainTextHTML = %q, want escaped line breaks", escaped)
	}
	html, err := renderAssistantBody([]llm.Part{{Type: llm.PartText, Text: "   "}}, NewServer(Options{Client: dummy.NewClient()}).markdown)
	if err != nil || html != "" {
		t.Fatalf("renderAssistantBody whitespace = %q, %v; want empty nil", html, err)
	}
	statuses := assistantStatuses([]llm.Part{
		{Type: llm.PartReasoning, Summary: []string{"from summary"}},
		{Type: llm.PartReasoning, Text: " "},
		{Type: llm.PartSummary, Text: " "},
	}, 2)
	if len(statuses) != 1 || statuses[0].Text != "from summary" || statuses[0].ContentID == "" {
		t.Fatalf("assistantStatuses = %#v, want one summary-backed reasoning status", statuses)
	}
	if got := statusContentID(-1, 2); got != "" {
		t.Fatalf("statusContentID negative = %q, want empty", got)
	}
	views := viewMessages([]llm.Message{{Role: llm.RoleAssistant}}, NewServer(Options{Client: dummy.NewClient()}).markdown)
	if len(views) != 0 {
		t.Fatalf("viewMessages empty assistant = %#v, want skipped", views)
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

func TestTurnJobEmitErrorMapsCancellationToAborted(t *testing.T) {
	turn := newTestTurnJob(t)
	turn.emitError(context.Canceled)

	replay, _, terminal := turn.subscribe(0)
	if !terminal || !hasReplayEvent(replay, "aborted") || hasReplayEvent(replay, "stream-error") {
		t.Fatalf("replay = %#v terminal=%v, want aborted without stream-error", replay, terminal)
	}
}

func TestTurnJobIgnoresEmptyTextDeltas(t *testing.T) {
	turn := newTestTurnJob(t)
	session := chat.NewService(webSequenceClient{events: []llm.Event{
		{Type: llm.EventTextDelta, Delta: ""},
		{Type: llm.EventCompleted},
	}}).NewSession()

	turn.run(session, chat.SendOptions{})

	replay, _, terminal := turn.subscribe(0)
	if !terminal || hasReplayEvent(replay, "preview") || !hasReplayEvent(replay, "done") {
		t.Fatalf("replay = %#v terminal=%v, want only terminal done event", replay, terminal)
	}
}

func TestMergeCompletedOutputPartSkipsTrailingNonReasoningParts(t *testing.T) {
	parts := []llm.Part{
		{Type: llm.PartReasoning, Text: "old"},
		{Type: llm.PartText, Text: "answer"},
	}

	mergeCompletedOutputPart(&parts, llm.Part{Type: llm.PartReasoning, Text: "new"})

	if parts[0].Text != "new" || parts[1].Text != "answer" {
		t.Fatalf("parts = %#v, want reasoning updated before trailing text", parts)
	}
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

	return createTurnWithPayload(t, client, baseURL, csrfToken, map[string]any{
		"prompt": prompt,
	})
}

func createTurnWithPayload(t *testing.T, client *http.Client, baseURL, csrfToken string, payload map[string]any) turnResponse {
	t.Helper()

	request := newJSONRequest(t, http.MethodPost, baseURL+"/chat/turns", payload)
	request.Header.Set(csrfHeaderName, csrfToken)
	response, body := do(t, client, request)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /chat/turns status = %d, want 201; body = %q", response.StatusCode, body)
	}

	var turn turnResponse
	if err := json.Unmarshal([]byte(body), &turn); err != nil {
		t.Fatalf("decode turn response error = %v; body = %q", err, body)
	}
	if turn.TurnID == "" || turn.UserMessageID == "" || turn.AssistantMessageID == "" || turn.StreamURL == "" {
		t.Fatalf("turn response = %#v, want stable ids and stream URL", turn)
	}
	return turn
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

type webAuthAdapter struct {
	*auth.Service
}

type webFailingAuth struct {
	anonymous          storage.Session
	verified           storage.Session
	user               storage.User
	createAnonymousErr error
	verifyErr          error
	rotateErr          error
	logoutErr          error
}

func (a *webFailingAuth) Register(context.Context, string, string) (storage.User, error) {
	return a.user, nil
}

func (a *webFailingAuth) Authenticate(context.Context, string, string) (storage.User, error) {
	return a.user, nil
}

func (a *webFailingAuth) CreateAnonymousBrowserSession(context.Context) (auth.BrowserSession, error) {
	if a.createAnonymousErr != nil {
		return auth.BrowserSession{}, a.createAnonymousErr
	}
	session := a.anonymous
	if session.ID == "" {
		session = storage.Session{ID: "anon", CSRFToken: "csrf", ExpiresAt: time.Now().Add(time.Hour)}
	}
	return auth.BrowserSession{CookieValue: session.ID + ".secret", Session: session}, nil
}

func (a *webFailingAuth) RotateBrowserSession(context.Context, string, int64) (auth.BrowserSession, error) {
	if a.rotateErr != nil {
		return auth.BrowserSession{}, a.rotateErr
	}
	session := storage.Session{ID: "rotated", UserID: a.user.ID, CSRFToken: "csrf-rotated", ExpiresAt: time.Now().Add(time.Hour)}
	return auth.BrowserSession{CookieValue: "rotated.secret", Session: session}, nil
}

func (a *webFailingAuth) VerifyBrowserSession(context.Context, string) (storage.Session, error) {
	if a.verifyErr != nil {
		return storage.Session{}, a.verifyErr
	}
	if a.verified.ID != "" {
		return a.verified, nil
	}
	return storage.Session{}, auth.ErrInvalidSession
}

func (a *webFailingAuth) Logout(context.Context, string) error {
	return a.logoutErr
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

func mustReadAllString(t *testing.T, reader io.Reader) string {
	t.Helper()

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll error = %v, want nil", err)
	}
	return string(body)
}

func drainChatTurnStream(t *testing.T, stream *chat.TurnStream) {
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

func assertReplayEvents(t *testing.T, replay []streamEvent, names []string) {
	t.Helper()

	if len(replay) != len(names) {
		t.Fatalf("replay event count = %d, want %d: %#v", len(replay), len(names), replay)
	}
	for i, name := range names {
		if replay[i].Name != name {
			t.Fatalf("replay[%d] = %q, want %q; replay = %#v", i, replay[i].Name, name, replay)
		}
	}
}

type controlledClient struct {
	mu       sync.Mutex
	events   chan llm.Event
	ctx      chan context.Context
	requests []llm.Request
}

func newControlledClient() *controlledClient {
	return &controlledClient{
		events: make(chan llm.Event),
		ctx:    make(chan context.Context, 1),
	}
}

func (c *controlledClient) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	c.mu.Lock()
	c.requests = append(c.requests, request.Clone())
	c.mu.Unlock()
	c.ctx <- ctx
	return &controlledStream{ctx: ctx, events: c.events}, nil
}

func (c *controlledClient) Requests() []llm.Request {
	c.mu.Lock()
	defer c.mu.Unlock()

	requests := make([]llm.Request, len(c.requests))
	for i, request := range c.requests {
		requests[i] = request.Clone()
	}
	return requests
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

type errorRenderer struct{}

func (errorRenderer) Render(string) (template.HTML, error) {
	return "", errors.New("render failed")
}
