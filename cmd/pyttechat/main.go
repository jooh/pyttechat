package main

import (
	"context"
	"os"

	"example.com/llm-chat-web/internal/cli"
)

func main() {
	os.Exit(cli.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
