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
	ErrInvalidResume      = errors.New("resume requires a stopped assistant response")
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
	ReplaceLastAssistant(context.Context, int64, llm.Message) error
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
}

type ResumeOptions struct {
	Model                 string
	ReasoningEffort       string
	RenderingInstructions string
	TelemetryComponent    string
	ContinuationPrompt    string
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
		ctx:          ctx,
		startedAt:    startedAt,
	}, nil
}

func (s *Session) ResumeLastAssistant(ctx context.Context, opts ResumeOptions) (*TurnStream, error) {
	prompt := strings.TrimSpace(opts.ContinuationPrompt)
	if prompt == "" {
		return nil, ErrEmptyPrompt
	}

	ctx, span := observability.StartSpan(ctx, "chat.turn.resume", opts.TelemetryComponent)
	startedAt := time.Now()
	defer span.End()

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

	continuationMessage := llm.NewTextMessage(llm.RoleUser, prompt)

	s.mu.Lock()
	if s.inFlight {
		s.mu.Unlock()
		observability.RecordSpanError(span, ErrTurnInProgress)
		return nil, ErrTurnInProgress
	}
	lastIndex := len(s.messages) - 1
	if lastIndex < 0 || s.messages[lastIndex].Role != llm.RoleAssistant {
		s.mu.Unlock()
		observability.RecordSpanError(span, ErrInvalidResume)
		return nil, ErrInvalidResume
	}
	baseAssistant := s.messages[lastIndex].Clone()
	request.Messages = append(llm.CloneMessages(s.messages), continuationMessage.Clone())
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
		session:             s,
		stream:              stream,
		userMessage:         continuationMessage,
		ctx:                 ctx,
		startedAt:           startedAt,
		resumeBaseAssistant: baseAssistant,
		resume:              true,
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
	if err := s.replaceTailLocked(ctx, keepMessages, userMessage, llm.Message{Role: llm.RoleAssistant}); err != nil {
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
	ctx          context.Context
	startedAt    time.Time

	assistantParts []llm.Part
	completed      bool
	finalized      bool
	recorded       bool

	resumeBaseAssistant llm.Message
	resume              bool
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
		Role:  llm.RoleAssistant,
		Parts: cloneParts(s.assistantParts),
	}
	if s.resume {
		assistant.Parts = mergeAssistantParts(s.resumeBaseAssistant.Parts, assistant.Parts)
	}

	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if s.resume {
		if err := s.session.replaceLastAssistantLocked(context.Background(), assistant); err != nil {
			return err
		}
	} else {
		if err := s.session.replaceTailLocked(context.Background(), s.keepMessages, s.userMessage, assistant); err != nil {
			return err
		}
	}
	s.finalized = true
	return nil
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

func (s *Session) replaceTailLocked(ctx context.Context, keepMessages int, userMessage, assistant llm.Message) error {
	if s.store != nil {
		if err := s.store.ReplaceTailAndAppendTurn(ctx, s.conversationID, keepMessages, userMessage.Clone(), assistant.Clone()); err != nil {
			return err
		}
	}
	nextMessages := append(llm.CloneMessages(s.messages[:keepMessages]), userMessage.Clone(), assistant.Clone())
	s.messages = nextMessages
	s.inFlight = false
	return nil
}

func (s *Session) replaceLastAssistantLocked(ctx context.Context, assistant llm.Message) error {
	lastIndex := len(s.messages) - 1
	if lastIndex < 0 || s.messages[lastIndex].Role != llm.RoleAssistant || assistant.Role != llm.RoleAssistant {
		return ErrInvalidResume
	}
	if s.store != nil {
		if err := s.store.ReplaceLastAssistant(ctx, s.conversationID, assistant.Clone()); err != nil {
			return err
		}
	}
	nextMessages := llm.CloneMessages(s.messages)
	nextMessages[lastIndex] = assistant.Clone()
	s.messages = nextMessages
	s.inFlight = false
	return nil
}

func mergeAssistantParts(base, continuation []llm.Part) []llm.Part {
	parts := cloneParts(base)
	for _, part := range continuation {
		if part.Type == "" {
			continue
		}
		lastIndex := len(parts) - 1
		if lastIndex >= 0 && canMergeAssistantParts(parts[lastIndex], part) {
			parts[lastIndex].Text += part.Text
			if len(part.Summary) > 0 {
				parts[lastIndex].Summary = append(parts[lastIndex].Summary, part.Summary...)
			}
			if part.ID != "" {
				parts[lastIndex].ID = part.ID
			}
			if part.EncryptedContent != "" {
				parts[lastIndex].EncryptedContent = part.EncryptedContent
			}
			continue
		}
		parts = append(parts, part.Clone())
	}
	return parts
}

func canMergeAssistantParts(left, right llm.Part) bool {
	return (left.Type == llm.PartText || left.Type == llm.PartReasoning) && left.Type == right.Type
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
