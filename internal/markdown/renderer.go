package markdown

import (
	"bytes"
	stdhtml "html"
	"html/template"
	"regexp"
	"strconv"
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
	out = r.sanitizer.Sanitize(out)
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
	policy.AllowAttrs("data-citation").Matching(citationIDPattern).OnElements("span")
	policy.AllowAttrs("data-artifact-type").Matching(regexp.MustCompile(`\A[-+./_a-zA-Z0-9]+\z`)).OnElements("section")
	policy.AllowAttrs("title").Matching(citationIDPattern).OnElements("span")
	policy.AllowAttrs("type").Matching(regexp.MustCompile(`\Acheckbox\z`)).OnElements("input")
	policy.AllowAttrs("checked", "disabled").OnElements("input")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`\A[-_a-zA-Z0-9 ]+\z`)).OnElements(
		"a", "div", "figcaption", "figure", "header", "pre", "code", "section", "span", "sup", "sub",
	)
	policy.AllowAttrs("src").Matching(regexp.MustCompile(`\A/assets/[^<>"'\s]*\z`)).OnElements("img")
	policy.AllowAttrs("alt", "title").OnElements("img")
	policy.AllowAttrs("width", "height").Matching(regexp.MustCompile(`\A[1-9][0-9]{0,4}\z`)).OnElements("img")
	policy.AllowAttrs("loading").Matching(regexp.MustCompile(`\Alazy\z`)).OnElements("img")
	policy.AllowAttrs("decoding").Matching(regexp.MustCompile(`\Aasync\z`)).OnElements("img")
	return policy
}

type artifactBlock struct {
	Title   string
	Type    string
	Content string
}

type artifactPlaceholder struct {
	marker   string
	artifact *artifactBlock
}

type artifactExtraction struct {
	markdown  string
	artifacts []artifactPlaceholder
}

func (r *Renderer) renderWithArtifacts(markdown string) (string, error) {
	extracted := extractArtifactBlocks(markdown)
	html, err := r.renderMarkdownSegment(extracted.markdown)
	if err != nil {
		return "", err
	}

	out := string(html)
	for _, placeholder := range extracted.artifacts {
		artifactHTML, err := r.renderArtifact(*placeholder.artifact)
		if err != nil {
			return "", err
		}
		out = replaceArtifactPlaceholder(out, placeholder.marker, artifactHTML)
	}
	return out, nil
}

func extractArtifactBlocks(input string) artifactExtraction {
	lines := strings.SplitAfter(input, "\n")
	if len(lines) == 1 && lines[0] == "" {
		return artifactExtraction{}
	}

	var markdown strings.Builder
	var artifacts []artifactPlaceholder
	var fence fencedCodeBlock
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if fence.open {
			markdown.WriteString(line)
			if closesFence(line, fence) {
				fence.open = false
			}
			continue
		}
		if nextFence, ok := startsFence(line); ok {
			fence = nextFence
			markdown.WriteString(line)
			continue
		}
		if !isArtifactOpener(line) {
			markdown.WriteString(line)
			continue
		}

		var content strings.Builder
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if isArtifactCloser(lines[j]) {
				end = j
				break
			}
			content.WriteString(lines[j])
		}
		if end < 0 {
			markdown.WriteString(line)
			continue
		}

		marker := artifactMarker(input, len(artifacts))
		writeArtifactPlaceholder(&markdown, marker)
		artifacts = append(artifacts, artifactPlaceholder{
			marker:   marker,
			artifact: parseArtifactBlock(strings.TrimSpace(line), content.String()),
		})
		i = end
	}
	return artifactExtraction{markdown: markdown.String(), artifacts: artifacts}
}

type fencedCodeBlock struct {
	open   bool
	marker byte
	length int
}

func startsFence(line string) (fencedCodeBlock, bool) {
	indent, rest := leadingSpaces(line)
	if indent > 3 || len(rest) < 3 {
		return fencedCodeBlock{}, false
	}
	marker := rest[0]
	if marker != '`' && marker != '~' {
		return fencedCodeBlock{}, false
	}
	length := 0
	for length < len(rest) && rest[length] == marker {
		length++
	}
	if length < 3 {
		return fencedCodeBlock{}, false
	}
	return fencedCodeBlock{open: true, marker: marker, length: length}, true
}

