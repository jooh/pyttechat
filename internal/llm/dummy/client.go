package dummy

import (
	"context"

	"example.com/llm-chat-web/internal/llm"
)

const responseText = "This is a dummy LLM response."

type Client struct{}

func NewClient() Client {
	return Client{}
}

func (Client) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{Text: responseText}, nil
}
