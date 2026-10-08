package purchase

import (
	"errors"
	"fmt"
	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/notify"
	"github.com/ovh-buy/server/internal/types"
	ovhsdk "github.com/ovh/go-ovh/ovh"
)

// 只有明确的 4xx 拒绝才能认定未创建订单；超时、断连、5xx 不能自动重试结账。
func checkoutUncertain(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *ovhsdk.APIError
	if errors.As(err, &apiErr) && apiErr.Code >= 400 && apiErr.Code < 500 && apiErr.Code != 408 {
		return false
	}
	return true
}

func pauseUnknownCheckout(state *app.State, item *types.QueueItem, cartID, detail string) Outcome {
	reason := fmt.Sprintf("结账结果待核对，任务已暂停。请在原账户确认是否已有订单，再决定是否恢复任务。购物车: %s；%s", cartID, detail)
	state.QueueMu.Lock()
	for i := range state.Queue {
		if state.Queue[i].ID == item.ID {
			state.Queue[i].Status = "paused"
			state.Queue[i].UpdatedAt = types.NowISO()
			break
		}
	}
	state.QueueMu.Unlock()
	if err := state.SaveQueue(); err != nil {
		state.Logger.Error("保存结账待核对状态失败: "+err.Error(), "purchase")
	}
	recordFailure(state, item, reason)
	notify.Broadcast(state, "⚠️ 抢购结账结果待核对\n型号: "+item.PlanCode+"\n机房: "+item.Datacenter+"\n"+reason, nil)
	return Outcome{PauseTask: true, Reason: reason}
}
