package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.com/llm-chat-web/internal/chat"
	"example.com/llm-chat-web/internal/llm"
	"example.com/llm-chat-web/internal/markdown"
)

const (
	sessionCookieName = "pyttechat_session"
	csrfHeaderName    = "X-CSRF-Token"
)

//go:embed assets/* assets/vendor/* assets/vendor/katex/* assets/vendor/katex/fonts/* templates/*
var embeddedFiles embed.FS

var randomReader io.Reader = rand.Reader
var timeNow = func() time.Time { return time.Now().UTC() }

type Options struct {
	Client          llm.Client
	Model           string
	ReasoningEffort string
	CookieSecure    bool
}

type Server struct {
	client          llm.Client
	model           string
	reasoningEffort string
	cookieSecure    bool
	template        *template.Template
	markdown        *markdown.Renderer
	assets          http.Handler

	mu       sync.Mutex
	sessions map[string]*browserSession
}

type browserSession struct {
	id    string
	csrf  string
	chat  *chat.Session
	turns map[string]*turnJob
	mu    sync.Mutex
}

func NewServer(opts Options) *Server {
	assets, _ := fs.Sub(embeddedFiles, "assets")
	renderer := markdown.NewRenderer()
	tmpl := template.Must(template.ParseFS(embeddedFiles, "templates/*.html"))
	return &Server{
		client:          opts.Client,
		model:           opts.Model,
		reasoningEffort: opts.ReasoningEffort,
		cookieSecure:    opts.CookieSecure,
		template:        tmpl,
		markdown:        renderer,
		assets:          http.StripPrefix("/assets/", http.FileServer(http.FS(assets))),
		sessions:        map[string]*browserSession{},
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		s.handleIndex(w, r)
	case r.URL.Path == "/favicon.ico" && r.Method == http.MethodGet:
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/chat/turns" && r.Method == http.MethodPost:
		s.handleCreateTurn(w, r)
	case strings.HasPrefix(r.URL.Path, "/chat/turns/"):
		s.handleTurnRoute(w, r)
	case strings.HasPrefix(r.URL.Path, "/assets/") && r.Method == http.MethodGet:
		s.assets.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	session, err := s.session(w, r)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "session_error", "could not create session")
		return
	}

	data := pageData{
		CSRFToken:  session.csrf,
		ModelLabel: modelDisplayLabel(s.model),
		Messages:   viewMessages(session.chat.Messages(), s.markdown),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.template.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) handleCreateTurn(w http.ResponseWriter, r *http.Request) {
	session, err := s.session(w, r)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "session_error", "could not create session")
		return
	}
	if !validCSRF(r, session.csrf) {
		writeJSONError(w, http.StatusForbidden, "invalid_csrf", "invalid CSRF token")
		return
	}

	var request createTurnRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if decodeErr := decoder.Decode(&request); decodeErr != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "invalid JSON")
		return
	}
	if decodeErr := decoder.Decode(&struct{}{}); decodeErr == nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "unexpected trailing JSON")
		return
	} else if !errors.Is(decodeErr, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "invalid JSON")
		return
	}

	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		writeJSONError(w, http.StatusBadRequest, "empty_prompt", "prompt must not be empty")
		return
	}

	turn, err := newTurnJob(prompt)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "turn_error", "could not create turn")
		return
	}

	session.mu.Lock()
	for _, existing := range session.turns {
		if !existing.isTerminal() {
			session.mu.Unlock()
			writeJSONError(w, http.StatusConflict, "turn_in_progress", "turn already in progress")
			return
		}
	}
	session.turns[turn.id] = turn
	session.mu.Unlock()

	go turn.run(session.chat, chat.SendOptions{
		Model:           s.model,
		ReasoningEffort: s.reasoningEffort,
	})

	writeJSON(w, http.StatusCreated, createTurnResponse{
		TurnID:             turn.id,
		UserMessageID:      turn.userMessageID,
		AssistantMessageID: turn.assistantMessageID,
		StreamURL:          "/chat/turns/" + turn.id + "/events",
	})
}