func closesFence(line string, fence fencedCodeBlock) bool {
	indent, rest := leadingSpaces(line)
	if indent > 3 || len(rest) < fence.length || rest[0] != fence.marker {
		return false
	}
	length := 0
	for length < len(rest) && rest[length] == fence.marker {
		length++
	}
	return length >= fence.length && strings.TrimSpace(rest[length:]) == ""
}

func isArtifactOpener(line string) bool {
	indent, rest := leadingSpaces(line)
	if indent > 3 {
		return false
	}
	trimmed := strings.TrimSpace(rest)
	return trimmed == ":::artifact" || strings.HasPrefix(trimmed, ":::artifact ")
}

func isArtifactCloser(line string) bool {
	indent, rest := leadingSpaces(line)
	return indent <= 3 && strings.TrimSpace(rest) == ":::"
}

func leadingSpaces(line string) (int, string) {
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	return spaces, line[spaces:]
}

func artifactMarker(input string, index int) string {
	marker := "PYTTECHAT_ARTIFACT_" + strconv.Itoa(index)
	for suffix := 1; strings.Contains(input, marker); suffix++ {
		marker = "PYTTECHAT_ARTIFACT_" + strconv.Itoa(index) + "_" + strconv.Itoa(suffix)
	}
	return marker
}

func writeArtifactPlaceholder(out *strings.Builder, marker string) {
	if out.Len() > 0 {
		current := out.String()
		switch {
		case strings.HasSuffix(current, "\n\n"):
		case strings.HasSuffix(current, "\n"):
			out.WriteByte('\n')
		default:
			out.WriteString("\n\n")
		}
	}
	out.WriteString(marker)
	out.WriteString("\n\n")
}

func replaceArtifactPlaceholder(input, marker, html string) string {
	for _, target := range []string{"<p>" + marker + "</p>\n", "<p>" + marker + "</p>"} {
		input = strings.ReplaceAll(input, target, html)
	}
	return strings.ReplaceAll(input, marker, html)
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
		body = escapedCodeBlock(artifact.Content, "language-mermaid")
	case "application/vnd.code":
		body = escapedCodeBlock(artifact.Content, "")
	default:
		body = escapedCodeBlock(artifact.Content, "")
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

func escapedCodeBlock(content, className string) []byte {
	var out strings.Builder
	out.WriteString("<pre><code")
	if className != "" {
		out.WriteString(` class="`)
		out.WriteString(stdhtml.EscapeString(className))
		out.WriteString(`"`)
	}
	out.WriteString(">")
	out.WriteString(stdhtml.EscapeString(content))
	out.WriteString("</code></pre>\n")
	return []byte(out.String())
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
	var out strings.Builder
	skipDepth := 0
	for offset := 0; offset < len(input); {
		if input[offset] == '<' {
			end := strings.IndexByte(input[offset:], '>')
			if end < 0 {
				out.WriteString(decorateCitationText(input[offset:]))
				break
			}
			end += offset + 1
			tag := input[offset:end]
			updateCitationSkipDepth(tag, &skipDepth)
			out.WriteString(tag)
			offset = end
			continue
		}

		nextTag := strings.IndexByte(input[offset:], '<')
		end := len(input)
		if nextTag >= 0 {
			end = offset + nextTag
		}
		text := input[offset:end]
		if skipDepth > 0 {
			out.WriteString(text)
		} else {
			out.WriteString(decorateCitationText(text))
		}
		offset = end
	}
	return out.String()
}

func decorateCitationText(input string) string {
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

func updateCitationSkipDepth(tag string, skipDepth *int) {
	name, closing, selfClosing := htmlTagName(tag)
	if name != "pre" && name != "code" {
		return
	}
	if closing {
		if *skipDepth > 0 {
			*skipDepth--
		}
		return
	}
	if !selfClosing {
		*skipDepth++
	}
}

func htmlTagName(tag string) (name string, closing bool, selfClosing bool) {
	if len(tag) < 3 || tag[0] != '<' {
		return "", false, false
	}
	i := 1
	if tag[i] == '/' {
		closing = true
		i++
	}
	for i < len(tag) && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\n' || tag[i] == '\r') {
		i++
	}
	start := i
	for i < len(tag) {
		ch := tag[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' {
			i++
			continue
		}
		break
	}
	if start == i {
		return "", closing, false
	}
	return strings.ToLower(tag[start:i]), closing, strings.HasSuffix(strings.TrimSpace(tag), "/>")
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
