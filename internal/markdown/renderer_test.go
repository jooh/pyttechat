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
		{
			name: "headings and horizontal rules",
			in:   "# Heading\n\n---",
			want: []string{"<h1>Heading</h1>", "<hr"},
		},
		{
			name: "footnotes",
			in:   "Fact[^1]\n\n[^1]: Source",
			want: []string{`class="footnote-ref"`, `role="doc-endnotes"`, "Source"},
		},
		{
			name: "superscript and subscript",
			in:   "E = mc^2^ and H~2~O, not ~~deleted~~",
			want: []string{"mc<sup>2</sup>", "H<sub>2</sub>O", "<del>deleted</del>"},
		},
		{
			name: "safe local images",
			in:   `![Plot](/chat/files/file_123 "Generated plot")`,
			want: []string{`<img src="/chat/files/file_123"`, `alt="Plot"`, `title="Generated plot"`},
		},
		{
			name: "math delimiters survive for client rendering",
			in:   `Inline \(x^2\) and block: $$\int_0^1 x dx$$`,
			want: []string{`\(x^2\)`, `$$\int_0^1 x dx$$`},
		},
		{
			name: "mermaid fenced code keeps language marker",
			in:   "```mermaid\ngraph TD\n  A-->B\n```",
			want: []string{`language-mermaid`, "graph", "A", "B"},
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

func TestRendererRendersSafeArtifactDirectives(t *testing.T) {
	renderer := NewRenderer()

	html, err := renderer.Render(`Before

:::artifact title="Notes" type="text/markdown"
# Artifact Heading

Artifact body.
:::

After`)
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	got := string(html)
	for _, want := range []string{
		`class="message-artifact"`,
		`data-artifact-type="text/markdown"`,
		`Notes`,
		`<h1>Artifact Heading</h1>`,
		`Artifact body.`,
		`<p>Before</p>`,
		`<p>After</p>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render output = %q, want artifact substring %q", got, want)
		}
	}
}

func TestRendererDecoratesCitationMarkers(t *testing.T) {
	renderer := NewRenderer()

	html, err := renderer.Render(`Answer cites 【turn1search0】 and keeps unknown 【not-a-source】.`)
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	got := string(html)
	if !strings.Contains(got, `class="citation-chip"`) || !strings.Contains(got, `data-citation="turn1search0"`) {
		t.Fatalf("Render output = %q, want source citation chip", got)
	}
	if !strings.Contains(got, `【not-a-source】`) {
		t.Fatalf("Render output = %q, want unknown marker preserved as text", got)
	}
}

func TestCitationMatchesFindsSourceMarkers(t *testing.T) {
	matches := citationMatches("Answer 【turn1search0】.")
	if len(matches) != 1 || matches[0].id != "turn1search0" {
		t.Fatalf("citationMatches = %#v, want turn1search0", matches)
	}
	if got := decorateCitations("<p>Answer 【turn1search0】.</p>\n"); !strings.Contains(got, `class="citation-chip"`) {
		t.Fatalf("decorateCitations = %q, want citation chip", got)
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

func TestRendererSanitizesUnsafeImagesAndAttributes(t *testing.T) {
	renderer := NewRenderer()

	html, err := renderer.Render(`![tracker](https://tracker.example/pixel.png)

![svg](data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9YWxlcnQoMSk+)

<img src="/chat/files/file_123" onerror="alert(1)" alt="raw">`)
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	got := string(html)
	for _, unsafe := range []string{`src="https://tracker.example`, `data:image/svg`, "onerror", "alert(1)"} {
		if strings.Contains(got, unsafe) {
			t.Fatalf("Render output = %q, did not expect unsafe image substring %q", got, unsafe)
		}
	}
	if strings.Contains(got, `<img src="/chat/files/file_123"`) {
		t.Fatalf("Render output = %q, did not expect raw HTML image to be trusted", got)
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
