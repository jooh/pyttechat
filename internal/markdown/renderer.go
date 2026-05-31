package markdown

import (
	"bytes"
	stdhtml "html"
	"html/template"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
)

type Renderer struct {
	markdown  goldmark.Markdown
	sanitizer *bluemonday.Policy
}

func NewRenderer() *Renderer {
	return &Renderer{
		markdown: goldmark.New(
			goldmark.WithExtensions(
				extension.GFM,
				extension.Footnote,
				supersubExtension{},
				highlighting.NewHighlighting(
					highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
				),
			),
		),
		sanitizer: markdownPolicy(),
	}
}

func (r *Renderer) Render(markdown string) (template.HTML, error) {
	out, err := r.renderWithArtifacts(markdown)
	if err != nil {
		return "", err
	}
	out = decorateCitations(out)
	return template.HTML(out), nil
}

func (r *Renderer) renderMarkdownSegment(markdown string) ([]byte, error) {
	markdown = protectMathDelimiters(markdown)
	var out bytes.Buffer
	if err := r.markdown.Convert([]byte(markdown), &out); err != nil {
		return nil, err
	}
	safe := r.sanitizer.SanitizeBytes(out.Bytes())
	return []byte(restoreMathDelimiters(string(safe))), nil
}

func (r *Renderer) RenderBlock(markdown string) (template.HTML, error) {
	return r.Render(markdown)
}

func markdownPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowStandardURLs()

	policy.AllowElements(
		"p", "br",
		"h1", "h2", "h3", "h4", "h5", "h6", "hr",
		"strong", "em", "del",
		"sup", "sub",
		"a",
		"ul", "ol", "li",
		"table", "thead", "tbody", "tr", "th", "td",
		"blockquote",
		"div", "section", "header", "figure", "figcaption",
		"img",
		"pre", "code", "span",
		"input",
	)
	policy.AllowAttrs("href").OnElements("a")
	policy.AllowAttrs("title").OnElements("a")
	policy.AllowAttrs("href").Matching(regexp.MustCompile(`\A#[._:a-zA-Z0-9-]+\z`)).OnElements("a")
	policy.AllowAttrs("id").Matching(regexp.MustCompile(`\A[._:a-zA-Z0-9-]+\z`)).OnElements("a", "sup", "li", "div", "section")
	policy.AllowAttrs("role").Matching(regexp.MustCompile(`\Adoc-(?:noteref|backlink|endnotes)\z`)).OnElements("a", "div")
	policy.AllowAttrs("type").Matching(regexp.MustCompile(`\Acheckbox\z`)).OnElements("input")
	policy.AllowAttrs("checked", "disabled").OnElements("input")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`\A[-_a-zA-Z0-9 ]+\z`)).OnElements(
		"a", "div", "figcaption", "figure", "header", "pre", "code", "section", "span", "sup", "sub",
	)
	policy.AllowAttrs("src").Matching(regexp.MustCompile(`\A/(?:assets/|chat/(?:attachments|files|images)/)[^<>"'\s]*\z`)).OnElements("img")
	policy.AllowAttrs("alt", "title").OnElements("img")
	policy.AllowAttrs("width", "height").Matching(regexp.MustCompile(`\A[1-9][0-9]{0,4}\z`)).OnElements("img")
	policy.AllowAttrs("loading").Matching(regexp.MustCompile(`\Alazy\z`)).OnElements("img")
	policy.AllowAttrs("decoding").Matching(regexp.MustCompile(`\Aasync\z`)).OnElements("img")
	return policy
}

type renderSegment struct {
	markdown string
	artifact *artifactBlock
}

type artifactBlock struct {
	Title   string
	Type    string
	Content string
}

func (r *Renderer) renderWithArtifacts(markdown string) (string, error) {
	segments := splitArtifactBlocks(markdown)
	var out strings.Builder
	for _, segment := range segments {
		if segment.artifact != nil {
			html, err := r.renderArtifact(*segment.artifact)
			if err != nil {
				return "", err
			}
			out.WriteString(html)
			continue
		}
		if segment.markdown == "" {
			continue
		}
		html, err := r.renderMarkdownSegment(segment.markdown)
		if err != nil {
			return "", err
		}
		out.Write(html)
	}
	return out.String(), nil
}

func splitArtifactBlocks(input string) []renderSegment {
	lines := strings.SplitAfter(input, "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}

	var segments []renderSegment
	var normal strings.Builder
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, ":::artifact") {
			normal.WriteString(line)
			continue
		}

		var content strings.Builder
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == ":::" {
				end = j
				break
			}
			content.WriteString(lines[j])
		}
		if end < 0 {
			normal.WriteString(line)
			continue
		}

		if normal.Len() > 0 {
			segments = append(segments, renderSegment{markdown: normal.String()})
			normal.Reset()
		}
		segments = append(segments, renderSegment{artifact: parseArtifactBlock(trimmed, content.String())})
		i = end
	}
	if normal.Len() > 0 {
		segments = append(segments, renderSegment{markdown: normal.String()})
	}
	return segments
}

