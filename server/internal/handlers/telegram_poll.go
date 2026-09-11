package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/monitor"
	"github.com/ovh-buy/server/internal/telegram"
	"github.com/ovh-buy/server/internal/types"
)

// 长轮询（getUpdates）模式的接线。
//
// 和 webhook 是互斥的两条路，不是并列的两个开关：Telegram 只允许一种在生效。
// 所以这里切模式一定是"先把另一条关掉"，不给用户留下两条都开着的中间态 ——
// 那个状态下按钮会时灵时不灵，排查起来极痛苦。

var globalPoller *telegram.Poller

// InitPoller 在 main 里调用一次，把 update 处理函数注入 poller。
// 处理逻辑和 webhook 完全共用 ProcessUpdate，不另写一份。
func InitPoller(state *app.State, mon *monitor.Monitor) *telegram.Poller {
	globalPoller = telegram.NewPoller(state, func(data map[string]interface{}) {
		// polling 没有"伪造来源"的问题（没有入站端点），所以 legacy 恒为 false。
		u := ProcessUpdate(state, mon, data, false)
		if u.status >= 400 {
			state.Logger.Debug("长轮询处理 update 未通过: "+http.StatusText(u.status), "telegram")
		}
	})
	return globalPoller
}

// StartPollerIfEnabled 启动时按配置决定要不要拉起长轮询。
func StartPollerIfEnabled(state *app.State) {
	if globalPoller == nil {
		return
	}
	cfg := state.Config.Get()
	if !cfg.IsPollingMode() {
		return
	}
	if strings.TrimSpace(cfg.TgToken) == "" {
		state.Logger.Warn("配置为长轮询模式，但没有 Telegram Bot Token，跳过启动", "telegram")
		return
	}
	if err := globalPoller.Start(); err != nil {
		state.Logger.Error("启动 Telegram 长轮询失败: "+err.Error(), "telegram")
	}
}

// StopPoller 进程退出时调用。
func StopPoller() {
	if globalPoller != nil {
		globalPoller.Stop()
	}
}

// GetTelegramUpdateMode GET /api/telegram/update-mode
func GetTelegramUpdateMode(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := state.Config.Get()
		mode := "webhook"
		if cfg.IsPollingMode() {
			mode = types.UpdateModePolling
		}
		out := gin.H{
			"success":     true,
			"mode":        mode,
			"hasToken":    strings.TrimSpace(cfg.TgToken) != "",
			"pollingHint": "长轮询不需要公网地址和证书，只要这台机器能访问 api.telegram.org",
		}
		if globalPoller != nil {
			out["poller"] = globalPoller.Status()
		}
		c.JSON(http.StatusOK, out)
	}
}

// SetTelegramUpdateMode POST /api/telegram/update-mode  {"mode":"polling"|"webhook"}
func SetTelegramUpdateMode(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Mode string `json:"mode"`
		}
		_ = c.ShouldBindJSON(&body)
		mode := strings.TrimSpace(strings.ToLower(body.Mode))
		if mode != types.UpdateModePolling && mode != "webhook" {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   `mode 只能是 "polling" 或 "webhook"`,
			})
			return
		}

		cfg := state.Config.Get()
		if strings.TrimSpace(cfg.TgToken) == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "请先填写并保存 Telegram Bot Token",
			})
			return
		}

		if mode == types.UpdateModePolling {
			if globalPoller == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "poller 未初始化"})
				return
			}
			// Start 内部会先 deleteWebhook —— 两者互斥，不删 getUpdates 直接失败。
			if err := globalPoller.Start(); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
				return
			}
			cfg.TgUpdateMode = types.UpdateModePolling
			if err := state.Config.Set(cfg); err != nil {
				globalPoller.Stop()
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "保存配置失败: " + err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"mode":    types.UpdateModePolling,
				"message": "已切换到长轮询，webhook 已注销。现在不需要公网地址也能用一键下单",
				"poller":  globalPoller.Status(),
			})
			return
		}

		// 切回 webhook：先停轮询，否则两边抢同一个 token。
		// 注意这里只改模式，不替用户注册 webhook —— 注册需要他填自己的公网地址，
		// 走设置页原来那个「注册 Webhook」按钮。
		if globalPoller != nil {
			globalPoller.Stop()
		}
		cfg.TgUpdateMode = "webhook"
		if err := state.Config.Set(cfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "保存配置失败: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"mode":    "webhook",
			"message": "已停止长轮询。还需要在下面填公网地址并点「注册 Webhook」，交互式下单才会恢复",
		})
	}
}
