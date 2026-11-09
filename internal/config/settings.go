package config

import (
	"errors"
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
)

const modelEndpoint = "https://api.modelverse.cn/v1/models"

type Mailbox struct {
	Host, Port, TLS, User, Password, Folder string
	Replay                                  bool
	Cron                                    string `json:",omitempty"`
}

type SMTP struct {
	Host, Port, TLS, User, Password, From string
}

type Channel struct {
	Type, Address, Secret string
	Customer              string `json:",omitempty"`
}

type Route struct {
	MessageTemplate
	Channels []int64
}

type Rule struct {
	Mailbox          int64
	Subject, Extract string
	Route
}

type ModelMonitor struct {
	URL, Token string
	Cron       string `json:",omitempty"`
	Up, Down   Route
}

func DefaultMailTemplate() MessageTemplate {
	return MessageTemplate{
		Title: "邮件告警 · {{.Subject}}",
		Body:  "发件人：{{.From}}\n{{range $name, $value := .Fields}}{{$name}}：{{$value}}\n{{end}}",
	}
}

func DefaultModelMonitor() ModelMonitor {
	return ModelMonitor{
		URL:  modelEndpoint,
		Cron: "*/5 * * * *",
		Up:   Route{MessageTemplate: MessageTemplate{"模型上线 · {{.Count}} 个", "以下模型已上线：\n{{range .Models}}- {{.}}\n{{end}}"}},
		Down: Route{MessageTemplate: MessageTemplate{"模型下线 · {{.Count}} 个", "以下模型已下线：\n{{range .Models}}- {{.}}\n{{end}}"}},
	}
}

func ValidateServer(host, port, mode string) error {
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return errors.New("端口必须在 1–65535 范围内")
	}
	if host == "" || strings.ContainsAny(host, " /\\\r\n\t@?#[]") || (strings.Contains(host, ":") && net.ParseIP(host) == nil) {
		return errors.New("服务器请填写主机名或 IP，端口单独填写")
	}
	if mode != "tls" && mode != "starttls" {
		return errors.New("请选择 TLS 或 STARTTLS")
	}
	return nil
}

func ValidateURL(raw string, httpsOnly bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" ||
		(u.Scheme != "https" && (httpsOnly || u.Scheme != "http")) {
		return errors.New("请填写有效的 HTTP(S) 地址，飞书地址必须使用 HTTPS")
	}
	return nil
}

func ParseAddress(s string) (*mail.Address, error) {
	if strings.ContainsAny(s, "\r\n") {
		return nil, errors.New("邮件地址不能换行")
	}
	a, err := mail.ParseAddress(s)
	if err != nil {
		return nil, errors.New("请填写有效的邮件地址")
	}
	return a, nil
}
