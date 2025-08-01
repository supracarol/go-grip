package pkg

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"path"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chroma_html "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/chrishrb/go-grip/defaults"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

var blockquotes = []string{"Note", "Tip", "Important", "Warning", "Caution", "BlockQuote"}

type Parser struct {
	theme string
}

func NewParser(theme string) *Parser {
	return &Parser{
		theme: theme,
	}
}

func (m Parser) MdToHTML(bytes []byte) []byte {
	extensions := parser.NoIntraEmphasis | parser.Tables | parser.FencedCode |
		parser.Autolink | parser.Strikethrough | parser.SpaceHeadings | parser.HeadingIDs |
		parser.BackslashLineBreak | parser.MathJax | parser.OrderedListStart |
		parser.AutoHeadingIDs
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(bytes)

	htmlFlags := html.CommonFlags
	opts := html.RendererOptions{Flags: htmlFlags, RenderNodeHook: m.renderHook}
	renderer := html.NewRenderer(opts)

	return markdown.Render(doc, renderer)
}

func (m Parser) renderHook(w io.Writer, node ast.Node, entering bool) (ast.WalkStatus, bool) {
	switch node.(type) {
	case *ast.BlockQuote:
		return renderHookBlockQuote()
	case *ast.Paragraph:
		return renderHookParagraph(w, node, entering)
	case *ast.Text:
		return renderHookText(w, node)
	case *ast.ListItem:
		return renderHookListItem(w, node, entering)
	case *ast.CodeBlock:
		return renderHookCodeBlock(w, node, m.theme)
	}

	return ast.GoToNext, false
}

func renderHookCodeBlock(w io.Writer, node ast.Node, theme string) (ast.WalkStatus, bool) {
	block := node.(*ast.CodeBlock)

	if string(block.Info) == "mermaid" {
		m, err := renderMermaid(string(block.Literal), theme)
		if err != nil {
			log.Println("Error:", err)
		}
		fmt.Fprint(w, m)
		return ast.GoToNext, true
	}

	var lexer chroma.Lexer
	if block.Info == nil {
		lexer = lexers.Analyse(string(block.Literal))
	} else {
		lexer = lexers.Get(string(block.Info))
	}
	// ensure lexer is never nil
	if lexer == nil {
		lexer = lexers.Get("plaintext")
	}

	iterator, _ := lexer.Tokenise(nil, string(block.Literal))
	formatter := chroma_html.New(chroma_html.WithClasses(true))
	err := formatter.Format(w, styles.Fallback, iterator)
	if err != nil {
		log.Println("Error:", err)
	}
	return ast.GoToNext, true
}

func renderHookBlockQuote() (ast.WalkStatus, bool) {
	return ast.GoToNext, true
}

func renderHookParagraph(w io.Writer, node ast.Node, entering bool) (ast.WalkStatus, bool) {
	paragraph := node.(*ast.Paragraph)

	_, ok := paragraph.GetParent().(*ast.BlockQuote)
	if !ok {
		return ast.GoToNext, false
	}

	t, ok := (paragraph.GetChildren()[0]).(*ast.Text)
	if !ok {
		return ast.GoToNext, false
	}

	// Get the text content of the blockquote
	content := string(t.Literal)

	var alert string
	for _, b := range blockquotes {
		if strings.HasPrefix(content, fmt.Sprintf("[!%s]", strings.ToUpper(b))) {
			alert = strings.ToLower(b)
		}
	}

	if alert == "" {
		return ast.GoToNext, false
	}

	// Set the message type based on the content of the blockquote
	var err error
	if entering {
		var s string
		s, _ = createBlockquoteStart(alert)
		_, err = io.WriteString(w, s)
	} else {
		_, err = io.WriteString(w, "</div>")
	}
	if err != nil {
		log.Println("Error:", err)
	}

	return ast.GoToNext, true
}