func (s *Server) handleTurnRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/chat/turns/")
	turnID, action, ok := strings.Cut(path, "/")
	if !ok || turnID == "" {
		http.NotFound(w, r)
		return
	}

	switch {
	case action == "events" && r.Method == http.MethodGet:
		s.handleTurnEvents(w, r, turnID)
	case action == "abort" && r.Method == http.MethodPost:
		s.handleAbortTurn(w, r, turnID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleTurnEvents(w http.ResponseWriter, r *http.Request, turnID string) {
	session, err := s.session(w, r)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "session_error", "could not create session")
		return
	}

	turn := session.turn(turnID)
	if turn == nil {
		writeJSONError(w, http.StatusNotFound, "turn_not_found", "turn not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming unsupported")
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	replay, updates, terminal := turn.subscribe(lastEventID(r))
	if updates != nil {
		defer turn.unsubscribe(updates)
	}

	for _, event := range replay {
		if err := writeSSE(w, flusher, event); err != nil {
			return
		}
	}
	if terminal {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-updates:
			if !ok {
				return
			}
			if err := writeSSE(w, flusher, event); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleAbortTurn(w http.ResponseWriter, r *http.Request, turnID string) {
	session, err := s.session(w, r)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "session_error", "could not create session")
		return
	}
	if !validCSRF(r, session.csrf) {
		writeJSONError(w, http.StatusForbidden, "invalid_csrf", "invalid CSRF token")
		return
	}

	turn := session.turn(turnID)
	if turn == nil {
		writeJSONError(w, http.StatusNotFound, "turn_not_found", "turn not found")
		return
	}
	if !turn.abort(r.Context()) {
		writeJSONError(w, http.StatusConflict, "turn_finished", "turn is already finished")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"aborted": turnID,
	})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) (*browserSession, error) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		s.mu.Lock()
		session := s.sessions[cookie.Value]
		s.mu.Unlock()
		if session != nil {
			return session, nil
		}
	}

	id, err := randomID("sess")
	if err != nil {
		return nil, err
	}
	csrf, err := randomID("csrf")
	if err != nil {
		return nil, err
	}
	session := &browserSession{
		id:    id,
		csrf:  csrf,
		chat:  chat.NewService(s.client).NewSession(),
		turns: map[string]*turnJob{},
	}

	s.mu.Lock()
	s.sessions[id] = session
	s.mu.Unlock()

	// #nosec G124 -- CookieSecure is configurable so local HTTP development can use cookies; production should enable it.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	return session, nil
}

func (s *browserSession) turn(id string) *turnJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turns[id]
}

func validCSRF(r *http.Request, token string) bool {
	return token != "" && r.Header.Get(csrfHeaderName) == token
}

type createTurnRequest struct {
	Prompt string `json:"prompt"`
}

type createTurnResponse struct {
	TurnID             string `json:"turn_id"`
	UserMessageID      string `json:"user_message_id"`
	AssistantMessageID string `json:"assistant_message_id"`
	StreamURL          string `json:"stream_url"`
}

type pageData struct {
	CSRFToken  string
	ModelLabel string
	Messages   []viewMessage
}

type viewMessage struct {
	Role     string
	Label    string
	Text     string
	HTML     template.HTML
	Statuses []viewStatus
}

type viewStatus struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Text      string `json:"text"`
	ContentID string `json:"content_id,omitempty"`
}

func viewMessages(messages []llm.Message, renderer *markdown.Renderer) []viewMessage {
	out := make([]viewMessage, 0, len(messages))
	for messageIndex, message := range messages {
		text := message.Text()
		view := viewMessage{
			Role:  string(message.Role),
			Label: messageRoleLabel(message.Role),
			Text:  text,
		}
		if message.Role == llm.RoleAssistant {
			statuses := assistantStatuses(message.Parts, messageIndex)
			html, err := renderAssistantBody(message.Parts, renderer)
			if err != nil {
				log.Printf("markdown render failed for stored assistant message: %v", err)
				html = escapedPlainTextHTML(text)
			}
			view.HTML = html
			view.Statuses = statuses
		}
		if view.Text == "" && view.HTML == "" && len(view.Statuses) == 0 {
			continue
		}
		out = append(out, view)
	}
	return out
}

