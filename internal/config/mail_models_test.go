package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestMailRuleStructuresOfflineModels(t *testing.T) {
	const offline = "2026-12-01 00:00 (UTC+8)"
	rule, err := CompileRule("提取模型下线解析", Rule{Subject: "下线", Extract: "models=(?s)(.*)"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, raw, shared string
		want              []MailModel
	}{
		{
			"flattened provider email",
			"模型名称 计划下线时间 推荐替代模型kimi-k2.6  " + offline + "  kimi-k3.1-pro kimi-k2.7-code  " + offline + "  kimi-k3.1-pro",
			offline, []MailModel{{"kimi-k2.6", offline, "kimi-k3.1-pro"}, {"kimi-k2.7-code", offline, "kimi-k3.1-pro"}},
		},
		{
			"empty columns and different times",
			"\t\tkimi-k3.1-pro\n| kimi-k2.6 | " + offline + " | |\n| kimi-k2.7-code | 2026-12-02 | kimi-k3.1-pro |",
			"", []MailModel{{"", "", "kimi-k3.1-pro"}, {"kimi-k2.6", offline, ""}, {"kimi-k2.7-code", "2026-12-02", "kimi-k3.1-pro"}},
		},
		{
			"optional block not found", "", "", []MailModel{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, detail := rule.Apply(TemplateData{Body: tc.raw}, 7621)
			if detail.Error != "" || detail.MailModelList == nil || !slices.Equal(detail.Models, tc.want) || detail.OfflineAt != tc.shared {
				t.Fatalf("unexpected parsed detail: %#v", detail.MailModelList)
			}
			encoded, err := json.Marshal(detail)
			if err != nil || !strings.Contains(string(encoded), `"Models":[`) || !strings.Contains(string(encoded), `"OfflineAt":`) {
				t.Fatalf("unexpected event JSON: %s (%v)", encoded, err)
			}
			tmpl := NotificationTemplate{Format: BodyHTML, MessageTemplate: MessageTemplate{
				Title: "下线 {{.Count}}",
				Body:  `<table>{{range .Models}}<tr><td>{{.ModelName}}</td><td>{{.OfflineTime}}</td><td>{{.ReplacementModel}}</td></tr>{{end}}</table>`,
			}}
			_, body, err := tmpl.Render(data)
			if err != nil || strings.Count(body, "<tr>") != len(tc.want) {
				t.Fatalf("unexpected template output: %s (%v)", body, err)
			}
		})
	}
}
