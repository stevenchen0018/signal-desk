package worker

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

func TestHTMLNotificationPreservesTableAndEscapesModel(t *testing.T) {
	tmpl := config.NotificationTemplate{Category: "custom", Format: config.BodyHTML, MessageTemplate: config.MessageTemplate{
		Title: "模型变更", Body: `<p>模型列表</p><table><tr><th>模型</th><th>状态</th></tr><tr><td>{{.Model}}</td><td>上线</td></tr></table>`,
	}}
	title, body, err := tmpl.Render(config.TemplateData{Model: "test|a<&>"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "test|a&lt;&amp;&gt;") {
		t.Fatalf("HTML variables not escaped: %q", body)
	}
	d := store.Delivery{Title: title, Body: body, BodyFormat: config.BodyHTML}
	var raw strings.Builder
	if err := writeEmail(&raw, "from@example.test", "to@example.test", d); err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(raw.String()))
	if err != nil {
		t.Fatal(err)
	}
	media, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || media != "multipart/alternative" {
		t.Fatalf("incorrect MIME: %s %v", media, err)
	}
	reader := multipart.NewReader(msg.Body, params["boundary"])
	for _, want := range []struct{ media, content string }{
		{"text/plain", "test|a<&>"},
		{"text/html", body},
	} {
		part, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		if err != nil || !strings.HasPrefix(part.Header.Get("Content-Type"), want.media) || !strings.Contains(string(data), want.content) {
			t.Fatalf("incorrect part %s: %s %v", want.media, data, err)
		}
	}

	card, err := feishuHTMLCard(title, body)
	if err != nil {
		t.Fatal(err)
	}
	elements := card["body"].(map[string]any)["elements"].([]map[string]string)
	if len(elements) != 2 || !strings.Contains(elements[1]["content"], "| --- | --- |") ||
		!strings.Contains(elements[1]["content"], `test\|a&lt;&amp;&gt;`) {
		t.Fatalf("incorrect Feishu table: %#v", elements)
	}
}
