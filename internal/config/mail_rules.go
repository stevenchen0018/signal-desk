package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type CompiledRule struct {
	ID   int64
	Name string
	Rule
	subject *regexp.Regexp
	Fields  []Extractor
}

func CompileRule(name string, cfg Rule) (CompiledRule, error) {
	subject, err := regexp.Compile(cfg.Subject)
	if err != nil || strings.TrimSpace(cfg.Subject) == "" {
		return CompiledRule{}, errors.New("邮件主题需要有效的正则表达式")
	}
	fields, err := parseExtractors(cfg.Extract)
	return CompiledRule{Name: name, Rule: cfg, subject: subject, Fields: fields}, err
}

func (r CompiledRule) Matches(subject string) bool {
	return r.subject.MatchString(subject)
}

type MailDetails struct {
	UID        uint32            `json:"uid"`
	Rule       string            `json:"rule"`
	From       string            `json:"from"`
	ReceivedAt string            `json:"received_at"`
	Fields     map[string]string `json:"fields"`
	Error      string            `json:"error,omitempty"`
	*MailModelList
}

func (r CompiledRule) Apply(data TemplateData, uid uint32) (TemplateData, MailDetails) {
	data.Rule, data.Event = r.Name, "mail"
	fields, err := extract(data.Body, r.Fields)
	data.SetMailFields(fields)
	detail := MailDetails{UID: uid, Rule: r.Name, From: data.From, ReceivedAt: data.ReceivedAt, Fields: fields}
	if data.MailModels != nil {
		detail.MailModelList = &MailModelList{Models: data.MailModels, OfflineAt: data.OfflineAt}
	}
	if err != nil {
		detail.Error = err.Error()
	}
	return data, detail
}

func (d *TemplateData) SetMailFields(fields map[string]string) {
	d.Fields = fields
	d.Models = nil
	var models []MailModel
	if raw, ok := fields["models"]; ok {
		models = parseMailModels(raw)
	}
	d.SetMailModels(models)
	for _, key := range []string{"model", "模型", "模型ID"} {
		if fields[key] != "" {
			d.Model = fields[key]
			break
		}
	}
}

type Extractor struct {
	Name     string
	Required bool
	Pattern  *regexp.Regexp
}

var fieldName = regexp.MustCompile(`^[\p{L}_][\p{L}\p{N}_-]{0,63}$`)

func parseExtractors(raw string) ([]Extractor, error) {
	list := make([]Extractor, 0)
	seen := make(map[string]bool)
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, pattern, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		name := strings.TrimSuffix(key, "*")
		if !ok || !fieldName.MatchString(name) || seen[name] || strings.TrimSpace(pattern) == "" {
			return nil, fmt.Errorf("提取规则第 %d 行格式错误或字段重复，应为 字段名=正则", i+1)
		}
		if len(list) >= 32 || len(pattern) > 4096 {
			return nil, errors.New("最多配置 32 个提取字段，单条正则最多 4096 字节")
		}
		re, err := regexp.Compile(strings.TrimSpace(pattern))
		if err != nil {
			return nil, fmt.Errorf("提取规则第 %d 行：%w", i+1, err)
		}
		seen[name] = true
		list = append(list, Extractor{name, key != name, re})
	}
	return list, nil
}

func extract(body string, rules []Extractor) (map[string]string, error) {
	fields := make(map[string]string, len(rules))
	var missing, tooLong []string
	remaining := 64 << 10
	for _, rule := range rules {
		match := rule.Pattern.FindStringSubmatch(body)
		value := ""
		if len(match) > 1 {
			value = match[1]
		} else if len(match) == 1 {
			value = match[0]
		}
		if rule.Name == "models" && strings.TrimSpace(value) != "" {
			value = strings.Trim(value, " \r\n") // Preserve empty columns delimited by tabs.
		} else {
			value = strings.TrimSpace(value)
		}
		if len(value) > remaining {
			tooLong = append(tooLong, rule.Name)
			fields[rule.Name] = ""
			continue
		}
		remaining -= len(value)
		fields[rule.Name] = strings.Clone(value)
		if value == "" && rule.Required {
			missing = append(missing, rule.Name)
		}
	}
	if len(tooLong) > 0 {
		return fields, fmt.Errorf("提取结果超过 64 KiB，无法保留字段 %s", strings.Join(tooLong, "、"))
	}
	if len(missing) > 0 {
		return fields, fmt.Errorf("未提取到必填字段 %s", strings.Join(missing, "、"))
	}
	return fields, nil
}
