package markdown

import (
	"regexp"
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
			in:   `![Plot](/assets/app.css "Generated plot")`,
			want: []string{`<img src="/assets/app.css"`, `alt="Plot"`, `title="Generated plot"`},
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

func TestRendererDecoratesCitationsOnlyInTextNodes(t *testing.T) {
	renderer := NewRenderer()

	html, err := renderer.Render("[link](https://example.com \"【turn1search0】\")\n\n```text\n【turn1search1】\n```\n\nVisible 【turn1search2】.")
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	got := string(html)
	if count := strings.Count(got, `class="citation-chip"`); count != 1 {
		t.Fatalf("Render output = %q, citation chip count = %d, want only visible text citation decorated", got, count)
	}
	if !strings.Contains(got, `title="【turn1search0】"`) {
		t.Fatalf("Render output = %q, want citation marker preserved in title attribute", got)
	}
	if !strings.Contains(got, `【turn1search1】`) {
		t.Fatalf("Render output = %q, want citation marker preserved in code block", got)
	}
	if !strings.Contains(got, `data-citation="turn1search2"`) {
		t.Fatalf("Render output = %q, want visible citation decorated", got)
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

func TestRendererIgnoresArtifactDirectivesInsideFencedCode(t *testing.T) {
	renderer := NewRenderer()

	html, err := renderer.Render("```text\n:::artifact title=\"Nope\" type=\"text/markdown\"\n# Not an artifact\n:::\n```")
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	got := string(html)
	if strings.Contains(got, `message-artifact`) {
		t.Fatalf("Render output = %q, did not expect artifact rendered from fenced code", got)
	}
	if !strings.Contains(got, `:::artifact`) || !strings.Contains(got, `# Not an artifact`) {
		t.Fatalf("Render output = %q, want literal artifact directive in code", got)
	}
}

func TestRendererRendersCodeArtifactContentAsLiteralCode(t *testing.T) {
	renderer := NewRenderer()

	html, err := renderer.Render(":::artifact title=\"Code\" type=\"application/vnd.code\"\nbefore\n```\n# Not markdown\n```\nafter\n:::\n")
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	got := string(html)
	if !strings.Contains(got, `class="message-artifact"`) || !strings.Contains(got, `<pre><code>`) {
		t.Fatalf("Render output = %q, want code artifact", got)
	}
	if strings.Contains(got, `<h1>Not markdown</h1>`) {
		t.Fatalf("Render output = %q, did not expect code artifact content parsed as markdown", got)
	}
	for _, want := range []string{"before", "```", "# Not markdown", "after"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render output = %q, want literal code artifact content %q", got, want)
		}
	}
}

func TestRendererKeepsFootnoteIDsUniqueAcrossArtifacts(t *testing.T) {
	renderer := NewRenderer()

	html, err := renderer.Render(`Before[^1]

[^1]: First source

:::artifact title="Plain" type="text/plain"
plain artifact
:::

After[^2]

[^2]: Second source`)
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	got := string(html)
	idPattern := regexp.MustCompile(`id="(fn(?:ref)?:[^"]+)"`)
	seen := map[string]bool{}
	for _, match := range idPattern.FindAllStringSubmatch(got, -1) {
		if seen[match[1]] {
			t.Fatalf("Render output = %q, duplicate footnote id %q", got, match[1])
		}
		seen[match[1]] = true
	}
	if len(seen) < 4 {
		t.Fatalf("Render output = %q, want footnote refs and backlinks around artifact", got)
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
