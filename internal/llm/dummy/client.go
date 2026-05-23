package dummy

import (
	"context"
	"io"
	"sync"

	"example.com/llm-chat-web/internal/llm"
)

const responseText = "This is a dummy LLM response."

type Turn struct {
	ReasoningChunks []string
	TextChunks      []string
	ReasoningPart   llm.Part
}

type Client struct {
	mu       sync.Mutex
	turns    []Turn
	requests []llm.Request
}

func NewClient(turns ...Turn) *Client {
	if len(turns) == 0 {
		turns = []Turn{defaultTurn()}
	}
	return &Client{turns: turns}
}

func (c *Client) Requests() []llm.Request {
	c.mu.Lock()
	defer c.mu.Unlock()

	requests := make([]llm.Request, len(c.requests))
	for i, request := range c.requests {
		requests[i] = request.Clone()
	}
	return requests
}

func (c *Client) Stream(ctx context.Context, request llm.Request) (llm.Stream, error) {
	c.mu.Lock()
	c.requests = append(c.requests, request.Clone())
	turnNumber := len(c.requests)
	turn := c.turns[min(turnNumber-1, len(c.turns)-1)]
	c.mu.Unlock()

	events := make([]llm.Event, 0, len(turn.ReasoningChunks)+len(turn.TextChunks)+2)
	for _, chunk := range turn.ReasoningChunks {
		events = append(events, llm.Event{Type: llm.EventReasoningDelta, Delta: chunk})
	}
	reasoningPart := turn.ReasoningPart.Clone()
	if reasoningPart.Type == "" {
		reasoningPart.Type = llm.PartReasoning
	}
	if reasoningPart.Text == "" {
		for _, chunk := range turn.ReasoningChunks {
			reasoningPart.Text += chunk
		}
	}
	if len(reasoningPart.Summary) == 0 && reasoningPart.Text != "" {
		reasoningPart.Summary = []string{reasoningPart.Text}
	}
	if reasoningPart.ID == "" {
		reasoningPart.ID = "dummy-reasoning-1"
	}
	if reasoningPart.EncryptedContent == "" {
		reasoningPart.EncryptedContent = "dummy-encrypted-1"
	}
	events = append(events, llm.Event{Type: llm.EventOutputItemDone, Part: reasoningPart})
	for _, chunk := range turn.TextChunks {
		events = append(events, llm.Event{Type: llm.EventTextDelta, Delta: chunk})
	}
	events = append(events, llm.Event{
		Type:       llm.EventCompleted,
		ResponseID: "dummy-response-" + itoa(turnNumber),
	})

	return &stream{ctx: ctx, events: events}, nil
}

type stream struct {
	ctx    context.Context
	events []llm.Event
	index  int
}

func (s *stream) Next() (llm.Event, error) {
	select {
	case <-s.ctx.Done():
		return llm.Event{}, s.ctx.Err()
	default:
	}

	if s.index >= len(s.events) {
		return llm.Event{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (*stream) Close() error {
	return nil
}

func defaultTurn() Turn {
	return Turn{
		ReasoningChunks: []string{"Thinking about the prompt."},
		TextChunks:      []string{responseText},
		ReasoningPart: llm.Part{
			Type:             llm.PartReasoning,
			Summary:          []string{"Thinking about the prompt."},
			ID:               "dummy-reasoning-1",
			EncryptedContent: "dummy-encrypted-1",
		},
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for i > 0 {
		pos--
		digits[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(digits[pos:])
}
