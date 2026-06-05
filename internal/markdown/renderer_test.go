package markdown

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	gmrenderer "github.com/yuin/goldmark/renderer"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
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

func TestRendererHandlesArtifactEdgeCases(t *testing.T) {
	renderer := NewRenderer()

	for _, tc := range []struct {
		name       string
		in         string
		want       []string
		notWant    []string
		wantExact  string
		exactCheck bool
	}{
		{
			name:       "empty input",
			in:         "",
			wantExact:  "",
			exactCheck: true,
		},
		{
			name:    "unclosed directive stays literal",
			in:      ":::artifact title=\"Draft\"\n# still markdown",
			want:    []string{":::artifact", "<h1>still markdown</h1>"},
			notWant: []string{"message-artifact"},
		},
		{
			name:    "indented directive is not an artifact",
			in:      "    :::artifact\n    # code\n    :::",
			want:    []string{":::artifact", "# code"},
			notWant: []string{"message-artifact"},
		},
		{
			name: "marker collision keeps original text and artifact",
			in: `PYTTECHAT_ARTIFACT_0

:::artifact title="Collision" type="text/plain"
artifact body
:::`,
			want: []string{"PYTTECHAT_ARTIFACT_0", "Collision", "artifact body", "message-artifact"},
		},
		{
			name: "default artifact metadata",
			in: `:::artifact title=" " type=" "
plain body
:::`,
			want: []string{`data-artifact-type="text/plain"`, "Artifact", "plain body"},
		},
		{
			name: "artifact after single newline",
			in:   "Before\n:::artifact title=\"Notes\" type=\"text/plain\"\nbody\n:::\nAfter",
			want: []string{"<p>Before</p>", "Notes", "body", "<p>After</p>"},
		},
		{
			name: "mermaid artifact",
			in:   ":::artifact title=\"Flow\" type=\"application/vnd.mermaid\"\ngraph TD\nA-->B\n:::\n",
			want: []string{`data-artifact-type="application/vnd.mermaid"`, `class="language-mermaid"`, "graph TD"},
		},
		{
			name:    "unknown artifact type is escaped code",
			in:      ":::artifact title=\"HTML\" type=\"application/x-custom\"\n<script>alert(1)</script>\n:::\n",
			want:    []string{`data-artifact-type="application/x-custom"`, "&lt;script&gt;"},
			notWant: []string{"<script>"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html, err := renderer.Render(tc.in)
			if err != nil {
				t.Fatalf("Render error = %v", err)
			}
			got := string(html)
			if tc.exactCheck && got != tc.wantExact {
				t.Fatalf("Render(%q) = %q, want %q", tc.in, got, tc.wantExact)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("Render output = %q, want substring %q", got, want)
				}
			}
			for _, unwanted := range tc.notWant {
				if strings.Contains(got, unwanted) {
					t.Fatalf("Render output = %q, did not expect substring %q", got, unwanted)
				}
			}
		})
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

func TestCitationDecorationHandlesMalformedHTMLAndMarkers(t *testing.T) {
	if got := decorateCitations("<p>Broken tag 【turn1search0】"); !strings.Contains(got, `data-citation="turn1search0"`) {
		t.Fatalf("decorateCitations malformed HTML = %q, want citation decorated", got)
	}
	if got := decorateCitations("<pre>【turn1search0】</pre><code>【turn1search1】</code> visible 【turn1search2】"); strings.Count(got, `class="citation-chip"`) != 1 {
		t.Fatalf("decorateCitations code/pre = %q, want only visible citation decorated", got)
	}
	if got := citationMatches("unfinished 【turn1search0"); len(got) != 0 {
		t.Fatalf("citationMatches unfinished = %#v, want none", got)
	}
	if got := citationMatches("invalid 【turn1bad0】 then valid 【turn2file3】"); len(got) != 1 || got[0].id != "turn2file3" {
		t.Fatalf("citationMatches mixed = %#v, want turn2file3 only", got)
	}

	for _, tc := range []struct {
		tag         string
		name        string
		closing     bool
		selfClosing bool
	}{
		{tag: "x", name: ""},
		{tag: "</>", name: "", closing: true},
		{tag: "< code />", name: "code", selfClosing: true},
		{tag: "</Pre>", name: "pre", closing: true},
	} {
		name, closing, selfClosing := htmlTagName(tc.tag)
		if name != tc.name || closing != tc.closing || selfClosing != tc.selfClosing {
			t.Fatalf("htmlTagName(%q) = %q, %v, %v; want %q, %v, %v", tc.tag, name, closing, selfClosing, tc.name, tc.closing, tc.selfClosing)
		}
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

func TestRendererPropagatesConversionErrors(t *testing.T) {
	renderer := NewRenderer()
	renderer.markdown = failingMarkdown{}

	if _, err := renderer.Render("hello"); err == nil {
		t.Fatalf("Render error = nil, want conversion error")
	}
	if _, err := renderer.renderMarkdownSegment("hello"); err == nil {
		t.Fatalf("renderMarkdownSegment error = nil, want conversion error")
	}
}

func TestRendererPropagatesArtifactRenderErrors(t *testing.T) {
	renderer := NewRenderer()
	renderer.markdown = &sequenceMarkdown{
		errs: []error{nil, errors.New("artifact render failed")},
	}

	_, err := renderer.renderWithArtifacts(`:::artifact type="text/markdown"
hello
:::
`)
	if err == nil {
		t.Fatalf("renderWithArtifacts error = nil, want artifact render error")
	}
}

func TestRendererPrivateHelpersCoverParserBranches(t *testing.T) {
	if block, ok := startsFence("    ```go\n"); ok || block.open {
		t.Fatalf("startsFence indented = %#v, %v; want no fence", block, ok)
	}
	if block, ok := startsFence("``\n"); ok || block.open {
		t.Fatalf("startsFence short = %#v, %v; want no fence", block, ok)
	}
	if block, ok := startsFence("~~~\n"); !ok || !block.open || block.marker != '~' || block.length != 3 {
		t.Fatalf("startsFence tilde = %#v, %v; want open tilde fence", block, ok)
	}
	if isArtifactOpener("    :::artifact\n") {
		t.Fatalf("isArtifactOpener indented by four spaces = true, want false")
	}
	if got := artifactMarker("PYTTECHAT_ARTIFACT_0 PYTTECHAT_ARTIFACT_0_1", 0); got != "PYTTECHAT_ARTIFACT_0_2" {
		t.Fatalf("artifactMarker collision = %q, want suffixed marker", got)
	}
	if got := string(escapedCodeBlock("<tag>", "")); !strings.Contains(got, "&lt;tag&gt;") || strings.Contains(got, `class="`) {
		t.Fatalf("escapedCodeBlock without class = %q, want escaped plain code", got)
	}
	var placeholder strings.Builder
	placeholder.WriteString("before")
	writeArtifactPlaceholder(&placeholder, "MARKER")
	if got := placeholder.String(); !strings.Contains(got, "before\n\nMARKER\n\n") {
		t.Fatalf("writeArtifactPlaceholder = %q, want blank line before marker", got)
	}
	if got := decorateCitations("<broken 【turn1search2】"); !strings.Contains(got, `data-citation="turn1search2"`) {
		t.Fatalf("decorateCitations malformed tag = %q, want decorated text", got)
	}
	newSuperscriptNode().Dump([]byte("x"), 0)
	newSubscriptNode().Dump([]byte("x"), 0)
	p := newSupersubParser('^', newSuperscriptNode)
	if node := p.Parse(gast.NewDocument(), text.NewReader([]byte("plain")), parser.NewContext()); node != nil {
		t.Fatalf("supersub parser wrong marker = %#v, want nil", node)
	}
	if renderer := newSupersubHTMLRenderer(goldmarkhtml.WithUnsafe()); renderer == nil {
		t.Fatalf("newSupersubHTMLRenderer returned nil")
	}
}

type failingMarkdown struct{}

func (failingMarkdown) Convert([]byte, io.Writer, ...parser.ParseOption) error {
	return errors.New("convert failed")
}

func (failingMarkdown) Parser() parser.Parser {
	return nil
}

func (failingMarkdown) SetParser(parser.Parser) {}

func (failingMarkdown) Renderer() gmrenderer.Renderer {
	return nil
}

func (failingMarkdown) SetRenderer(gmrenderer.Renderer) {}

type sequenceMarkdown struct {
	errs  []error
	calls int
}

func (m *sequenceMarkdown) Convert([]byte, io.Writer, ...parser.ParseOption) error {
	if m.calls >= len(m.errs) {
		return nil
	}
	err := m.errs[m.calls]
	m.calls++
	return err
}

func (sequenceMarkdown) Parser() parser.Parser {
	return nil
}

func (sequenceMarkdown) SetParser(parser.Parser) {}

func (sequenceMarkdown) Renderer() gmrenderer.Renderer {
	return nil
}

func (sequenceMarkdown) SetRenderer(gmrenderer.Renderer) {}