func renderHookText(w io.Writer, node ast.Node) (ast.WalkStatus, bool) {
	block := node.(*ast.Text)

	r := regexp.MustCompile(`(:\S+:)`)
	withEmoji := r.ReplaceAllStringFunc(string(block.Literal), func(s string) string {
		val, ok := EmojiMap[s]
		if !ok {
			return s
		}

		if strings.HasPrefix(val, "/") {
			return fmt.Sprintf(`<img class="emoji" title="%s" alt="%s" src="%s" height="20" width="20" align="absmiddle">`, s, s, val)
		}

		return val
	})

	paragraph, ok := block.GetParent().(*ast.Paragraph)
	if !ok {
		_, err := io.WriteString(w, withEmoji)
		if err != nil {
			log.Println("Error:", err)
		}
		return ast.GoToNext, true
	}

	_, ok = paragraph.GetParent().(*ast.BlockQuote)
	if ok {
		// Remove prefixes
		for _, b := range blockquotes {
			content, found := strings.CutPrefix(withEmoji, fmt.Sprintf("[!%s]", strings.ToUpper(b)))
			if found {
				_, err := io.WriteString(w, content)
				if err != nil {
					log.Println("Error:", err)
				}
				return ast.GoToNext, true
			}
		}
	}

	_, ok = paragraph.GetParent().(*ast.ListItem)
	if ok {
		content, found := strings.CutPrefix(withEmoji, "[ ]")
		content = `<input type="checkbox" disabled class="task-list-item-checkbox"> ` + content
		if found {
			_, err := io.WriteString(w, content)
			if err != nil {
				log.Println("Error:", err)
			}
			return ast.GoToNext, true
		}

		content, found = strings.CutPrefix(withEmoji, "[x]")
		content = `<input type="checkbox" disabled class="task-list-item-checkbox" checked> ` + content
		if found {
			_, err := io.WriteString(w, content)
			if err != nil {
				log.Println("Error:", err)
			}
		}
	}

	_, err := io.WriteString(w, withEmoji)
	if err != nil {
		log.Println("Error:", err)
	}
	return ast.GoToNext, true
}

func renderHookListItem(w io.Writer, node ast.Node, entering bool) (ast.WalkStatus, bool) {
	block := node.(*ast.ListItem)

	paragraph, ok := (block.GetChildren()[0]).(*ast.Paragraph)
	if !ok {
		return ast.GoToNext, false
	}

	t, ok := (paragraph.GetChildren()[0]).(*ast.Text)
	if !ok {
		return ast.GoToNext, false
	}

	if !(strings.HasPrefix(string(t.Literal), "[ ]") || strings.HasPrefix(string(t.Literal), "[x]")) {
		return ast.GoToNext, false
	}

	if entering {
		_, err := io.WriteString(w, "<li class=\"task-list-item\">")
		if err != nil {
			log.Println("Error:", err)
		}
	} else {
		_, err := io.WriteString(w, "</li>")
		if err != nil {
			log.Println("Error:", err)
		}
	}

	return ast.GoToNext, true
}

func createBlockquoteStart(alert string) (string, error) {
	lp := path.Join("templates/alert", fmt.Sprintf("%s.html", alert))
	tmpl, err := template.ParseFS(defaults.Templates, lp)
	if err != nil {
		return "", err
	}
	var tpl bytes.Buffer
	if err := tmpl.Execute(&tpl, alert); err != nil {
		return "", err
	}
	return tpl.String(), nil
}

type mermaid struct {
	Content string
	Theme   string
}

func renderMermaid(content string, theme string) (string, error) {
	m := mermaid{
		Content: content,
		Theme:   theme,
	}
	lp := path.Join("templates/mermaid/mermaid.html")
	tmpl, err := template.ParseFS(defaults.Templates, lp)
	if err != nil {
		return "", err
	}
	var tpl bytes.Buffer
	if err := tmpl.Execute(&tpl, m); err != nil {
		return "", err
	}
	return tpl.String(), nil
}