func assistantStatuses(parts []llm.Part, messageIndex int) []viewStatus {
	var statuses []viewStatus
	for partIndex, part := range parts {
		switch part.Type {
		case llm.PartReasoning:
			text := reasoningDisplayText(part)
			if text == "" {
				continue
			}
			statuses = append(statuses, viewStatus{
				Kind:      "thinking",
				Label:     "thinking",
				Text:      text,
				ContentID: statusContentID(messageIndex, partIndex),
			})
		case llm.PartSummary:
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			statuses = append(statuses, viewStatus{
				Kind:      "summary",
				Label:     "summary",
				Text:      text,
				ContentID: statusContentID(messageIndex, partIndex),
			})
		}
	}
	return statuses
}

func statusContentID(messageIndex, partIndex int) string {
	if messageIndex < 0 {
		return ""
	}
	return fmt.Sprintf("message-status-content-%d-%d", messageIndex, partIndex)
}

func reasoningDisplayText(part llm.Part) string {
	if text := strings.TrimSpace(part.Text); text != "" {
		return text
	}
	return strings.TrimSpace(strings.Join(part.Summary, "\n"))
}

func renderAssistantBody(parts []llm.Part, renderer *markdown.Renderer) (template.HTML, error) {
	var out strings.Builder
	for _, part := range parts {
		switch part.Type {
		case llm.PartText:
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			html, err := renderer.Render(part.Text)
			if err != nil {
				return "", err
			}
			out.WriteString(string(html))
		case llm.PartError:
			out.WriteString(renderErrorPart(part))
		case llm.PartImage:
			out.WriteString(renderImagePart(part))
		case llm.PartAttachment:
			out.WriteString(renderAttachmentPart(part))
		}
	}
	return template.HTML(out.String()), nil
}

func renderErrorPart(part llm.Part) string {
	text := strings.TrimSpace(part.Text)
	if text == "" {
		return ""
	}
	return `<div class="message-part-error" role="alert">` + stdhtml.EscapeString(text) + `</div>`
}

func renderImagePart(part llm.Part) string {
	src := safeBFFURL(part.URL)
	if src == "" {
		return ""
	}
	alt := strings.TrimSpace(part.Alt)
	if alt == "" {
		alt = strings.TrimSpace(part.Filename)
	}

	var out strings.Builder
	out.WriteString(`<figure class="message-image"><a href="`)
	out.WriteString(stdhtml.EscapeString(src))
	out.WriteString(`" target="_blank" rel="noopener noreferrer"><img src="`)
	out.WriteString(stdhtml.EscapeString(src))
	out.WriteString(`" alt="`)
	out.WriteString(stdhtml.EscapeString(alt))
	out.WriteString(`" loading="lazy" decoding="async"`)
	if part.Width > 0 {
		out.WriteString(` width="`)
		out.WriteString(strconv.Itoa(part.Width))
		out.WriteString(`"`)
	}
	if part.Height > 0 {
		out.WriteString(` height="`)
		out.WriteString(strconv.Itoa(part.Height))
		out.WriteString(`"`)
	}
	out.WriteString(`></a>`)
	if caption := strings.TrimSpace(part.Filename); caption != "" {
		out.WriteString(`<figcaption>`)
		out.WriteString(stdhtml.EscapeString(caption))
		out.WriteString(`</figcaption>`)
	}
	out.WriteString(`</figure>`)
	return out.String()
}

