package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func runCommand(args ...string) (int, string, string) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Execute(context.Background(), args, &stdout, &stderr)

	return code, stdout.String(), stderr.String()
}

func TestAskCommandPrintsDummyResponse(t *testing.T) {
	code, stdout, stderr := runCommand("ask", "hello")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	want := "This is a dummy LLM response.\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}

	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestAskCommandRequiresPrompt(t *testing.T) {
	code, _, stderr := runCommand("ask")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}

	if !strings.Contains(stderr, "prompt is required") {
		t.Fatalf("stderr = %q, want missing prompt error", stderr)
	}

	if !strings.Contains(stderr, "Usage:") {
		t.Fatalf("stderr = %q, want usage", stderr)
	}
}

func TestAskCommandRejectsBlankPrompt(t *testing.T) {
	code, _, stderr := runCommand("ask", "  ")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}

	if !strings.Contains(stderr, "prompt must not be empty") {
		t.Fatalf("stderr = %q, want empty prompt error", stderr)
	}
}

func TestVersionCommandPrintsBuildInfo(t *testing.T) {
	code, stdout, stderr := runCommand("version")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	want := "version=dev commit=unknown date=unknown\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestNoArgsPrintsHelp(t *testing.T) {
	code, stdout, stderr := runCommand()

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	if !strings.Contains(stdout, "Minimal LLM chat backend CLI") {
		t.Fatalf("stdout = %q, want help text", stdout)
	}
}
