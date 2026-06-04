package chat

import (
	"context"
	"errors"
	"strings"
	"sync"

	"example.com/llm-chat-web/internal/llm"
)

var (
	ErrEmptyPrompt    = errors.New("prompt must not be empty")
	ErrTurnInProgress = errors.New("turn already in progress")
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
}

func (s *Session) Send(ctx context.Context, prompt string, opts SendOptions) (*TurnStream, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, ErrEmptyPrompt
	}

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
		return nil, ErrTurnInProgress
	}
	request.Messages = append(llm.CloneMessages(s.messages), userMessage.Clone())
	s.inFlight = true
	s.mu.Unlock()

	stream, err := s.client.Stream(ctx, request)
	if err != nil {
		s.releaseTurn()
		return nil, err
	}

	return &TurnStream{
		session:     s,
		stream:      stream,
		userMessage: userMessage,
	}, nil
}

func (s *Session) Messages() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return llm.CloneMessages(s.messages)
}

type TurnStream struct {
	session     *Session
	stream      llm.Stream
	userMessage llm.Message

	assistantParts []llm.Part
	completed      bool
	finalized      bool
}

func (s *TurnStream) Next() (llm.Event, error) {
	event, err := s.stream.Next()
	if err != nil {
		s.abort()
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
		if err := s.finalize(); err != nil {
			s.abort()
			return event, err
		}
	case llm.EventError:
		if event.Err != nil {
			s.abort()
			return event, event.Err
		}
	}

	return event, nil
}

func (s *TurnStream) Close() error {
	s.abort()
	return s.stream.Close()
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

func (s *TurnStream) finalize() error {
	if !s.completed || s.finalized {
		return nil
	}

	assistant := llm.Message{
		Role:  llm.RoleAssistant,
		Parts: cloneParts(s.assistantParts),
	}

	s.session.mu.Lock()
	defer s.session.mu.Unlock()
	if s.session.store != nil {
		if err := s.session.store.AppendTurn(context.Background(), s.session.conversationID, s.userMessage.Clone(), assistant.Clone()); err != nil {
			return err
		}
	}
	s.finalized = true
	s.session.messages = append(s.session.messages, s.userMessage.Clone(), assistant)
	s.session.inFlight = false
	return nil
}

func (s *TurnStream) abort() {
	if s.finalized {
		return
	}
	s.finalized = true
	s.session.releaseTurn()
}

func (s *Session) releaseTurn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight = false
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