func renderAttachmentPart(part llm.Part) string {
	href := safeBFFURL(part.URL)
	name := strings.TrimSpace(part.Filename)
	if name == "" {
		name = "attachment"
	}

	var out strings.Builder
	out.WriteString(`<div class="message-attachment-block">`)
	if href != "" {
		out.WriteString(`<a class="message-attachment" href="`)
		out.WriteString(stdhtml.EscapeString(href))
		out.WriteString(`">`)
	} else {
		out.WriteString(`<span class="message-attachment">`)
	}
	out.WriteString(`<span class="message-attachment-name">`)
	out.WriteString(stdhtml.EscapeString(name))
	out.WriteString(`</span>`)
	if meta := attachmentMeta(part); meta != "" {
		out.WriteString(`<span class="message-attachment-meta">`)
		out.WriteString(stdhtml.EscapeString(meta))
		out.WriteString(`</span>`)
	}
	if href != "" {
		out.WriteString(`</a>`)
	} else {
		out.WriteString(`</span>`)
	}
	if preview := strings.TrimSpace(part.Text); preview != "" {
		out.WriteString(`<pre class="message-attachment-preview"><code>`)
		out.WriteString(stdhtml.EscapeString(preview))
		out.WriteString(`</code></pre>`)
	}
	out.WriteString(`</div>`)
	return out.String()
}

func attachmentMeta(part llm.Part) string {
	var fields []string
	if mimeType := strings.TrimSpace(part.MimeType); mimeType != "" {
		fields = append(fields, mimeType)
	}
	if size := formatByteSize(part.Size); size != "" {
		fields = append(fields, size)
	}
	return strings.Join(fields, " - ")
}

func formatByteSize(size int64) string {
	switch {
	case size <= 0:
		return ""
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
}

func safeBFFURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsAny(value, "\"'<> \t\r\n") {
		return ""
	}
	if strings.HasPrefix(value, "/assets/") {
		return value
	}
	return ""
}

func messageRoleLabel(role llm.Role) string {
	switch role {
	case llm.RoleUser:
		return "You"
	case llm.RoleAssistant:
		return "Assistant"
	default:
		return string(role)
	}
}

