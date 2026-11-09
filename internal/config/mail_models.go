package config

import (
	"regexp"
	"strings"
)

type MailModel struct {
	ModelName, OfflineTime, ReplacementModel string
}

func (m MailModel) String() string { return m.ModelName }

type MailModelList struct {
	Models    []MailModel
	OfflineAt string
}

var (
	modelTableHeader = regexp.MustCompile(`模型名称[\s|]*计划下线时间[\s|]*推荐替代模型`)
	offlineTimestamp = regexp.MustCompile(`(?:^|[ \t])(\d{4}-\d{2}-\d{2}(?:[ T]\d{2}:\d{2}(?::\d{2})?)?(?:[ \t]*[(（](?:UTC|GMT)[+-]\d{1,2}(?::?\d{2})?[)）])?)(?:$|\b|[ \t])`)
	modelTableID     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@+-]*$`)
)

func parseMailModels(raw string) []MailModel {
	raw = strings.ReplaceAll(raw, "\u00a0", " ")
	if header := modelTableHeader.FindStringIndex(raw); header != nil {
		raw = strings.TrimLeft(raw[header[1]:], " |")
	}
	models := make([]MailModel, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.Trim(line, " \r")
		if line == "" {
			continue
		}
		if model, ok := delimitedMailModel(line); ok {
			if model != (MailModel{}) {
				models = append(models, model)
			}
			continue
		}
		models = append(models, flattenedMailModels(line)...)
	}
	return models
}

func delimitedMailModel(line string) (MailModel, bool) {
	var parts []string
	if strings.Contains(line, "|") {
		if strings.Count(line, "|") == 4 && strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|") {
			line = line[1 : len(line)-1]
		}
		parts = strings.Split(line, "|")
	} else if strings.Contains(line, "\t") {
		parts = strings.Split(line, "\t")
	}
	if len(parts) != 3 {
		return MailModel{}, false
	}
	return MailModel{modelCell(parts[0]), modelCell(parts[1]), modelCell(parts[2])}, true
}

func flattenedMailModels(line string) []MailModel {
	dates := offlineTimestamp.FindAllStringSubmatchIndex(line, -1)
	if len(dates) == 0 {
		parts := strings.Fields(line)
		if len(parts) == 0 || len(parts) > 2 {
			return nil
		}
		row := MailModel{ModelName: modelID(parts[0])}
		if len(parts) == 2 {
			row.ReplacementModel = modelID(parts[1])
		}
		if row == (MailModel{}) {
			return nil
		}
		return []MailModel{row}
	}

	// Between two timestamps, the first token is the preceding row's replacement
	// (if present) and the last is the next row's model name.
	rows := make([]MailModel, len(dates))
	end := 0
	for i, date := range dates {
		start, next := date[2], date[3]
		gap := strings.Fields(line[end:start])
		if len(gap) > 0 {
			rows[i].ModelName = modelID(gap[len(gap)-1])
		}
		if i > 0 && len(gap) > 1 {
			rows[i-1].ReplacementModel = modelID(gap[0])
		}
		rows[i].OfflineTime = strings.Join(strings.Fields(line[start:next]), " ")
		end = next
	}
	if rest := strings.Fields(line[end:]); len(rest) > 0 {
		rows[len(rows)-1].ReplacementModel = modelID(rest[0])
	}
	return rows
}

func modelID(value string) string {
	if modelTableID.MatchString(value) {
		return modelCell(value)
	}
	return ""
}

func modelCell(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "-", "--", "—", "–", "无", "暂无", "N/A", "n/a":
		return ""
	default:
		if strings.Trim(value, "-:") == "" {
			return ""
		}
		return value
	}
}

func (d *TemplateData) SetMailModels(models []MailModel) {
	d.MailModels = models
	d.Count = len(models)
	d.Model = ""
	if d.Count == 1 {
		d.Model = models[0].ModelName
	}
	d.OfflineAt = d.Fields["offline_at"]
	if d.OfflineAt == "" {
		d.OfflineAt = d.Fields["下线时间"]
	}
	if d.OfflineAt != "" {
		return
	}
	for _, model := range models {
		if model.OfflineTime == "" {
			continue
		}
		if d.OfflineAt != "" && d.OfflineAt != model.OfflineTime {
			d.OfflineAt = ""
			return
		}
		d.OfflineAt = model.OfflineTime
	}
}
