package worker

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"signal-desk/internal/store"
)

func TestSMTPMessageEncoding(t *testing.T) {
	var raw strings.Builder
	if err := writeEmail(&raw, "sender@example.com", "receiver@example.com", store.Delivery{Title: "模型上线", Body: "型号：示例\n第二行"}); err != nil {
		t.Fatal(err)
	}
	if _, err := readMailBody([]byte(raw.String())); !errors.Is(err, errOwnMail) {
		t.Fatalf("generated email would be reprocessed: %v", err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(raw.String()))
	if err != nil {
		t.Fatal(err)
	}
	title, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || title != "模型上线" {
		t.Fatalf("subject = %q, %v", title, err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil || strings.ReplaceAll(string(body), "\r\n", "\n") != "型号：示例\n第二行" {
		t.Fatalf("body = %q, %v", body, err)
	}
}

func TestSMTPProbeReportsEnvelopeRejectionWithoutSending(t *testing.T) {
	for _, code := range []int{250, 550} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			conn, server := net.Pipe()
			defer conn.Close()
			defer server.Close()
			deadline := time.Now().Add(3 * time.Second)
			conn.SetDeadline(deadline)
			server.SetDeadline(deadline)
			result := make(chan error, 1)
			go func() {
				reader := bufio.NewReader(server)
				if _, err := fmt.Fprint(server, "220 local test\r\n"); err != nil {
					result <- err
					return
				}
				line, err := reader.ReadString('\n')
				if err != nil || !strings.HasPrefix(line, "EHLO ") {
					result <- fmt.Errorf("expected EHLO: %q %v", line, err)
					return
				}
				fmt.Fprint(server, "250 local test\r\n")
				line, err = reader.ReadString('\n')
				if err != nil || line != "MAIL FROM:<sender@example.com>\r\n" {
					result <- fmt.Errorf("expected MAIL FROM: %q %v", line, err)
					return
				}
				fmt.Fprintf(server, "%d 5.7.1 sender@example.com not owned by user sensitive-value\r\n", code)
				line, err = reader.ReadString('\n')
				if !errors.Is(err, io.EOF) || line != "" {
					result <- fmt.Errorf("probe attempted an additional SMTP command: %q %v", line, err)
					return
				}
				result <- nil
			}()
			client, err := smtp.NewClient(conn, "local.test")
			if err != nil {
				t.Fatal(err)
			}
			trace := &jobTrace{}
			err = smtpEnvelope(client, "sensitive-value", &mail.Address{Address: "sender@example.com"}, nil, store.Delivery{}, trace)
			client.Close()
			if serverErr := <-result; serverErr != nil {
				t.Fatal(serverErr)
			}
			if code == 250 {
				if err != nil || len(trace.steps) != 1 || trace.steps[0].Status != "ok" {
					t.Fatalf("MAIL FROM probe failed: %v, %#v", err, trace.steps)
				}
			} else if err == nil || smtpCode(err) != 550 || !strings.Contains(err.Error(), "not owned by user") ||
				strings.Contains(err.Error(), "sensitive-value") || strings.Contains(err.Error(), "sender@example.com") {
				t.Fatalf("missing or unsafe SMTP reply: %v", err)
			}
		})
	}
}
