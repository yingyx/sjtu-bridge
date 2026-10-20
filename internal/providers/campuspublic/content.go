package campuspublic

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"golang.org/x/net/html"
	"strings"
)

// Source text limits alone do not bound encoded output: JSON HTML escaping
// can expand one byte to six. Leave room for the response envelope and mark
// every discarded prefix/link explicitly instead of returning a size failure.
func boundPublicContent(v *contracts.Item) {
	if v.Content == nil {
		return
	}
	for {
		raw, _ := json.Marshal(v)
		if len(raw) <= contracts.MaxJSONBytes-4096 {
			return
		}
		v.Content.Truncated = true
		if v.Content.Text != "" {
			end := len(v.Content.Text) * 3 / 4
			for end > 0 && !utf8.RuneStart(v.Content.Text[end]) {
				end--
			}
			v.Content.Text = v.Content.Text[:end]
		} else if len(v.Content.Links) > 0 {
			v.Content.Links = v.Content.Links[:len(v.Content.Links)-1]
		} else {
			return
		}
	}
}

// publicText preserves inline words and block boundaries; it never treats
// source text as executable instructions or exposes embedded forms/scripts.
func publicText(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "svg", "noscript", "form", "iframe", "object", "embed", "input", "button":
				return
			case "p", "div", "li", "br", "tr":
				b.WriteByte('\n')
			case "td", "th":
				b.WriteByte('\t')
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "div" || n.Data == "li" || n.Data == "tr") {
			b.WriteByte('\n')
		}
	}
	visit(n)
	var lines []string
	for _, line := range strings.Split(b.String(), "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
