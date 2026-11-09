package worker

import (
	"errors"
	"fmt"
	"net/textproto"
	"regexp"
	"strings"
	"unicode"
)

var smtpPrivateReply = regexp.MustCompile(`(?i)https?://\S+|[^\s<>:]+@[^\s<>:]+`)

type smtpStageError struct {
	stage, hint, reply string
	code               int
}

func (e *smtpStageError) Error() string {
	if e.code == 0 {
		return e.stage + " · " + e.hint
	}
	message := fmt.Sprintf("%s · SMTP %d · %s", e.stage, e.code, e.hint)
	if e.reply != "" {
		message += " · 服务端回复：" + e.reply
	}
	return message
}

func smtpCode(err error) int {
	var stage *smtpStageError
	if errors.As(err, &stage) {
		return stage.code
	}
	return 0
}

func smtpStep(trace *jobTrace, stage, success, hint string, err error, secret string) error {
	if err == nil {
		trace.record(stage, "ok", success)
		return nil
	}
	failure := &smtpStageError{stage: stage, hint: hint}
	var reply *textproto.Error
	if errors.As(err, &reply) {
		failure.code = reply.Code
		if stage != "AUTH" {
			text := reply.Msg
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "[已隐藏]")
			}
			text = smtpPrivateReply.ReplaceAllString(text, "[已隐藏]")
			text = strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return ' '
				}
				return r
			}, text)
			chars := []rune(strings.Join(strings.Fields(text), " "))
			failure.reply = string(chars[:min(300, len(chars))])
		}
	}
	trace.record(stage, "failed", failure.Error())
	return failure
}
