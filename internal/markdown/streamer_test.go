package markdown

import (
	"reflect"
	"testing"
)

func TestBlockStreamerEmitsParagraphAtBlankLine(t *testing.T) {
	var streamer BlockStreamer

	if got := streamer.Add("hello"); len(got) != 0 {
		t.Fatalf("Add incomplete paragraph = %#v, want no blocks", got)
	}
	if got := streamer.Add(" world\n\n"); !reflect.DeepEqual(got, []string{"hello world\n\n"}) {
		t.Fatalf("Add paragraph boundary = %#v, want completed paragraph", got)
	}
	if got := streamer.FullMarkdown(); got != "hello world\n\n" {
		t.Fatalf("FullMarkdown = %q, want exact input", got)
	}
}

func TestBlockStreamerEmitsMultipleBlocksFromOneDelta(t *testing.T) {
	var streamer BlockStreamer

	got := streamer.Add("one\n\ntwo\n\nthree")
	want := []string{"one\n\n", "two\n\n"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Add multiple blocks = %#v, want %#v", got, want)
	}
	if got := streamer.Flush(); !reflect.DeepEqual(got, []string{"three"}) {
		t.Fatalf("Flush = %#v, want trailing paragraph", got)
	}
}

func TestBlockStreamerBuffersFencesUntilClosed(t *testing.T) {
	var streamer BlockStreamer

	if got := streamer.Add("```go\npackage main\n"); len(got) != 0 {
		t.Fatalf("Add incomplete fence = %#v, want no blocks", got)
	}
	if got := streamer.Add("```\n"); !reflect.DeepEqual(got, []string{"```go\npackage main\n```\n"}) {
		t.Fatalf("Add closed fence = %#v, want completed fence", got)
	}
}

func TestBlockStreamerBuffersFenceWithFollowingText(t *testing.T) {
	var streamer BlockStreamer

	got := streamer.Add("```go\npackage main\n```\n\nnext\n\n")
	want := []string{"```go\npackage main\n```\n\n", "next\n\n"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Add closed fence and paragraph = %#v, want %#v", got, want)
	}
}

func TestBlockStreamerBuffersFenceAfterIntroLine(t *testing.T) {
	var streamer BlockStreamer

	if got := streamer.Add("Here is code:\n```go\nfmt.Println(1)\n\n"); len(got) != 0 {
		t.Fatalf("Add open fence after intro line = %#v, want no blocks", got)
	}
	if got := streamer.Add("```\n"); !reflect.DeepEqual(got, []string{"Here is code:\n```go\nfmt.Println(1)\n\n```\n"}) {
		t.Fatalf("Add closing fence after intro line = %#v, want completed block", got)
	}
}

func TestBlockStreamerTracksFenceMarkerAndLength(t *testing.T) {
	var streamer BlockStreamer

	if got := streamer.Add("````\nnot closed by shorter fence\n```\n"); len(got) != 0 {
		t.Fatalf("Add shorter closing fence = %#v, want no blocks", got)
	}
	if got := streamer.Add("````\n"); !reflect.DeepEqual(got, []string{"````\nnot closed by shorter fence\n```\n````\n"}) {
		t.Fatalf("Add matching closing fence = %#v, want completed fence", got)
	}

	streamer = BlockStreamer{}
	if got := streamer.Add("~~~\nnot closed by backticks\n```\n"); len(got) != 0 {
		t.Fatalf("Add mismatched closing fence = %#v, want no blocks", got)
	}
	if got := streamer.Add("~~~\n"); !reflect.DeepEqual(got, []string{"~~~\nnot closed by backticks\n```\n~~~\n"}) {
		t.Fatalf("Add matching tilde closing fence = %#v, want completed fence", got)
	}
}

func TestBlockStreamerBuffersTableUntilBlankLineOrFlush(t *testing.T) {
	var streamer BlockStreamer

	if got := streamer.Add("| A | B |\n| - | - |\n| 1 | 2 |\n"); len(got) != 0 {
		t.Fatalf("Add table before blank line = %#v, want no blocks", got)
	}
	if got := streamer.Add("\n"); !reflect.DeepEqual(got, []string{"| A | B |\n| - | - |\n| 1 | 2 |\n\n"}) {
		t.Fatalf("Add table blank line = %#v, want completed table", got)
	}

	streamer = BlockStreamer{}
	streamer.Add("| A | B |\n| - | - |\n| 1 | 2 |")
	if got := streamer.Flush(); !reflect.DeepEqual(got, []string{"| A | B |\n| - | - |\n| 1 | 2 |"}) {
		t.Fatalf("Flush table = %#v, want buffered table", got)
	}
}

func TestBlockStreamerFlushAndFullMarkdown(t *testing.T) {
	var streamer BlockStreamer

	if got := streamer.Add("alpha\n\nbeta"); !reflect.DeepEqual(got, []string{"alpha\n\n"}) {
		t.Fatalf("Add = %#v, want first block", got)
	}
	if got := streamer.Flush(); !reflect.DeepEqual(got, []string{"beta"}) {
		t.Fatalf("Flush = %#v, want beta", got)
	}
	if got := streamer.Flush(); len(got) != 0 {
		t.Fatalf("second Flush = %#v, want no blocks", got)
	}
	if got := streamer.FullMarkdown(); got != "alpha\n\nbeta" {
		t.Fatalf("FullMarkdown = %q, want exact stream", got)
	}
}
