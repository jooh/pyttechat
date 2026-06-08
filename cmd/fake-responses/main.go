package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/llm-chat-web/internal/buildinfo"
	"example.com/llm-chat-web/internal/llm/openresponses/fakeprovider"
	"example.com/llm-chat-web/internal/observability"
)

type fakeResponsesServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

var (
	exit       = os.Exit
	runCommand = run
	newServer  = func(addr string, opts fakeprovider.Options) fakeResponsesServer {
		return &http.Server{
			Addr:              addr,
			Handler:           observability.HTTPMiddleware(fakeprovider.NewHandlerWithOptions(opts)),
			ReadHeaderTimeout: 5 * time.Second,
		}
	}
	signalChannel = func() (chan os.Signal, func()) {
		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
		return sigc, func() { signal.Stop(sigc) }
	}
)

func main() {
	exit(runCommand(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) (code int) {
	flags := flag.NewFlagSet("fake-responses", flag.ContinueOnError)
	flags.SetOutput(stderr)
	addr := flags.String("addr", ":8080", "address for the fake Responses API provider")
	streamDelay := flags.Duration("stream-delay", 150*time.Millisecond, "delay between streamed fake Responses API events")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	shutdown, err := observability.Init(context.Background(), observability.FromEnv(buildinfo.Snapshot()))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil && code == 0 {
			fmt.Fprintln(stderr, err)
			code = 1
		}
	}()

	server := newServer(*addr, fakeprovider.Options{StreamDelay: *streamDelay})
	errc := make(chan error, 1)
	go func() {
		errc <- server.ListenAndServe()
	}()

	fmt.Fprintf(stderr, "fake Responses API listening on %s\n", *addr)

	sigc, stop := signalChannel()
	defer stop()

	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(stderr, err)
			return 1
		}
	case <-sigc:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	return 0
}