var artifactAttrPattern = regexp.MustCompile(`([A-Za-z][-_A-Za-z0-9]*)="([^"]*)"`)

func parseArtifactBlock(openingLine, content string) *artifactBlock {
	artifact := &artifactBlock{
		Title:   "Artifact",
		Type:    "text/plain",
		Content: content,
	}
	for _, match := range artifactAttrPattern.FindAllStringSubmatch(openingLine, -1) {
		switch strings.ToLower(match[1]) {
		case "title":
			if strings.TrimSpace(match[2]) != "" {
				artifact.Title = strings.TrimSpace(match[2])
			}
		case "type":
			if strings.TrimSpace(match[2]) != "" {
				artifact.Type = strings.TrimSpace(match[2])
			}
		}
	}
	return artifact
}

func (r *Renderer) renderArtifact(artifact artifactBlock) (string, error) {
	artifactType := strings.ToLower(strings.TrimSpace(artifact.Type))
	var body []byte
	var err error
	switch artifactType {
	case "text/markdown", "text/md":
		body, err = r.renderMarkdownSegment(artifact.Content)
	case "application/vnd.mermaid":
		body, err = r.renderMarkdownSegment("```mermaid\n" + artifact.Content + "\n```")
	case "application/vnd.code":
		body, err = r.renderMarkdownSegment("```\n" + artifact.Content + "\n```")
	default:
		body = []byte("<pre><code>" + stdhtml.EscapeString(artifact.Content) + "</code></pre>\n")
	}
	if err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString(`<section class="message-artifact" data-artifact-type="`)
	out.WriteString(stdhtml.EscapeString(artifactType))
	out.WriteString(`">`)
	out.WriteString(`<header class="message-artifact-header"><span class="message-artifact-title">`)
	out.WriteString(stdhtml.EscapeString(artifact.Title))
	out.WriteString(`</span><span class="message-artifact-type">`)
	out.WriteString(stdhtml.EscapeString(artifactType))
	out.WriteString(`</span></header>`)
	out.WriteString(`<div class="message-artifact-body">`)
	out.Write(body)
	out.WriteString(`</div></section>`)
	return out.String(), nil
}

var mathDelimiterReplacements = []struct {
	raw       string
	protected string
}{
	{`\(`, "PYTTECHAT_MATH_INLINE_OPEN"},
	{`\)`, "PYTTECHAT_MATH_INLINE_CLOSE"},
	{`\[`, "PYTTECHAT_MATH_BLOCK_OPEN"},
	{`\]`, "PYTTECHAT_MATH_BLOCK_CLOSE"},
}

func protectMathDelimiters(input string) string {
	for _, replacement := range mathDelimiterReplacements {
		input = strings.ReplaceAll(input, replacement.raw, replacement.protected)
	}
	return input
}

func restoreMathDelimiters(input string) string {
	for _, replacement := range mathDelimiterReplacements {
		input = strings.ReplaceAll(input, replacement.protected, replacement.raw)
	}
	return input
}

var citationIDPattern = regexp.MustCompile(`^turn[0-9]+(search|image|news|video|ref|file)[0-9]+$`)

type citationMatch struct {
	start int
	end   int
	id    string
}

func decorateCitations(input string) string {
	if !strings.Contains(input, "【turn") {
		return input
	}
	matches := citationMatches(input)
	if len(matches) == 0 {
		return input
	}

	var out strings.Builder
	last := 0
	for _, match := range matches {
		out.WriteString(input[last:match.start])
		out.WriteString(`<span class="citation-chip" data-citation="`)
		out.WriteString(stdhtml.EscapeString(match.id))
		out.WriteString(`" title="`)
		out.WriteString(stdhtml.EscapeString(match.id))
		out.WriteString(`">source</span>`)
		last = match.end
	}
	out.WriteString(input[last:])
	return out.String()
}

func citationMatches(text string) []citationMatch {
	var matches []citationMatch
	offset := 0
	for offset < len(text) {
		relativeStart := strings.Index(text[offset:], "【turn")
		if relativeStart < 0 {
			break
		}
		start := offset + relativeStart
		idStart := start + len("【")
		relativeEnd := strings.Index(text[idStart:], "】")
		if relativeEnd < 0 {
			break
		}
		idEnd := idStart + relativeEnd
		end := idEnd + len("】")
		id := text[idStart:idEnd]
		if citationIDPattern.MatchString(id) {
			matches = append(matches, citationMatch{start: start, end: end, id: id})
			offset = end
			continue
		}
		offset = idStart
	}
	return matches
}
