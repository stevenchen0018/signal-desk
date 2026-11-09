package admin

import (
	"embed"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/config"
	"signal-desk/internal/store"
)

//go:embed templates/*.html static/*
var webAssets embed.FS

type adminUI struct {
	store         *store.Store
	smtpTest      smtpTestFunc
	template      *template.Template
	secureCookie  bool
	loginMu       sync.Mutex
	loginWindow   time.Time
	loginAttempts int
}

func New(s *store.Store, secureCookie bool, smtpTest smtpTestFunc) http.Handler {
	pages := template.Must(template.New("layout.html").Funcs(template.FuncMap{
		"action": func(view, csrf string, id int64, action, label, class string) actionButton {
			return actionButton{view, csrf, id, action, label, class}
		},
		"traceReference": func(reference string) int64 {
			if id, ok := strings.CutPrefix(reference, "链路 #"); ok {
				return number(id)
			}
			return 0
		},
		"stamp":       config.Stamp,
		"kindLabel":   func(k string) string { return kindLabels[k] },
		"statusLabel": func(k string) string { return statusLabels[k] },
		"href": func(k string) string {
			if k == "home" {
				return "/"
			}
			return "/" + k
		},
	}).ParseFS(webAssets, "templates/*.html"))

	a := &adminUI{store: s, template: pages, secureCookie: secureCookie, smtpTest: smtpTest}
	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.RedirectTrailingSlash = false
	router.Use(a.accessLog, requestSecurity(), gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		log.Printf("后台异常 ref=%s\n%s", c.Writer.Header().Get("X-Request-ID"), debug.Stack())
		c.AbortWithStatus(http.StatusInternalServerError)
	}))

	static, err := fs.Sub(webAssets, "static")
	if err != nil {
		panic(err)
	}
	readMethods := []string{http.MethodGet, http.MethodHead}
	router.Match(readMethods, "/assets/style.css", func(c *gin.Context) {
		http.ServeFileFS(c.Writer, c.Request, static, "style.css")
	})
	router.Match(readMethods, "/assets/app.js", func(c *gin.Context) {
		http.ServeFileFS(c.Writer, c.Request, static, "app.js")
	})
	router.Match(readMethods, "/login", a.signIn)
	router.POST("/login", a.signIn)

	management := router.Group("/", a.authorize)
	management.Match(readMethods, "/", a.dashboard)
	management.Match(readMethods, "/history", a.history)
	management.Match(readMethods, "/interface-logs", a.interfaceLogs)
	management.Match(readMethods, "/traces", a.traces)
	management.Match(readMethods, "/notification-test", a.notificationTest)
	management.POST("/notification-test", a.notificationTest)
	management.POST("/smtp/test", a.testSMTP)
	management.POST("/history", a.retry)
	management.POST("/logs/clear", a.clearLogs)
	management.Match(readMethods, "/account", a.account)
	management.POST("/account", a.account)
	management.POST("/logout", a.logout)
	for _, kind := range []string{"mailboxes", "rules", "models", "channels", "smtp", "templates", "policies"} {
		handler := a.settings(kind)
		management.Match(readMethods, "/"+kind, handler)
		management.POST("/"+kind, handler)
	}
	return router
}
