package llm

import (
	"context"
	"strings"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type PartType string

const (
	PartText       PartType = "text"
	PartReasoning  PartType = "reasoning"
	PartSummary    PartType = "summary"
	PartError      PartType = "error"
	PartImage      PartType = "image"
	PartAttachment PartType = "attachment"
)

type Part struct {
	Type             PartType
	Text             string
	ID               string
	Summary          []string
	EncryptedContent string
	URL              string
	Filename         string
	MimeType         string
	Alt              string
	Width            int
	Height           int
	Size             int64
}

func (p Part) Clone() Part {
	clone := p
	if p.Summary != nil {
		clone.Summary = append([]string(nil), p.Summary...)
	}
	return clone
}

type Message struct {
	Role        Role
	Parts       []Part
	CompletedAt time.Time
}

func NewTextMessage(role Role, text string) Message {
	return Message{
		Role: role,
		Parts: []Part{
			{Type: PartText, Text: text},
		},
	}
}

func (m Message) Text() string {
	var text strings.Builder
	for _, part := range m.Parts {
		if part.Type == PartText {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}

func (m Message) Clone() Message {
	clone := Message{Role: m.Role, CompletedAt: m.CompletedAt}
	if m.Parts != nil {
		clone.Parts = make([]Part, len(m.Parts))
		for i, part := range m.Parts {
			clone.Parts[i] = part.Clone()
		}
	}
	return clone
}

func CloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	clone := make([]Message, len(messages))
	for i, message := range messages {
		clone[i] = message.Clone()
	}
	return clone
}

type ReasoningOptions struct {
	Summary string
	Effort  string
}

type Request struct {
	Model        string
	Instructions string
	Messages     []Message
	Reasoning    ReasoningOptions
}

func (r Request) Clone() Request {
	clone := r
	clone.Messages = CloneMessages(r.Messages)
	return clone
}

type Usage struct {
	InputTokens     int
	OutputTokens    int
	TotalTokens     int
	ReasoningTokens int
}

type EventType string

const (
	EventTextDelta      EventType = "text_delta"
	EventReasoningDelta EventType = "reasoning_delta"
	EventOutputItemDone EventType = "output_item_done"
	EventCompleted      EventType = "completed"
	EventError          EventType = "error"
)

type Event struct {
	Type       EventType
	Delta      string
	Part       Part
	ResponseID string
	Usage      *Usage
	Err        error
}

type Stream interface {
	Next() (Event, error)
	Close() error
}

type Client interface {
	Stream(ctx context.Context, request Request) (Stream, error)
}