func modelDisplayLabel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "Proxy default"
	}
	return model
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event streamEvent) error {
	if event.ID > 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", event.ID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event.Name); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", event.Data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func lastEventID(r *http.Request) int64 {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		return 0
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
}

func randomID(prefix string) (string, error) {
	var raw [24]byte
	if _, err := io.ReadFull(randomReader, raw[:]); err != nil {
		return "", err
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

type streamEvent struct {
	ID   int64
	Name string
	Data []byte
}

type turnJob struct {
	id                 string
	userMessageID      string
	assistantMessageID string
	prompt             string
	ctx                context.Context
	cancel             context.CancelFunc

	mu             sync.Mutex
	events         []streamEvent
	nextEventID    int64
	subscribers    map[chan streamEvent]struct{}
	terminal       bool
	abortRequested bool
	done           chan struct{}
	doneClosed     bool
}

func newTurnJob(prompt string) (*turnJob, error) {
	turnID, err := randomID("turn")
	if err != nil {
		return nil, err
	}
	userMessageID, err := randomID("msg")
	if err != nil {
		return nil, err
	}
	assistantMessageID, err := randomID("msg")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &turnJob{
		id:                 turnID,
		userMessageID:      userMessageID,
		assistantMessageID: assistantMessageID,
		prompt:             prompt,
		ctx:                ctx,
		cancel:             cancel,
		subscribers:        map[chan streamEvent]struct{}{},
		done:               make(chan struct{}),
	}, nil
}

func (j *turnJob) run(session *chat.Session, opts chat.SendOptions) {
	defer j.finish()

	stream, err := session.Send(j.ctx, j.prompt, opts)
	if err != nil {
		j.emitError(err)
		return
	}
	defer stream.Close()

	renderer := markdown.NewRenderer()
	var fullMarkdown strings.Builder
	var assistantParts []llm.Part
	completed := false
	for {
		event, err := stream.Next()
		if errors.Is(err, io.EOF) {
			if !completed {
				j.emitError(io.ErrUnexpectedEOF)
			}
			return
		}
		if err != nil {
			j.emitError(err)
			return
		}

		switch event.Type {
		case llm.EventTextDelta:
			if event.Delta == "" {
				continue
			}
			fullMarkdown.WriteString(event.Delta)
			appendOutputDelta(&assistantParts, llm.PartText, event.Delta)
			html, err := renderer.Render(fullMarkdown.String())
			if err != nil {
				log.Printf("markdown preview render failed for turn %s: %v", j.id, err)
				html = escapedPlainTextHTML(fullMarkdown.String())
			}
			j.emit("preview", htmlEvent{
				TurnID:             j.id,
				AssistantMessageID: j.assistantMessageID,
				HTML:               html,
			})
		case llm.EventReasoningDelta:
			appendOutputDelta(&assistantParts, llm.PartReasoning, event.Delta)
			j.emit("reasoning", deltaEvent{
				TurnID:             j.id,
				AssistantMessageID: j.assistantMessageID,
				Delta:              event.Delta,
			})
		case llm.EventOutputItemDone:
			mergeCompletedOutputPart(&assistantParts, event.Part)
		case llm.EventCompleted:
			html, err := renderAssistantBody(assistantParts, renderer)
			if err != nil {
				log.Printf("markdown final render failed for turn %s: %v", j.id, err)
				html = escapedPlainTextHTML(fullMarkdown.String())
			}
			j.emitTerminal("done", doneEvent{
				TurnID:             j.id,
				AssistantMessageID: j.assistantMessageID,
				ResponseID:         event.ResponseID,
				Usage:              event.Usage,
				HTML:               html,
				Statuses:           assistantStatuses(assistantParts, -1),
				CompletedAt:        timeNow().UTC().Format(time.RFC3339),
			})
			return
		}
	}
}

func appendOutputDelta(parts *[]llm.Part, partType llm.PartType, delta string) {
	if delta == "" {
		return
	}
	lastIndex := len(*parts) - 1
	if lastIndex >= 0 && (*parts)[lastIndex].Type == partType {
		(*parts)[lastIndex].Text += delta
		return
	}
	*parts = append(*parts, llm.Part{Type: partType, Text: delta})
}

func mergeCompletedOutputPart(parts *[]llm.Part, part llm.Part) {
	if part.Type == "" {
		return
	}
	switch part.Type {
	case llm.PartReasoning:
		for i := len(*parts) - 1; i >= 0; i-- {
			if (*parts)[i].Type != llm.PartReasoning {
				continue
			}
			if part.Text != "" {
				(*parts)[i].Text = part.Text
			}
			if part.ID != "" {
				(*parts)[i].ID = part.ID
			}
			if len(part.Summary) > 0 {
				(*parts)[i].Summary = append([]string(nil), part.Summary...)
			}
			if part.EncryptedContent != "" {
				(*parts)[i].EncryptedContent = part.EncryptedContent
			}
			return
		}
	case llm.PartText:
		lastIndex := len(*parts) - 1
		if lastIndex >= 0 && (*parts)[lastIndex].Type == llm.PartText && part.Text != "" && strings.HasPrefix(part.Text, (*parts)[lastIndex].Text) {
			(*parts)[lastIndex].Text = part.Text
			return
		}
	}
	*parts = append(*parts, part.Clone())
}

func escapedPlainTextHTML(text string) template.HTML {
	escaped := template.HTMLEscapeString(text)
	escaped = strings.ReplaceAll(escaped, "\r\n", "\n")
	escaped = strings.ReplaceAll(escaped, "\r", "\n")
	escaped = strings.ReplaceAll(escaped, "\n", "<br>\n")
	return template.HTML(escaped)
}

func (j *turnJob) emitError(err error) {
	if j.wasAbortRequested() || errors.Is(err, context.Canceled) {
		j.emitTerminal("aborted", abortedEvent{
			TurnID:             j.id,
			AssistantMessageID: j.assistantMessageID,
		})
		return
	}
	j.emitTerminal("stream-error", errorEvent{
		TurnID:             j.id,
		AssistantMessageID: j.assistantMessageID,
		Message:            "The response stream failed.",
	})
}

func (j *turnJob) emit(name string, payload any) {
	name, data := encodeStreamEvent(name, payload)

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.terminal {
		return
	}

	j.nextEventID++
	event := streamEvent{ID: j.nextEventID, Name: name, Data: data}
	j.events = append(j.events, event)
	for subscriber := range j.subscribers {
		select {
		case subscriber <- event:
		default:
			close(subscriber)
			delete(j.subscribers, subscriber)
		}
	}
}

func (j *turnJob) emitTerminal(name string, payload any) {
	name, data := encodeStreamEvent(name, payload)

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.terminal {
		return
	}

	j.nextEventID++
	event := streamEvent{ID: j.nextEventID, Name: name, Data: data}
	j.events = append(j.events, event)
	j.terminal = true
	for subscriber := range j.subscribers {
		select {
		case subscriber <- event:
		default:
		}
		close(subscriber)
		delete(j.subscribers, subscriber)
	}
}

func encodeStreamEvent(name string, payload any) (string, []byte) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "stream-error", []byte(`{"message":"could not encode stream event"}`)
	}
	return name, data
}

func (j *turnJob) subscribe(lastSeenID int64) ([]streamEvent, chan streamEvent, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()

	start := 0
	for start < len(j.events) && j.events[start].ID <= lastSeenID {
		start++
	}
	replay := append([]streamEvent(nil), j.events[start:]...)
	if j.terminal {
		return replay, nil, true
	}
	updates := make(chan streamEvent, 128)
	j.subscribers[updates] = struct{}{}
	return replay, updates, false
}

func (j *turnJob) unsubscribe(updates chan streamEvent) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, ok := j.subscribers[updates]; !ok {
		return
	}
	delete(j.subscribers, updates)
	close(updates)
}

