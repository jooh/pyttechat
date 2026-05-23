package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func runCommand(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("PYTTECHAT_LLM_PROXY_URL", "")
	t.Setenv("PYTTECHAT_MODEL", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Execute(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)

	return code, stdout.String(), stderr.String()
}

func TestAskCommandStreamsAnswerToStdoutAndReasoningToStderr(t *testing.T) {
	code, stdout, stderr := runCommand(t, "", "ask", "hello")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	wantStdout := "This is a dummy LLM response.\n"
	if stdout != wantStdout {
		t.Fatalf("stdout = %q, want %q", stdout, wantStdout)
	}

	if !strings.Contains(stderr, "Thinking") {
		t.Fatalf("stderr = %q, want streamed reasoning", stderr)
	}
}

func TestChatCommandKeepsOneEphemeralSession(t *testing.T) {
	code, stdout, stderr := runCommand(t, "first\nsecond\n", "chat")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	if strings.Count(stdout, "This is a dummy LLM response.") != 2 {
		t.Fatalf("stdout = %q, want two streamed answers", stdout)
	}

	if strings.Count(stderr, "Thinking") != 2 {
		t.Fatalf("stderr = %q, want two streamed reasoning blocks", stderr)
	}
}

func TestAskCommandRequiresPrompt(t *testing.T) {
	code, _, stderr := runCommand(t, "", "ask")

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
	code, _, stderr := runCommand(t, "", "ask", "  ")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}

	if !strings.Contains(stderr, "prompt must not be empty") {
		t.Fatalf("stderr = %q, want empty prompt error", stderr)
	}
}

func TestVersionCommandPrintsBuildInfo(t *testing.T) {
	code, stdout, stderr := runCommand(t, "", "version")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	want := "version=dev commit=unknown date=unknown\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestNoArgsPrintsHelp(t *testing.T) {
	code, stdout, stderr := runCommand(t, "")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr)
	}

	if !strings.Contains(stdout, "Minimal LLM chat backend CLI") {
		t.Fatalf("stdout = %q, want help text", stdout)
	}
}
