package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestMainDelegatesToRunAndExit(t *testing.T) {
	originalArgs := os.Args
	originalExit := exit
	originalRun := runCommand
	t.Cleanup(func() {
		os.Args = originalArgs
		exit = originalExit
		runCommand = originalRun
	})

	os.Args = []string{"fake-responses", "--addr", "127.0.0.1:0"}
	var gotArgs []string
	runCommand = func(args []string, stderr io.Writer) int {
		gotArgs = append([]string(nil), args...)
		if stderr != os.Stderr {
			t.Fatalf("stderr = %#v, want os.Stderr", stderr)
		}
		return 9
	}
	var gotCode int
	exit = func(code int) {
		gotCode = code
	}

	main()

	if gotCode != 9 {
		t.Fatalf("exit code = %d, want 9", gotCode)
	}
	if !reflect.DeepEqual(gotArgs, []string{"--addr", "127.0.0.1:0"}) {
		t.Fatalf("args = %#v, want addr args", gotArgs)
	}
}

func TestRunReturnsInvalidFlagStatus(t *testing.T) {
	var stderr bytes.Buffer

	code := run([]string{"--unknown"}, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr = %q, want flag parse error", stderr.String())
	}
}

func TestRunReturnsHelpStatus(t *testing.T) {
	var stderr bytes.Buffer

	code := run([]string{"--help"}, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "Usage of fake-responses") {
		t.Fatalf("stderr = %q, want help usage", stderr.String())
	}
}

func TestRunHandlesListenFailure(t *testing.T) {
	withFakeResponseServer(t, &stubFakeResponsesServer{listenErr: errors.New("listen failed")})
	withSignalChannel(t, make(chan os.Signal))
	var stderr bytes.Buffer

	code := run([]string{"--addr", "127.0.0.1:0"}, &stderr)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "listen failed") {
		t.Fatalf("stderr = %q, want listen failure", stderr.String())
	}
}

func TestRunHandlesNormalShutdownAndShutdownFailure(t *testing.T) {
	for _, tc := range []struct {
		name        string
		shutdownErr error
		wantCode    int
	}{
		{name: "normal shutdown", wantCode: 0},
		{name: "shutdown failure", shutdownErr: errors.New("shutdown failed"), wantCode: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &stubFakeResponsesServer{shutdownErr: tc.shutdownErr}
			withFakeResponseServer(t, server)
			sigc := make(chan os.Signal, 1)
			sigc <- os.Interrupt
			withSignalChannel(t, sigc)
			var stderr bytes.Buffer

			code := run([]string{"--addr", "127.0.0.1:0"}, &stderr)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr = %q", code, tc.wantCode, stderr.String())
			}
			if server.shutdowns != 1 {
				t.Fatalf("shutdown count = %d, want 1", server.shutdowns)
			}
			if tc.shutdownErr != nil && !strings.Contains(stderr.String(), "shutdown failed") {
				t.Fatalf("stderr = %q, want shutdown failure", stderr.String())
			}
		})
	}
}

func TestRunTreatsServerClosedAsSuccess(t *testing.T) {
	withFakeResponseServer(t, &stubFakeResponsesServer{listenErr: http.ErrServerClosed})
	withSignalChannel(t, make(chan os.Signal))
	var stderr bytes.Buffer

	code := run([]string{"--addr", "127.0.0.1:0"}, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
}

func TestDefaultFactories(t *testing.T) {
	server := newServer("127.0.0.1:0")
	httpServer, ok := server.(*http.Server)
	if !ok {
		t.Fatalf("newServer type = %T, want *http.Server", server)
	}
	if httpServer.Addr != "127.0.0.1:0" || httpServer.Handler == nil {
		t.Fatalf("server = %#v, want configured fake provider server", httpServer)
	}

	sigc, stop := signalChannel()
	stop()
	if sigc == nil {
		t.Fatalf("signalChannel returned nil channel")
	}
}

func withFakeResponseServer(t *testing.T, server *stubFakeResponsesServer) {
	t.Helper()

	if server.done == nil {
		server.done = make(chan struct{})
	}
	original := newServer
	newServer = func(string) fakeResponsesServer {
		return server
	}
	t.Cleanup(func() {
		newServer = original
	})
}

func withSignalChannel(t *testing.T, sigc chan os.Signal) {
	t.Helper()

	original := signalChannel
	signalChannel = func() (chan os.Signal, func()) {
		return sigc, func() {}
	}
	t.Cleanup(func() {
		signalChannel = original
	})
}

type stubFakeResponsesServer struct {
	listenErr   error
	shutdownErr error
	done        chan struct{}
	once        sync.Once
	shutdowns   int
}

func (s *stubFakeResponsesServer) ListenAndServe() error {
	if s.listenErr != nil {
		return s.listenErr
	}
	<-s.done
	return http.ErrServerClosed
}

func (s *stubFakeResponsesServer) Shutdown(context.Context) error {
	s.shutdowns++
	s.once.Do(func() {
		close(s.done)
	})
	return s.shutdownErr
}
