package markdown

import (
	"bytes"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var (
	kindSuperscript = gast.NewNodeKind("Superscript")
	kindSubscript   = gast.NewNodeKind("Subscript")
)

type superscriptNode struct {
	gast.BaseInline
}

func newSuperscriptNode() gast.Node {
	return &superscriptNode{}
}

func (n *superscriptNode) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, nil, nil)
}

func (n *superscriptNode) Kind() gast.NodeKind {
	return kindSuperscript
}

type subscriptNode struct {
	gast.BaseInline
}

func newSubscriptNode() gast.Node {
	return &subscriptNode{}
}

func (n *subscriptNode) Dump(source []byte, level int) {
	gast.DumpHelper(n, source, level, nil, nil)
}

func (n *subscriptNode) Kind() gast.NodeKind {
	return kindSubscript
}

type supersubParser struct {
	marker byte
	node   func() gast.Node
}

func newSupersubParser(marker byte, node func() gast.Node) parser.InlineParser {
	return supersubParser{
		marker: marker,
		node:   node,
	}
}

func (p supersubParser) Trigger() []byte {
	return []byte{p.marker}
}

func (p supersubParser) Parse(parent gast.Node, block text.Reader, pc parser.Context) gast.Node {
	line, segment := block.PeekLine()
	if len(line) == 0 || line[0] != p.marker {
		return nil
	}
	if len(line) > 1 && line[1] == p.marker {
		return nil
	}
	closing := bytes.IndexByte(line[1:], p.marker)
	if closing < 0 {
		return nil
	}
	closing++
	content := line[1:closing]
	if len(content) == 0 || bytes.ContainsAny(content, " \t\r\n") {
		return nil
	}

	node := p.node()
	child := gast.NewTextSegment(text.NewSegment(segment.Start+1, segment.Start+closing))
	node.AppendChild(node, child)
	block.Advance(closing + 1)
	return node
}

type supersubHTMLRenderer struct {
	html.Config
}

func newSupersubHTMLRenderer(opts ...html.Option) renderer.NodeRenderer {
	r := &supersubHTMLRenderer{Config: html.NewConfig()}
	for _, opt := range opts {
		opt.SetHTMLOption(&r.Config)
	}
	return r
}

func (r *supersubHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindSuperscript, r.renderSuperscript)
	reg.Register(kindSubscript, r.renderSubscript)
}

func (r *supersubHTMLRenderer) renderSuperscript(w util.BufWriter, source []byte, n gast.Node, entering bool) (gast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<sup>")
	} else {
		_, _ = w.WriteString("</sup>")
	}
	return gast.WalkContinue, nil
}

func (r *supersubHTMLRenderer) renderSubscript(w util.BufWriter, source []byte, n gast.Node, entering bool) (gast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<sub>")
	} else {
		_, _ = w.WriteString("</sub>")
	}
	return gast.WalkContinue, nil
}

type supersubExtension struct{}

func (supersubExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithInlineParsers(
		util.Prioritized(newSupersubParser('^', newSuperscriptNode), 400),
		util.Prioritized(newSupersubParser('~', newSubscriptNode), 400),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(newSupersubHTMLRenderer(), 500),
	))
}
