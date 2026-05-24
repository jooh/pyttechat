package main

import (
	"context"
	"io"
	"os"
	"reflect"
	"testing"
)

func TestMainDelegatesToCLIExitStatus(t *testing.T) {
	originalArgs := os.Args
	originalExit := exit
	originalExecute := execute
	t.Cleanup(func() {
		os.Args = originalArgs
		exit = originalExit
		execute = originalExecute
	})

	os.Args = []string{"pyttechat", "version"}
	var gotArgs []string
	execute = func(_ context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		gotArgs = append([]string(nil), args...)
		if stdin != os.Stdin || stdout != os.Stdout || stderr != os.Stderr {
			t.Fatalf("execute received unexpected stdio")
		}
		return 7
	}
	var gotCode int
	exit = func(code int) {
		gotCode = code
	}

	main()

	if gotCode != 7 {
		t.Fatalf("exit code = %d, want 7", gotCode)
	}
	if !reflect.DeepEqual(gotArgs, []string{"version"}) {
		t.Fatalf("args = %#v, want version", gotArgs)
	}
}
