package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/serverops"
	"github.com/ovh-buy/server/internal/telegram"
)

// DeliveryNotificationSettings stores only local settings. It performs no OVH calls.
func DeliveryNotificationSettings(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		account, ok := ovhAccountFor(state, c)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请选择有效账户"})
			return
		}
		if c.Request.Method == http.MethodPut {
			var in struct {
				Enabled *bool `json:"enabled"`
			}
			if c.ShouldBindJSON(&in) != nil || in.Enabled == nil {
				c.JSON(400, gin.H{"error": "缺少 enabled"})
				return
			}
			if *in.Enabled {
				cfg := state.Config.Get()
				if cfg.TgToken == "" || cfg.TgChatID == "" {
					c.JSON(400, gin.H{"error": "请先配置 Telegram Bot Token 和 Chat ID"})
					return
				}
			}
			if err := state.DB.SetDeliveryEnabled(account.ID, *in.Enabled); err != nil {
				c.JSON(500, gin.H{"error": "保存发货通知开关失败"})
				return
			}
		}
		settings, err := state.DB.DeliverySettings(account.ID)
		if err != nil {
			c.JSON(500, gin.H{"error": "读取发货通知设置失败"})
			return
		}
		c.JSON(200, settings)
	}
}

// Called only after the common Telegram actor authorization, update dedupe and rate limiter.
func handleDeliveryCallback(state *app.State, action, id, callbackID string, chatID interface{}, messageID int64) {
	row, err := state.DB.GetDelivery(id)
	if err != nil || !validDeliveryCallback(row, chatID, messageID) {
		telegram.AnswerCallback(state, callbackID, "通知已失效或不属于此消息", true)
		return
	}
	if _, ok := state.FindAccount(row.AccountID); !ok {
		telegram.AnswerCallback(state, callbackID, "所属账户已删除", true)
		return
	}
	if action == "dn" || action == "dh" {
		command := "curl -sL yabs.sh | bash -s -- -fg"
		if action == "dh" {
			command = "curl -sL https://ba.sh/sick | bash -s -- -cn"
		}
		telegram.AnswerCallback(state, callbackID, "请复制命令到服务器执行", false)
		if !telegram.SendCodeReply(state, chatID, command, messageID) {
			telegram.AnswerCallback(state, callbackID, "命令发送失败，请重试", true)
		}
		return
	}
	// Resolve credentials before claiming; never fall back to another/default account.
	client, err := state.OVH.ClientFor(row.AccountID)
	if err != nil {
		telegram.AnswerCallback(state, callbackID, "账户暂不可用，请在服务器控制页检查", true)
		return
	}
	claimed, err := state.DB.ClaimDeliveryReboot(row.ID)
	if err != nil || !claimed {
		telegram.AnswerCallback(state, callbackID, "该通知已提交过重启，请到服务器控制页查看", true)
		return
	}
	telegram.AnswerCallback(state, callbackID, "正在提交重启请求", false)
	result, err := serverops.Reboot(client, row.ServiceName)
	if err != nil {
		state.Logger.Warn("TG 发货通知重启请求未确认: "+row.ServiceName, "delivery")
		telegram.SendReply(state, chatID, "⚠️ 重启请求未确认："+row.ServiceName+"\n请在服务器控制页核实，本按钮不会自动重复提交。", messageID)
		return
	}
	telegram.SendReply(state, chatID, fmt.Sprintf("✅ 重启请求已提交\n设备：%s\n任务：%v", row.ServiceName, result["taskId"]), messageID)
}

func validDeliveryCallback(row db.DeliveryServer, chatID interface{}, messageID int64) bool {
	return row.SentAt > 0 && row.ChatID != "" && row.ChatID == telegram.ChatIDString(chatID) && row.MessageID > 0 && row.MessageID == messageID
}
