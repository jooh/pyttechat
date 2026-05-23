package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/llm-chat-web/internal/llm/openresponses/fakeprovider"
)

func main() {
	os.Exit(run())
}

func run() int {
	addr := flag.String("addr", ":8080", "address for the fake Responses API provider")
	flag.Parse()

	server := &http.Server{
		Addr:              *addr,
		Handler:           fakeprovider.NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		errc <- server.ListenAndServe()
	}()

	fmt.Fprintf(os.Stderr, "fake Responses API listening on %s\n", *addr)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigc)

	select {
	case err := <-errc:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	case <-sigc:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	return 0
}
