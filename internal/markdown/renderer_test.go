package markdown

import (
	"strings"
	"testing"
)

func TestRendererRenderMarkdownAndGFM(t *testing.T) {
	renderer := NewRenderer()

	for _, tc := range []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "paragraphs",
			in:   "hello **world**",
			want: []string{"<p>hello <strong>world</strong></p>"},
		},
		{
			name: "tables",
			in:   "| A | B |\n| - | - |\n| 1 | 2 |",
			want: []string{"<table>", "<th>A</th>", "<td>2</td>"},
		},
		{
			name: "task lists",
			in:   "- [x] done\n- [ ] todo",
			want: []string{`type="checkbox"`, `checked`, `disabled`, "done"},
		},
		{
			name: "fenced highlighted code",
			in:   "```go\npackage main\n```",
			want: []string{`class="chroma"`, `package`, `class="`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html, err := renderer.Render(tc.in)
			if err != nil {
				t.Fatalf("Render error = %v", err)
			}
			got := string(html)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("Render(%q) = %q, want substring %q", tc.in, got, want)
				}
			}
		})
	}
}

func TestRendererSanitizesUnsafeModelHTML(t *testing.T) {
	renderer := NewRenderer()

	html, err := renderer.Render(`<script>alert(1)</script>

[safe](https://example.com)

<a href="javascript:alert(1)" onclick="bad">bad</a>`)
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	got := string(html)
	for _, unsafe := range []string{"<script", "alert(1)", "onclick", "javascript:"} {
		if strings.Contains(got, unsafe) {
			t.Fatalf("Render output = %q, did not expect unsafe substring %q", got, unsafe)
		}
	}
	if !strings.Contains(got, `href="https://example.com"`) {
		t.Fatalf("Render output = %q, want safe markdown link", got)
	}
}

func TestRendererFencedCodeKeepsPreCodeShapeForClientCopyControls(t *testing.T) {
	renderer := NewRenderer()

	html, err := renderer.Render("```go\nfmt.Println(1)\n```")
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	got := string(html)
	for _, want := range []string{"<pre", "<code", "fmt", "Println"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render output = %q, want code-block substring %q", got, want)
		}
	}
}

func TestRendererRenderBlockUsesSamePolicy(t *testing.T) {
	renderer := NewRenderer()

	html, err := renderer.RenderBlock("hello `code`")
	if err != nil {
		t.Fatalf("RenderBlock error = %v", err)
	}
	if got := string(html); !strings.Contains(got, "<code>code</code>") {
		t.Fatalf("RenderBlock = %q, want inline code", got)
	}
}
