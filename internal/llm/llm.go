package llm

import "context"

type Request struct {
	Prompt string
}

type Response struct {
	Text string
}

type Client interface {
	Complete(ctx context.Context, request Request) (Response, error)
}
