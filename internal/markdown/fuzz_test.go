package markdown

import (
	"strings"
	"testing"
)

const maxFuzzInputSize = 64 << 10

func FuzzRendererRender(f *testing.F) {
	for _, seed := range []string{
		"hello **world**",
		"<script>alert(1)</script><img src=x onerror=alert(2)>",
		":::artifact title=\"Example\" type=\"text/plain\"\ncontent\n:::\n",
		"```go\nfmt.Println(\"hello\")\n```\n",
		"Citation [turn1search0] and math $x^2$.",
		"\x00\xff\xfe",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > maxFuzzInputSize {
			return
		}

		renderer := NewRenderer()
		first, err := renderer.Render(input)
		if err != nil {
			t.Fatalf("Render() error = %v", err)
		}
		second, err := NewRenderer().Render(input)
		if err != nil {
			t.Fatalf("second Render() error = %v", err)
		}
		if first != second {
			t.Fatalf("Render() is nondeterministic: first = %q, second = %q", first, second)
		}

		sanitized := renderer.sanitizer.SanitizeBytes([]byte(first))
		if string(first) != string(sanitized) {
			t.Fatalf("Render() output is not sanitizer-idempotent: output = %q, sanitized = %q", first, sanitized)
		}
	})
}

func FuzzBlockStreamerChunking(f *testing.F) {
	f.Add("first\n\nsecond\n", []byte{1, 2, 3})
	f.Add("```go\nfmt.Println(1)\n```\n\nnext", []byte{4, 1, 8})
	f.Add("alpha\r\n\r\nbeta", []byte{2, 5})
	f.Add("", []byte{})

	f.Fuzz(func(t *testing.T, input string, chunkSizes []byte) {
		if len(input) > maxFuzzInputSize || len(chunkSizes) > maxFuzzInputSize {
			return
		}

		var streamer BlockStreamer
		var emitted strings.Builder
		offset := 0
		for _, rawSize := range chunkSizes {
			if offset == len(input) {
				break
			}
			size := 1 + int(rawSize)%(len(input)-offset)
			for _, block := range streamer.Add(input[offset : offset+size]) {
				emitted.WriteString(block)
			}
			offset += size
		}
		if offset < len(input) {
			for _, block := range streamer.Add(input[offset:]) {
				emitted.WriteString(block)
			}
		}
		for _, block := range streamer.Flush() {
			emitted.WriteString(block)
		}

		if got := emitted.String(); got != input {
			t.Fatalf("emitted content = %q, want %q", got, input)
		}
		if got := streamer.FullMarkdown(); got != input {
			t.Fatalf("FullMarkdown() = %q, want %q", got, input)
		}
		if blocks := streamer.Flush(); len(blocks) != 0 {
			t.Fatalf("second Flush() emitted %q, want no blocks", blocks)
		}
	})
}
