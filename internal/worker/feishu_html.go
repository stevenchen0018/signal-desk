package worker

import (
	"errors"
	"strings"

	"golang.org/x/net/html"
)

var feishuEscape = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", "\\", "\\\\", "|", "\\|",
	"*", "\\*", "_", "\\_", "`", "\\`", "#", "\\#", "~", "\\~",
	"[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)", "!", "\\!",
)

func feishuHTMLCard(title, body string) (map[string]any, error) {
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, errors.New("无法转换 HTML 为飞书卡片")
	}
	var elements []map[string]string
	var text strings.Builder
	add := func(content string) {
		if content = strings.TrimSpace(content); content != "" {
			elements = append(elements, map[string]string{"tag": "markdown", "content": content})
		}
	}
	flush := func() {
		add(text.String())
		text.Reset()
	}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(feishuEscape.Replace(node.Data))
			return
		}
		switch node.Data {
		case "head", "script", "style", "template":
			return
		case "table":
			flush()
			add(feishuTable(node))
			return
		case "br", "p", "div", "li", "h1", "h2", "h3", "h4", "h5", "h6":
			text.WriteByte('\n')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
		switch node.Data {
		case "p", "div", "li", "h1", "h2", "h3", "h4", "h5", "h6":
			text.WriteByte('\n')
		}
	}
	visit(document)
	flush()
	if len(elements) == 0 {
		return nil, errors.New("HTML 正文没有可显示的文本或表格")
	}
	return map[string]any{
		"schema": "2.0",
		"header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": title}},
		"body":   map[string]any{"elements": elements},
	}, nil
}

func feishuTable(table *html.Node) string {
	var rows [][]string
	columns := 0
	var readRows func(*html.Node)
	readRows = func(parent *html.Node) {
		for node := parent.FirstChild; node != nil; node = node.NextSibling {
			switch node.Data {
			case "thead", "tbody", "tfoot":
				readRows(node)
			case "tr":
				var row []string
				for cell := node.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Data == "td" || cell.Data == "th" {
						row = append(row, feishuEscape.Replace(tableCellText(cell)))
					}
				}
				if len(row) > 0 {
					rows = append(rows, row)
					columns = max(columns, len(row))
				}
			}
		}
	}
	readRows(table)
	var markdown strings.Builder
	for i, row := range rows {
		markdown.WriteString("| ")
		markdown.WriteString(strings.Join(row, " | "))
		// Keep each row rectangular. Oversized tables use one text line per row.
		if columns <= 20 {
			markdown.WriteString(strings.Repeat(" | ", columns-len(row)))
		}
		markdown.WriteString(" |\n")
		if i == 0 && columns <= 20 {
			markdown.WriteString("|")
			markdown.WriteString(strings.Repeat(" --- |", columns))
			markdown.WriteByte('\n')
		}
	}
	return markdown.String()
}

func tableCellText(cell *html.Node) string {
	var content strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.TextNode {
			content.WriteString(node.Data)
			return
		}
		switch node.Data {
		case "script", "style", "template":
			return
		case "br", "p", "div":
			content.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(cell)
	return strings.Join(strings.Fields(content.String()), " ")
}
