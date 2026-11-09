package admin

import (
	"bytes"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"signal-desk/internal/store"
)

type section struct{ Kind, Label, Description, Icon string }

var navigation = []section{
	{"home", "运行概览", "查看任务状态与最近运行记录", "grid"},
	{"mailboxes", "收件邮箱", "连接 IMAP 邮箱并设置检查时间", "inbox"},
	{"rules", "邮件规则", "按主题筛选邮件，提取正文并选择通知渠道", "filter"},
	{"models", "模型监控", "查询最新模型、查看请求日志与配置定时监控", "layers"},
	{"templates", "通知模板", "管理模型上线、下线和提前下线等通知文案", "layers"},
	{"policies", "告警策略", "为触发事件选择通知模板和客户接收渠道", "filter"},
	{"channels", "通知渠道", "配置客户的邮箱、飞书或 Webhook 接收地址", "send"},
	{"notification-test", "通知测试", "使用示例数据预览模板，并手动测试邮件、飞书或 Webhook", "send"},
	{"smtp", "发信服务器", "为邮件通知配置 SMTP 服务与发件地址", "server"},
	{"history", "运行记录", "查看提取结果、模型变更、任务执行及通知投递", "clock"},
	{"traces", "全链路日志", "查看收件检查、正文提取、告警匹配和每次投递", "clock"},
	{"interface-logs", "接口日志", "查看后台访问和外部请求的状态、耗时与错误", "clock"},
	{"account", "账号设置", "修改管理员登录密码", "lock"},
}

var kindLabels = map[string]string{
	"mailboxes": "收件邮箱", "models": "模型监控", "mail": "邮件命中",
	"model_up": "模型上线", "model_down": "模型下线", "mail_error": "邮件解析异常",
	"template_error": "模板异常", "test": "渠道测试", "smtp": "SMTP 测试",
	"notification_test": "通知测试",
}

var statusLabels = map[string]string{
	"running": "执行中", "skipped": "跳过", "interrupted": "中断",
	"ok": "完成", "pending": "待发送", "sending": "发送中", "sent": "已发送", "failed": "失败",
}

type page struct {
	View, Title, Description, CSRF, Error, Notice, Username string
	Nav                                                     []section
	Rows                                                    []resourceRow
	Form                                                    *settingsForm
	Records                                                 []store.Record
	HistoryTab                                              string
	Counts                                                  [3]int
	Models                                                  *store.ModelSnapshot
	ModelTab                                                string
	ModelRequests                                           []store.ModelRequest
	LatestModelRequest                                      *store.ModelRequest
	PreviousLogPage, NextLogPage                            int64
	InterfaceLogs                                           []store.InterfaceLog
	InterfaceDirection                                      string
	InterfaceNext                                           int64
	Traces                                                  []store.Trace
	Trace                                                   *store.Trace
	TraceSteps                                              []store.TraceStep
	TraceEvents                                             []store.TraceEvent
	TraceKind                                               string
	TraceNext, TraceResource                                int64
	NotificationTest                                        *notificationTestPage
}

type resourceRow struct {
	store.Resource
	Subtitle string
	Cron     string
}

type actionButton struct {
	View, CSRF           string
	ID                   int64
	Action, Label, Class string
}

func (a *adminUI) render(c *gin.Context, status int, data page) {
	var body bytes.Buffer
	data.Nav = navigation
	if err := a.template.ExecuteTemplate(&body, "layout.html", data); err != nil {
		a.internal(c, err)
		return
	}
	c.Data(status, "text/html; charset=utf-8", body.Bytes())
}

func (a *adminUI) internal(c *gin.Context, err error) {
	log.Printf("后台请求：%v", err)
	c.String(http.StatusInternalServerError, "操作暂时失败，请查看服务端日志")
}
