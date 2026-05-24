package markdown

import (
	"bytes"
	"html/template"
	"regexp"

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
				highlighting.NewHighlighting(
					highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
				),
			),
		),
		sanitizer: markdownPolicy(),
	}
}

func (r *Renderer) Render(markdown string) (template.HTML, error) {
	var out bytes.Buffer
	if err := r.markdown.Convert([]byte(markdown), &out); err != nil {
		return "", err
	}
	return template.HTML(r.sanitizer.SanitizeBytes(out.Bytes())), nil
}

func (r *Renderer) RenderBlock(markdown string) (template.HTML, error) {
	return r.Render(markdown)
}

func markdownPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowStandardURLs()

	policy.AllowElements(
		"p", "br",
		"strong", "em", "del",
		"a",
		"ul", "ol", "li",
		"table", "thead", "tbody", "tr", "th", "td",
		"blockquote",
		"pre", "code", "span",
		"input",
	)
	policy.AllowAttrs("href").OnElements("a")
	policy.AllowAttrs("title").OnElements("a")
	policy.AllowAttrs("type").Matching(regexp.MustCompile(`\Acheckbox\z`)).OnElements("input")
	policy.AllowAttrs("checked", "disabled").OnElements("input")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`\A[-_a-zA-Z0-9 ]+\z`)).OnElements("pre", "code", "span")
	return policy
}
