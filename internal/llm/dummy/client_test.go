package dummy

import (
	"context"
	"testing"

	"example.com/llm-chat-web/internal/llm"
)

func TestCompleteReturnsDeterministicResponse(t *testing.T) {
	client := NewClient()

	response, err := client.Complete(context.Background(), llm.Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Complete() error = %v, want nil", err)
	}

	if response.Text != responseText {
		t.Fatalf("Complete().Text = %q, want %q", response.Text, responseText)
	}
}
