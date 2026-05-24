package main

import (
	"context"
	"os"

	"example.com/llm-chat-web/internal/cli"
)

var (
	exit    = os.Exit
	execute = cli.Execute
)

func main() {
	exit(execute(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
