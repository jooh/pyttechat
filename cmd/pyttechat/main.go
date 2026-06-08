package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"example.com/llm-chat-web/internal/buildinfo"
	"example.com/llm-chat-web/internal/cli"
	"example.com/llm-chat-web/internal/observability"
)

var (
	exit    = os.Exit
	execute = cli.Execute
)

func main() {
	ctx := context.Background()
	shutdown, err := observability.Init(ctx, observability.FromEnv(buildinfo.Snapshot()))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
		return
	}

	code := execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := shutdown(shutdownCtx); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	cancel()
	exit(code)
}
