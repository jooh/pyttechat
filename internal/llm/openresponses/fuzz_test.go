package openresponses

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"testing"
)

const maxFuzzStreamSize = 64 << 10

func FuzzOpenResponsesStream(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"),
		[]byte("event: response.completed\r\ndata: {\"type\":\"response.completed\",\"response\":{}}\r\n\r\n"),
		[]byte("data: {\"type\":\ndata: \"response.completed\"}\n\n"),
		[]byte("data: [DONE]\n\n"),
		[]byte("data: {\n\n"),
		[]byte(": comment\n\n"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > maxFuzzStreamSize {
			return
		}

		body := io.NopCloser(bytes.NewReader(payload))
		stream := &stream{
			body:     body,
			reader:   bufio.NewReader(body),
			textSeen: map[string]bool{},
			ctx:      context.Background(),
		}
		maxCalls := bytes.Count(payload, []byte("\n")) + 2
		for calls := 0; ; calls++ {
			if calls > maxCalls {
				t.Fatalf("Next() did not terminate after %d calls", calls)
			}
			if _, err := stream.Next(); err != nil {
				break
			}
		}
		if err := stream.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
}
