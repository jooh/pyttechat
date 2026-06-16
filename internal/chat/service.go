package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"example.com/llm-chat-web/internal/llm"
	"example.com/llm-chat-web/internal/observability"
)

var (
	ErrEmptyPrompt        = errors.New("prompt must not be empty")
	ErrInvalidReplaceFrom = errors.New("replace_from must point to a user message or the end of the conversation")
	ErrTurnInProgress     = errors.New("turn already in progress")
)

type Service struct {
	client llm.Client
	store  Store
}

func NewService(client llm.Client) Service {
	return Service{client: client}
}

func NewPersistentService(client llm.Client, store Store) Service {
	return Service{client: client, store: store}
}

type Store interface {
	Messages(context.Context, int64) ([]llm.Message, error)
	AppendTurn(context.Context, int64, llm.Message, llm.Message) error
	ReplaceTailAndAppendTurn(context.Context, int64, int, llm.Message, llm.Message) error
}

func (s Service) NewSession() *Session {
	return &Session{client: s.client}
}

func (s Service) NewPersistedSession(ctx context.Context, conversationID int64) (*Session, error) {
	if s.store == nil {
		return s.NewSession(), nil
	}
	messages, err := s.store.Messages(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return &Session{
		client:         s.client,
		store:          s.store,
		conversationID: conversationID,
		messages:       llm.CloneMessages(messages),
	}, nil
}

type Session struct {
	mu             sync.Mutex
	client         llm.Client
	store          Store
	conversationID int64
	messages       []llm.Message
	inFlight       bool
}

type SendOptions struct {
	Model                 string
	ReasoningEffort       string
	RenderingInstructions string
	TelemetryComponent    string
	ReplaceFrom           *int
	Now                   func() time.Time
}

func (s *Session) Send(ctx context.Context, prompt string, opts SendOptions) (*TurnStream, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, ErrEmptyPrompt
	}

	ctx, span := observability.StartSpan(ctx, "chat.turn.start", opts.TelemetryComponent)
	startedAt := time.Now()
	defer span.End()

	userMessage := llm.NewTextMessage(llm.RoleUser, prompt)
	request := llm.Request{
		Model:        opts.Model,
		Instructions: strings.TrimSpace(opts.RenderingInstructions),
	}
	if effort := strings.TrimSpace(opts.ReasoningEffort); effort != "" {
		request.Reasoning = llm.ReasoningOptions{
			Summary: "auto",
			Effort:  effort,
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	s.mu.Lock()
	if s.inFlight {
		s.mu.Unlock()
		observability.RecordSpanError(span, ErrTurnInProgress)
		return nil, ErrTurnInProgress
	}
	keepMessages, err := replaceFromIndex(s.messages, opts.ReplaceFrom)
	if err != nil {
		s.mu.Unlock()
		observability.RecordSpanError(span, err)
		return nil, err
	}
	replaceTail := opts.ReplaceFrom != nil
	request.Messages = append(llm.CloneMessages(s.messages[:keepMessages]), userMessage.Clone())
	s.inFlight = true
	s.mu.Unlock()

	observability.ChatTurnStarted(ctx)
	stream, err := s.client.Stream(ctx, request)
	if err != nil {
		s.releaseTurn()
		observability.ChatTurnFailed(ctx, startedAt, err)
		observability.RecordSpanError(span, err)
		return nil, err
	}

	return &TurnStream{
		session:      s,
		stream:       stream,
		userMessage:  userMessage,
		keepMessages: keepMessages,
		replaceTail:  replaceTail,
		ctx:          ctx,
		startedAt:    startedAt,
		now:          now,
	}, nil
}

func (s *Session) Messages() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return llm.CloneMessages(s.messages)
}

func (s *Session) ValidateReplaceFrom(replaceFrom *int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := replaceFromIndex(s.messages, replaceFrom)
	return err
}

func (s *Session) CommitStopped(ctx context.Context, prompt string, opts SendOptions) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ErrEmptyPrompt
	}
	userMessage := llm.NewTextMessage(llm.RoleUser, prompt)

	s.mu.Lock()
	keepMessages, err := replaceFromIndex(s.messages, opts.ReplaceFrom)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.appendOrReplaceTailLocked(ctx, opts.ReplaceFrom != nil, keepMessages, userMessage, llm.Message{Role: llm.RoleAssistant}); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return nil
}

type TurnStream struct {
	session      *Session
	stream       llm.Stream
	userMessage  llm.Message
	keepMessages int
	replaceTail  bool
	ctx          context.Context
	startedAt    time.Time
	now          func() time.Time

	assistantParts []llm.Part
	completedAt    time.Time
	completed      bool
	finalized      bool
	recorded       bool
}

func (s *TurnStream) Next() (llm.Event, error) {
	event, err := s.stream.Next()
	if err != nil {
		s.recordError(err)
		if !errors.Is(err, context.Canceled) {
			s.abort()
		}
		return llm.Event{}, err
	}

	switch event.Type {
	case llm.EventReasoningDelta:
		s.appendDelta(llm.PartReasoning, event.Delta)
	case llm.EventTextDelta:
		s.appendDelta(llm.PartText, event.Delta)
	case llm.EventOutputItemDone:
		s.mergeCompletedPart(event.Part)
	case llm.EventCompleted:
		s.completed = true
		if err := s.finalize(false); err != nil {
			s.recordError(err)
			s.abort()
			return event, err
		}
		s.recordCompleted()
	case llm.EventError:
		if event.Err != nil {
			s.recordFailed(event.Err)
			s.abort()
			return event, event.Err
		}
	}

	return event, nil
}

