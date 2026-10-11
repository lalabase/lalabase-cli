package inboxfixture

import (
	"html"
	"strconv"
	"strings"
)

// The text of an HTML body as the server reads it back: body_text of a
// comment posted without format is ActionText's plain text of the stored
// markup (ActionText::PlainTextConversion of the core's Rails 8.1.4,
// pinned by TestAnHTMLBodyBecomesTextAsActionTextDoes). The markup is
// parsed as libxml2 parses what a client could send: elements that open
// and close, void elements, comments, a script's raw content, an open tag
// at the end (dropped), a stray closing tag of a block (an empty block, as
// libxml2 repairs it). Other repairs of broken markup are not mirrored.

type htmlNode struct {
	name     string // empty for text
	text     string
	parent   *htmlNode
	children []*htmlNode
}

// voidElements have no content and no closing tag.
var voidElements = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}

// rawElements keep their content as text until their closing tag.
var rawElements = map[string]bool{"script": true, "style": true}

// blockElements are what a stray closing tag stands for.
var blockElements = map[string]bool{"p": true, "div": true, "h1": true}

// htmlText is body_text of an HTML body. ActionText strips the markup
// (String#strip) before it parses it.
func htmlText(body string) string {
	return strings.TrimRight(plainTextOf(parseHTML(rubyStrip(body)), 0), "\n")
}

// parseHTML is the tree of the markup, with text nodes unescaped.
func parseHTML(s string) *htmlNode {
	root := &htmlNode{name: "#root"}
	open := root
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			open.children = append(open.children, &htmlNode{text: html.UnescapeString(text.String()), parent: open})
			text.Reset()
		}
	}
	for i := 0; i < len(s); {
		if s[i] != '<' {
			text.WriteByte(s[i])
			i++
			continue
		}
		rest := s[i+1:]
		switch {
		case strings.HasPrefix(rest, "!--"):
			flush()
			end := strings.Index(rest, "-->")
			if end < 0 {
				return root
			}
			i += 1 + end + 3
		case len(rest) > 0 && (rest[0] == '!' || rest[0] == '?'):
			flush()
			end := strings.IndexByte(rest, '>')
			if end < 0 {
				return root
			}
			i += 1 + end + 1
		case len(rest) > 0 && rest[0] == '/':
			flush()
			end := strings.IndexByte(rest, '>')
			if end < 0 {
				return root
			}
			name := strings.ToLower(strings.TrimSpace(rest[1:end]))
			i += 1 + end + 1
			if n := openAncestor(open, name); n != nil {
				open = n.parent
			} else if blockElements[name] {
				open.children = append(open.children, &htmlNode{name: name, parent: open})
			}
		case len(rest) > 0 && isASCIILetter(rest[0]):
			flush()
			end := strings.IndexByte(rest, '>')
			if end < 0 {
				// "a<b": libxml2 drops the open tag at the end.
				return root
			}
			tag := rest[:end]
			name := strings.ToLower(tagName(tag))
			i += 1 + end + 1
			node := &htmlNode{name: name, parent: open}
			open.children = append(open.children, node)
			switch {
			case rawElements[name]:
				closing := "</" + name
				j := strings.Index(strings.ToLower(s[i:]), closing)
				if j < 0 {
					node.children = append(node.children, &htmlNode{text: s[i:], parent: node})
					return root
				}
				node.children = append(node.children, &htmlNode{text: s[i : i+j], parent: node})
				i += j
				gt := strings.IndexByte(s[i:], '>')
				if gt < 0 {
					return root
				}
				i += gt + 1
			case strings.HasSuffix(tag, "/"), voidElements[name]:
			default:
				open = node
			}
		default:
			text.WriteByte('<')
			i++
		}
	}
	flush()
	return root
}

// openAncestor is the nearest open element with the name, or nil.
func openAncestor(open *htmlNode, name string) *htmlNode {
	for n := open; n != nil && n.name != "#root"; n = n.parent {
		if n.name == name {
			return n
		}
	}
	return nil
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// tagName is the name at the start of a tag's inside.
func tagName(tag string) string {
	end := strings.IndexFunc(tag, func(r rune) bool { return r == ' ' || r == '/' || r == '\t' || r == '\n' || r == '\r' })
	if end < 0 {
		return tag
	}
	return tag[:end]
}

// plainTextOf is ActionText::PlainTextConversion for one node. listDepth
// is the number of lists the node is inside.
func plainTextOf(n *htmlNode, listDepth int) string {
	if n.name == "" {
		return chomp(n.text)
	}
	children := func(depth int) string {
		var b strings.Builder
		for _, c := range n.children {
			b.WriteString(plainTextOf(c, depth))
		}
		return b.String()
	}
	switch n.name {
	case "script", "style":
		return ""
	case "p", "h1":
		return chomp(children(listDepth)) + "\n\n"
	case "ul", "ol":
		text := chomp(children(listDepth+1)) + "\n\n"
		if listDepth > 0 {
			text = "\n" + text
		}
		return text
	case "br":
		return "\n"
	case "div":
		return chomp(children(listDepth)) + "\n"
	case "figcaption":
		return "[" + chomp(children(listDepth)) + "]"
	case "blockquote":
		text := chomp(children(listDepth)) + "\n\n"
		if strings.TrimSpace(text) == "" {
			return "“”"
		}
		first := strings.IndexFunc(text, notSpace)
		last := strings.LastIndexFunc(text, notSpace)
		return text[:first] + "“" + text[first:last+1] + "”" + text[last+1:]
	case "li":
		bullet := "•"
		if list := nearestList(n); list != nil && list.name == "ol" {
			index := 0
			for _, sibling := range n.parent.children {
				if sibling == n {
					break
				}
				if sibling.name != "" {
					index++
				}
			}
			bullet = strconv.Itoa(index+1) + "."
		}
		indentation := ""
		if listDepth > 1 {
			indentation = strings.Repeat("  ", listDepth-1)
		}
		return indentation + bullet + " " + chomp(children(listDepth)) + "\n"
	}
	return children(listDepth)
}

// nearestList is the list the item belongs to, or nil.
func nearestList(n *htmlNode) *htmlNode {
	for p := n.parent; p != nil; p = p.parent {
		if p.name == "ul" || p.name == "ol" {
			return p
		}
	}
	return nil
}

func notSpace(r rune) bool { return r != ' ' && r != '\n' && r != '\t' && r != '\r' }

// chomp is String#chomp(""): every trailing line break goes.
func chomp(s string) string { return strings.TrimRight(s, "\r\n") }
