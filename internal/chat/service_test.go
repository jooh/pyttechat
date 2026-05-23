package chat

import (
	"context"
	"errors"
	"testing"

	"example.com/llm-chat-web/internal/llm"
)

type recordingClient struct {
	request llm.Request
}

func (c *recordingClient) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	c.request = request
	return llm.Response{Text: "backend response"}, nil
}

func TestServiceSendCallsLLMWithTrimmedPrompt(t *testing.T) {
	client := &recordingClient{}
	service := NewService(client)

	response, err := service.Send(context.Background(), "  hello  ")
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	if response.Text != "backend response" {
		t.Fatalf("Send().Text = %q, want %q", response.Text, "backend response")
	}

	if client.request.Prompt != "hello" {
		t.Fatalf("Complete() prompt = %q, want %q", client.request.Prompt, "hello")
	}
}

func TestServiceSendRejectsEmptyPrompt(t *testing.T) {
	service := NewService(&recordingClient{})

	_, err := service.Send(context.Background(), "  ")
	if !errors.Is(err, ErrEmptyPrompt) {
		t.Fatalf("Send() error = %v, want %v", err, ErrEmptyPrompt)
	}
}
