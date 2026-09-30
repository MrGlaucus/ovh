package delivery

import (
	"errors"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/telegram"
)

type rebootReply struct {
	ID        string `db:"delivery_id"`
	Text      string `db:"text"`
	ChatID    string `db:"chat_id"`
	MessageID int64  `db:"message_id"`
}

// One worker owns reply delivery, independently of the new-server notification switch.
// This path never calls OVH or resets the persisted reboot claim.
func runRebootReplies(state *app.State) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		flushRebootReplies(state, time.Now(), func(r rebootReply) error {
			_, err := telegram.SendReplyChecked(state, r.ChatID, r.Text, r.MessageID)
			return err
		})
		<-tick.C
	}
}

func flushRebootReplies(state *app.State, now time.Time, send func(rebootReply) error) {
	var rows []rebootReply
	if err := state.DB.Select(&rows, `SELECT r.delivery_id,r.text,s.chat_id,s.message_id
 FROM delivery_reboot_replies r JOIN delivery_servers s ON s.id=r.delivery_id
 WHERE r.next_attempt<=? ORDER BY r.next_attempt LIMIT 20`, now.Unix()); err != nil {
		state.Logger.Error("读取重启结果回复失败: "+err.Error(), "delivery")
		return
	}
	for _, r := range rows {
		err := send(r)
		var saveErr error
		if err == nil {
			// Do not delete a newer result saved while this request was in flight.
			_, saveErr = state.DB.Exec("DELETE FROM delivery_reboot_replies WHERE delivery_id=? AND text=?", r.ID, r.Text)
		} else {
			delay := 30 * time.Second
			var failure *telegram.SendError
			if errors.As(err, &failure) && failure.RetryAfter > delay {
				delay = failure.RetryAfter
			}
			_, saveErr = state.DB.Exec("UPDATE delivery_reboot_replies SET next_attempt=? WHERE delivery_id=? AND text=?", time.Now().Add(delay).Unix(), r.ID, r.Text)
			state.Logger.Warn("TG 重启结果回复失败，将自动重试: "+err.Error(), "delivery")
		}
		if saveErr != nil {
			state.Logger.Error("保存重启回复发送状态失败: "+saveErr.Error(), "delivery")
		}
	}
}
