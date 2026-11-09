package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

func sendEmail(ctx context.Context, cfg config.SMTP, address string, probe bool, d store.Delivery, trace *jobTrace) error {
	from, err := config.ParseAddress(cfg.From)
	if err != nil {
		return err
	}
	var to *mail.Address
	if !probe {
		to, err = config.ParseAddress(address)
		if err != nil {
			return err
		}
	}
	conn, tlsConfig, closeConn, err := dialServer(ctx, cfg.Host, cfg.Port, cfg.TLS)
	if err != nil {
		failure := requestError("SMTP", err)
		trace.record("建立连接", "failed", failure.Error())
		return failure
	}
	defer closeConn()
	trace.record("建立连接", "ok", cfg.Host+":"+cfg.Port+" · "+cfg.TLS)
	c, err := smtp.NewClient(conn, cfg.Host)
	if err = smtpStep(trace, "SMTP 握手", "服务器已响应", "SMTP 握手失败", err, cfg.Password); err != nil {
		return err
	}
	defer c.Close()
	if err = smtpStep(trace, "EHLO / HELO", "服务器已接受客户端问候", "检查 SMTP 主机及服务器的客户端标识要求",
		c.Hello("localhost"), cfg.Password); err != nil {
		return err
	}
	if cfg.TLS == "starttls" {
		err = smtpStep(trace, "STARTTLS", "TLS 协商成功", "检查端口、加密方式和证书", c.StartTLS(tlsConfig), cfg.Password)
		if err != nil {
			return err
		}
	}
	if cfg.User != "" {
		err = smtpStep(trace, "AUTH", "认证成功", "检查 SMTP 用户名、授权码及 AUTH PLAIN 支持",
			c.Auth(smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)), cfg.Password)
		if err != nil {
			return err
		}
		if strings.Contains(cfg.User, "@") && !strings.EqualFold(cfg.User, from.Address) {
			trace.record("发件身份", "skipped", "发件地址与登录邮箱不同，服务端须授权代发")
		}
	} else {
		trace.record("AUTH", "skipped", "SMTP 用户名为空，按免认证方式发信")
	}
	return smtpEnvelope(c, cfg.Password, from, to, d, trace)
}

func smtpEnvelope(c *smtp.Client, secret string, from, to *mail.Address, d store.Delivery, trace *jobTrace) error {
	if err := smtpStep(trace, "MAIL FROM", "服务器已接受发件地址", "服务器拒绝发件人，请核对登录账号及发件地址的代发权限",
		c.Mail(from.Address), secret); err != nil {
		return err
	}
	if to == nil {
		return nil
	}
	if err := smtpStep(trace, "RCPT TO", "服务器已接受收件地址", "服务器拒绝收件人，请检查目标邮箱与中继权限",
		c.Rcpt(to.Address), secret); err != nil {
		return err
	}
	writer, err := c.Data()
	if err = smtpStep(trace, "DATA", "服务器允许提交邮件", "服务器拒绝接收邮件正文", err, secret); err != nil {
		return err
	}
	if err = writeEmail(writer, from.String(), to.String(), d); err != nil {
		trace.record("提交邮件", "failed", "SMTP 写入邮件正文失败")
		return errors.New("SMTP 写入邮件正文失败")
	}
	return smtpStep(trace, "提交邮件", "SMTP 服务器已接受邮件", "SMTP 服务器未确认接收邮件", writer.Close(), secret)
}

func writeEmail(w io.Writer, from, to string, d store.Delivery) error {
	_, err := fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nAuto-Submitted: auto-generated\r\nX-Signal-Desk: notification\r\n",
		from, to, mime.QEncoding.Encode("UTF-8", d.Title), time.Now().Format(time.RFC1123Z))
	if err != nil {
		return err
	}
	if d.BodyFormat != config.BodyHTML {
		if _, err := io.WriteString(w, "Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n"); err != nil {
			return err
		}
		return writeQuotedText(w, d.Body)
	}

	multi := multipart.NewWriter(w)
	if _, err := fmt.Fprintf(w, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", multi.Boundary()); err != nil {
		return err
	}
	for _, part := range []struct{ mimeType, content string }{
		{"text/plain", htmlText(d.Body)},
		{"text/html", d.Body},
	} {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", part.mimeType+"; charset=UTF-8")
		header.Set("Content-Transfer-Encoding", "quoted-printable")
		writer, err := multi.CreatePart(header)
		if err != nil {
			return err
		}
		if err := writeQuotedText(writer, part.content); err != nil {
			return err
		}
	}
	return multi.Close()
}

func writeQuotedText(w io.Writer, body string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := io.WriteString(qp, strings.ReplaceAll(body, "\r\n", "\n")); err != nil {
		return err
	}
	return qp.Close()
}
