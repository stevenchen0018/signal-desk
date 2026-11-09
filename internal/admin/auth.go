package admin

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/store"
)

const (
	sessionCookie = "signal_desk_session"
	sessionKey    = "signal-desk.session"
)

func (a *adminUI) signIn(c *gin.Context) {
	r := c.Request
	if r.Method != http.MethodPost {
		a.render(c, http.StatusOK, page{View: "login", Title: "登录", Notice: notice(r.URL.Query().Get("ok"))})
		return
	}
	if err := r.ParseForm(); err != nil {
		c.String(http.StatusBadRequest, "表单无效")
		return
	}
	a.loginMu.Lock()
	if time.Since(a.loginWindow) > time.Minute {
		a.loginWindow, a.loginAttempts = time.Now(), 0
	}
	allowed := a.loginAttempts < 20
	a.loginAttempts++
	a.loginMu.Unlock()
	if !allowed {
		c.Header("Retry-After", "60")
		a.render(c, http.StatusTooManyRequests, page{View: "login", Title: "登录", Error: "登录尝试过多，请稍后重试"})
		return
	}
	name, password := strings.TrimSpace(r.PostForm.Get("username")), r.PostForm.Get("password")
	token, err := a.store.NewSession(r.Context(), name, password)
	if errors.Is(err, store.ErrLogin) {
		a.render(c, http.StatusUnauthorized, page{View: "login", Title: "登录", Error: err.Error(), Username: name})
		return
	}
	if err != nil {
		a.internal(c, err)
		return
	}
	a.cookie(c, token, int(store.SessionLifetime.Seconds()))
	c.Redirect(http.StatusSeeOther, "/")
}

func (a *adminUI) cookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true,
		Secure: a.secureCookie || c.Request.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

func csrfToken(token string) string {
	sum := sha256.Sum256([]byte("csrf:" + token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