func (j *turnJob) finish() {
	j.mu.Lock()
	defer j.mu.Unlock()

	if !j.terminal {
		j.terminal = true
		for subscriber := range j.subscribers {
			close(subscriber)
			delete(j.subscribers, subscriber)
		}
	}
	if j.doneClosed {
		return
	}
	close(j.done)
	j.doneClosed = true
}

func (j *turnJob) abort(ctx context.Context) bool {
	j.mu.Lock()
	if j.terminal {
		j.mu.Unlock()
		return false
	}
	j.abortRequested = true
	done := j.done
	j.mu.Unlock()

	j.cancel()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return true
}

func (j *turnJob) isTerminal() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.terminal
}

func (j *turnJob) wasAbortRequested() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.abortRequested
}

type deltaEvent struct {
	TurnID             string `json:"turn_id"`
	AssistantMessageID string `json:"assistant_message_id"`
	Delta              string `json:"delta"`
}

type htmlEvent struct {
	TurnID             string        `json:"turn_id"`
	AssistantMessageID string        `json:"assistant_message_id"`
	HTML               template.HTML `json:"html"`
}

type doneEvent struct {
	TurnID             string        `json:"turn_id"`
	AssistantMessageID string        `json:"assistant_message_id"`
	ResponseID         string        `json:"response_id"`
	Usage              *llm.Usage    `json:"usage,omitempty"`
	HTML               template.HTML `json:"html"`
	Statuses           []viewStatus  `json:"statuses,omitempty"`
	CompletedAt        string        `json:"completed_at"`
}

type abortedEvent struct {
	TurnID             string `json:"turn_id"`
	AssistantMessageID string `json:"assistant_message_id"`
}

type errorEvent struct {
	TurnID             string `json:"turn_id"`
	AssistantMessageID string `json:"assistant_message_id"`
	Message            string `json:"message"`
}
