package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/admin"
	"signal-desk/internal/store"
	"signal-desk/internal/worker"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:8787", "管理后台监听地址")
	dir := flag.String("data", "./data", "SQLite 数据与配置密钥目录")
	secure := flag.Bool("secure-cookie", false, "通过 HTTPS 反向代理访问时启用安全 Cookie")
	flag.Parse()
	if err := serve(*addr, *dir, *secure); err != nil {
		log.Fatal(err)
	}
}

func serve(addr, dir string, secure bool) error {
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	name := strings.TrimSpace(os.Getenv("SIGNALDESK_ADMIN_USER"))
	if name == "" {
		name = "admin"
	}
	if err = s.BootstrapAdmin(ctx, name, os.Getenv("SIGNALDESK_ADMIN_PASSWORD")); err != nil {
		return err
	}

	client := &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	jobs := worker.New(s, client)
	server := &http.Server{Addr: addr, Handler: admin.New(s, secure, jobs.TestSMTP), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan struct{})
	go func() {
		defer close(done)
		jobs.Run(ctx)
	}()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()
	log.Printf("Signal Desk 后台 http://%s", addr)
	select {
	case err = <-serverErrors:
		stop()
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		err = server.Shutdown(shutdownCtx)
		cancel()
	}
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