func (s *TurnStream) Close() error {
	if !s.completed {
		s.recordCancelled()
	}
	s.abort()
	return s.stream.Close()
}

func (s *TurnStream) CommitPartial() error {
	return s.finalize(true)
}

func (s *TurnStream) CompletedAt() time.Time {
	return s.completedAt
}

func (s *TurnStream) appendDelta(partType llm.PartType, delta string) {
	if delta == "" {
		return
	}
	lastIndex := len(s.assistantParts) - 1
	if lastIndex >= 0 && s.assistantParts[lastIndex].Type == partType {
		s.assistantParts[lastIndex].Text += delta
		return
	}
	s.assistantParts = append(s.assistantParts, llm.Part{Type: partType, Text: delta})
}

func (s *TurnStream) mergeCompletedPart(part llm.Part) {
	if part.Type == "" {
		return
	}

	if part.Type == llm.PartReasoning {
		for i := len(s.assistantParts) - 1; i >= 0; i-- {
			if s.assistantParts[i].Type != llm.PartReasoning {
				continue
			}
			if part.Text != "" {
				s.assistantParts[i].Text = part.Text
			}
			if part.ID != "" {
				s.assistantParts[i].ID = part.ID
			}
			if len(part.Summary) > 0 {
				s.assistantParts[i].Summary = append([]string(nil), part.Summary...)
			}
			if part.EncryptedContent != "" {
				s.assistantParts[i].EncryptedContent = part.EncryptedContent
			}
			return
		}
	}

	if part.Type == llm.PartText {
		lastIndex := len(s.assistantParts) - 1
		if lastIndex >= 0 && s.assistantParts[lastIndex].Type == llm.PartText && part.Text != "" && strings.HasPrefix(part.Text, s.assistantParts[lastIndex].Text) {
			s.assistantParts[lastIndex].Text = part.Text
			return
		}
	}

	s.assistantParts = append(s.assistantParts, part.Clone())
}

func (s *TurnStream) finalize(allowIncomplete bool) error {
	if (!s.completed && !allowIncomplete) || s.finalized {
		return nil
	}

	assistant := llm.Message{
		Role:        llm.RoleAssistant,
		Parts:       cloneParts(s.assistantParts),
		CompletedAt: s.completionTime(),
	}

	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if err := s.session.appendOrReplaceTailLocked(context.Background(), s.replaceTail, s.keepMessages, s.userMessage, assistant); err != nil {
		return err
	}
	s.finalized = true
	return nil
}

func (s *TurnStream) completionTime() time.Time {
	if !s.completed {
		return time.Time{}
	}
	if s.completedAt.IsZero() {
		now := s.now
		if now == nil {
			now = time.Now
		}
		s.completedAt = now().UTC()
	}
	return s.completedAt
}

func (s *TurnStream) abort() {
	if s.finalized {
		return
	}
	s.finalized = true
	s.session.releaseTurn()
}

func (s *TurnStream) recordCompleted() {
	if s.recorded {
		return
	}
	s.recorded = true
	observability.ChatTurnCompleted(s.ctx, s.startedAt)
}

func (s *TurnStream) recordCancelled() {
	if s.recorded {
		return
	}
	s.recorded = true
	observability.ChatTurnCancelled(s.ctx, s.startedAt)
}

func (s *TurnStream) recordFailed(err error) {
	if s.recorded {
		return
	}
	s.recorded = true
	observability.ChatTurnFailed(s.ctx, s.startedAt, err)
}

func (s *TurnStream) recordError(err error) {
	if errors.Is(err, context.Canceled) {
		s.recordCancelled()
		return
	}
	s.recordFailed(err)
}

func (s *Session) releaseTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight = false
}

func replaceFromIndex(messages []llm.Message, replaceFrom *int) (int, error) {
	if replaceFrom == nil {
		return len(messages), nil
	}
	keepMessages := *replaceFrom
	if keepMessages < 0 || keepMessages > len(messages) {
		return 0, ErrInvalidReplaceFrom
	}
	if keepMessages < len(messages) && messages[keepMessages].Role != llm.RoleUser {
		return 0, ErrInvalidReplaceFrom
	}
	return keepMessages, nil
}

func (s *Session) appendOrReplaceTailLocked(ctx context.Context, replaceTail bool, keepMessages int, userMessage, assistant llm.Message) error {
	if s.store != nil {
		var err error
		if replaceTail {
			err = s.store.ReplaceTailAndAppendTurn(ctx, s.conversationID, keepMessages, userMessage.Clone(), assistant.Clone())
		} else {
			err = s.store.AppendTurn(ctx, s.conversationID, userMessage.Clone(), assistant.Clone())
		}
		if err != nil {
			return err
		}
	}
	nextMessages := llm.CloneMessages(s.messages)
	if replaceTail {
		nextMessages = llm.CloneMessages(s.messages[:keepMessages])
	}
	nextMessages = append(nextMessages, userMessage.Clone(), assistant.Clone())
	s.messages = nextMessages
	s.inFlight = false
	return nil
}

func cloneParts(parts []llm.Part) []llm.Part {
	if parts == nil {
		return nil
	}
	clone := make([]llm.Part, len(parts))
	for i, part := range parts {
		clone[i] = part.Clone()
	}
	return clone
}
