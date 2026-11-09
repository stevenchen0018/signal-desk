package admin

import (
	"net/url"
	"strconv"
	"strings"
)

type option struct {
	Value, Label string
	Selected     bool
}

type field struct {
	Key, Label, Type, Value, Hint, Placeholder, For string
	Wide                                            bool
	Options                                         []option
}

type formGroup struct {
	Title, Intro string
	Fields       []field
}

type settingsForm struct {
	ID, Revision int64
	Title        string
	Groups       []formGroup
	Preview      string
}

func (f *settingsForm) add(title, intro string, fields ...field) {
	f.Groups = append(f.Groups, formGroup{title, intro, fields})
}

type formRequest struct {
	post   bool
	values url.Values
}

func (in formRequest) text(key string) string { return strings.TrimSpace(in.values.Get(key)) }

func (in formRequest) secret(key, original string) string {
	if value := in.values.Get(key); value != "" {
		return value
	}
	if in.text("clear_"+key) == "1" {
		return ""
	}
	return original
}

func input(key, label, value, hint string) field {
	return field{Key: key, Label: label, Type: "text", Value: value, Hint: hint}
}

func secret(key, label, value string) field {
	f := field{Key: key, Label: label, Type: "password"}
	if value != "" {
		f.Placeholder = "已设置 · 留空保留"
	}
	return f
}

func checkbox(key, label string, checked bool, hint string) field {
	f := field{Key: key, Label: label, Type: "checkbox", Hint: hint}
	if checked {
		f.Value = "1"
	}
	return f
}

func textArea(key, label, value, hint string) field {
	return field{Key: key, Label: label, Type: "textarea", Value: value, Hint: hint, Wide: true}
}

func selectField(key, label, selected string, choices ...option) field {
	for i := range choices {
		choices[i].Selected = choices[i].Value == selected
	}
	return field{Key: key, Label: label, Type: "select", Value: selected, Options: choices}
}

func tlsField(mode string) field {
	return selectField("tls", "加密方式", mode, option{Value: "tls", Label: "TLS（直连）"},
		option{Value: "starttls", Label: "STARTTLS"})
}

func intervalField(seconds int64) field {
	value := ""
	if seconds != 0 {
		value = strconv.FormatInt(seconds, 10)
	}
	f := input("interval", "轮询间隔（秒）", value, "Cron 留空时使用固定间隔，范围为 10–86400 秒。")
	f.Type = "number"
	return f
}
