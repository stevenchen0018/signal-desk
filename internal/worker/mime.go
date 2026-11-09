package worker

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"strings"

	"github.com/emersion/go-message/mail"
	"golang.org/x/net/html"
)

var errOwnMail = errors.New("Signal Desk 生成的通知")

func readMailBody(raw []byte) (string, error) {
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("无法解析 MIME 邮件或解码邮件字符集")
	}
	defer r.Close()
	if r.Header.Get("X-Signal-Desk") == "notification" {
		return "", errOwnMail
	}
	var plain, rich []string
	for {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if part != nil {
				if _, attachment := part.Header.(*mail.AttachmentHeader); attachment {
					continue
				}
			}
			return "", errors.New("无法解码邮件正文")
		}
		header, inline := part.Header.(*mail.InlineHeader)
		if !inline {
			continue
		}
		media, _, _ := mime.ParseMediaType(header.Get("Content-Type"))
		if media != "" && media != "text/plain" && media != "text/html" {
			continue
		}
		raw, err := readBounded(part.Body, maxMailBytes)
		if err != nil {
			return "", errors.New("无法读取邮件正文")
		}
		body := strings.ReplaceAll(string(raw), "\r\n", "\n")
		if media == "text/html" {
			rich = append(rich, htmlText(body))
		} else if strings.TrimSpace(body) != "" {
			plain = append(plain, body)
		}
	}
	if len(plain) > 0 {
		return strings.Join(plain, "\n"), nil
	}
	if body := strings.TrimSpace(strings.Join(rich, "\n")); body != "" {
		return body, nil
	}
	return "", errors.New("邮件没有可读取的文本正文")
}

func htmlText(raw string) string {
	var b strings.Builder
	parser := html.NewTokenizer(strings.NewReader(raw))
	hidden := ""
	for {
		switch parser.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(b.String())
		case html.TextToken:
			if hidden == "" {
				b.WriteString(string(parser.Text()))
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			tag, _ := parser.TagName()
			name := string(tag)
			if name == hidden && parser.Token().Type == html.EndTagToken {
				hidden = ""
				continue
			}
			if hidden != "" {
				continue
			}
			switch name {
			case "script", "style", "head", "title":
				if parser.Token().Type == html.StartTagToken {
					hidden = name
				}
			case "br", "p", "div", "li", "tr", "h1", "h2", "h3", "pre":
				b.WriteByte('\n')
			case "td", "th":
				b.WriteByte(' ')
			}
		}
	}
}
