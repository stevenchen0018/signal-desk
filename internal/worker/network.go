package worker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

func readBounded(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("内容超过大小限制")
	}
	return b, nil
}

func dialServer(ctx context.Context, host, port, mode string) (net.Conn, *tls.Config, func(), error) {
	var d net.Dialer
	d.Timeout = 12 * time.Second
	address := net.JoinHostPort(host, port)
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if mode == "tls" {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: config}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = d.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err = conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, nil, nil, err
		}
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	return conn, config, func() { stop(); conn.Close() }, nil
}

func requestError(service string, err error) error {
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s请求已取消", service)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return fmt.Errorf("%s连接或响应超时", service)
	default:
		return fmt.Errorf("%s请求失败，请检查地址、网络及 TLS 证书", service)
	}
}
