package admin

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
)

func requestSecurity() gin.HandlerFunc {
	origin := http.NewCrossOriginProtection()
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Cache-Control", "no-store")
		if err := origin.Check(c.Request); err != nil {
			c.Abort()
			http.Error(c.Writer, err.Error(), http.StatusForbidden)
			return
		}
		if c.Request.Method == http.MethodPost {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 384<<10)
		}
		c.Next()
	}
}

func (a *adminUI) authorize(c *gin.Context) {
	token, err := c.Cookie(sessionCookie)
	if err != nil {
		c.Abort()
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	active, err := a.store.HasSession(c.Request.Context(), token)
	if err != nil {
		c.Abort()
		a.internal(c, err)
		return
	}
	if !active {
		c.Abort()
		a.cookie(c, "", -1)
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	if c.Request.Method == http.MethodPost {
		if err := c.Request.ParseForm(); err != nil {
			c.Abort()
			c.String(http.StatusBadRequest, "表单无效或内容过长")
			return
		}
		if subtle.ConstantTimeCompare([]byte(c.PostForm("csrf")), []byte(csrfToken(token))) != 1 {
			c.Abort()
			c.String(http.StatusForbidden, "请求校验失败，请刷新页面后重试")
			return
		}
	}
	c.Set(sessionKey, token)
	c.Next()
}
