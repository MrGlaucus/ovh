package delivery

import (
	"errors"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/telegram"
)

type commandReply struct {
	ID        string `db:"delivery_id"`
	Action    string `db:"action"`
	Command   string `db:"command"`
	ChatID    string `db:"chat_id"`
	MessageID int64  `db:"message_id"`
}

func flushCommandReplies(state *app.State, now time.Time, send func(commandReply) error) {
	var rows []commandReply
	if err := state.DB.Select(&rows, `SELECT r.delivery_id,r.action,r.command,s.chat_id,s.message_id
 FROM delivery_command_replies r JOIN delivery_servers s ON s.id=r.delivery_id
 WHERE r.next_attempt<=? ORDER BY r.next_attempt LIMIT 20`, now.Unix()); err != nil {
		state.Logger.Error("读取 TG 检测命令回复失败: "+err.Error(), "delivery")
		return
	}
	for _, r := range rows {
		if _, err := state.DB.Exec("UPDATE delivery_command_replies SET next_attempt=? WHERE delivery_id=? AND action=?", time.Now().Add(30*time.Second).Unix(), r.ID, r.Action); err != nil {
			state.Logger.Error("保存 TG 命令重试时间失败: "+err.Error(), "delivery")
			continue
		}
		if err := send(r); err != nil {
			var failure *telegram.SendError
			delay := 30 * time.Second
			if errors.As(err, &failure) && failure.RetryAfter > delay {
				delay = failure.RetryAfter
			}
			if _, e := state.DB.Exec("UPDATE delivery_command_replies SET next_attempt=? WHERE delivery_id=? AND action=?", time.Now().Add(delay).Unix(), r.ID, r.Action); e != nil {
				state.Logger.Error("保存 TG 命令重试时间失败: "+e.Error(), "delivery")
			}
			state.Logger.Warn("TG 检测命令消息发送失败，将自动重试: "+err.Error(), "delivery")
			continue
		}
		if _, err := state.DB.Exec("DELETE FROM delivery_command_replies WHERE delivery_id=? AND action=?", r.ID, r.Action); err != nil {
			state.Logger.Error("清理 TG 命令回复任务失败: "+err.Error(), "delivery")
		}
	}
}
