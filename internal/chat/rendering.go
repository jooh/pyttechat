package chat

import "strings"

var webRenderingInstructions = strings.Join([]string{
	"The client renders assistant responses as sanitized Markdown. Use concise Markdown that works well in a chat transcript.",
	"",
	"Supported inline output:",
	"- GFM tables, task lists, headings, lists, blockquotes, links, inline code, and fenced code.",
	"- Fenced code blocks with language identifiers for syntax highlighting.",
	"- Math with \\(...\\) for inline math and $$...$$ or \\[...\\] for display math. Do not use single-dollar inline math.",
	"- Mermaid diagrams in fenced code blocks when a diagram helps:",
	"  ```mermaid",
	"  graph TD",
	"    A --> B",
	"  ```",
	"",
	"Artifacts are available for substantial reusable content only. Prefer normal inline Markdown for short answers and examples. Use this directive shape:",
	"",
	":::artifact title=\"Short title\" type=\"text/markdown\"",
	"content",
	":::",
	"",
	"Supported artifact types are text/markdown, text/md, application/vnd.mermaid, application/vnd.code, and plain text fallback. For Mermaid artifacts, put raw Mermaid source inside the artifact without surrounding backticks.",
	"",
	"Do not output or claim support for raw HTML execution, React components, JavaScript apps, SVG rendering, external images, invented file IDs, invented citations, or direct downloads. If tools provide citation markers like 【turn1search0】, preserve them; do not invent them.",
}, "\n")

func WebRenderingInstructions() string {
	return webRenderingInstructions
}
