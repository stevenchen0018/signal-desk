package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

var jobCronParser = cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func NextCronRun(expression string, after time.Time) (time.Time, error) {
	parts := strings.Fields(expression)
	count := len(parts)
	if count > 0 && (strings.HasPrefix(parts[0], "CRON_TZ=") || strings.HasPrefix(parts[0], "TZ=")) {
		count--
	}
	if len(expression) > 200 || (count != 5 && count != 6) {
		return time.Time{}, errors.New("Cron 需填写 5 个字段，或在最前面加秒字段组成 6 个字段")
	}
	schedule, err := jobCronParser.Parse(strings.Join(parts, " "))
	if err != nil {
		return time.Time{}, fmt.Errorf("Cron 表达式无效：%w", err)
	}
	next := schedule.Next(after)
	if next.IsZero() {
		return time.Time{}, errors.New("Cron 表达式没有可执行的日期")
	}
	return next, nil
}
