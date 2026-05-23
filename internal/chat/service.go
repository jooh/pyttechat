package chat

import (
	"context"
	"errors"
	"strings"

	"example.com/llm-chat-web/internal/llm"
)

var ErrEmptyPrompt = errors.New("prompt must not be empty")

type Service struct {
	client llm.Client
}

type Response struct {
	Text string
}

func NewService(client llm.Client) Service {
	return Service{client: client}
}

func (s Service) Send(ctx context.Context, prompt string) (Response, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return Response{}, ErrEmptyPrompt
	}

	response, err := s.client.Complete(ctx, llm.Request{Prompt: prompt})
	if err != nil {
		return Response{}, err
	}

	return Response{Text: response.Text}, nil
}
