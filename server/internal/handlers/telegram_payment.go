package handlers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/telegram"
)

func telegramPaymentLabel(autoPay bool) string {
	if autoPay {
		return "下单并使用账户默认支付方式自动付款（以实际扣款结果为准）"
	}
	return "仅下单，不自动付款"
}

func telegramPaymentKeyboard(id string, textOrder bool) [][]telegramMenuButton {
	yes, no, back := "py", "pn", "pb"
	if textOrder {
		yes, no, back = "ty", "tn", "tb"
	}
	// Both choices claim the same persisted ID: clicking both cannot create two orders.
	return [][]telegramMenuButton{
		{{Text: "是 · 下单并自动付款", CallbackData: menuCallback(yes, id)}},
		{{Text: "否 · 仅下单不付款", CallbackData: menuCallback(no, id)}},
		{{Text: "‹ 返回账户选择", CallbackData: menuCallback(back, id)}},
	}
}

func showTelegramPaymentStep(state *app.State, cb map[string]interface{}, id, action string) error {
	if state.DB == nil {
		return fmt.Errorf("数据库不可用")
	}
	row, exists, err := state.DB.GetTelegramButton(id)
	if err != nil {
		return err
	}
	if err = validBuyMenuButton(row, exists); err != nil {
		return err
	}
	message, _ := cb["message"].(map[string]interface{})
	chat := getNested(message, "chat", "id")
	messageID, _ := getNumOrFloat(message["message_id"])
	menu, err := readBuyMenuState(row)
	if err != nil {
		return err
	}
	if action == "pb" {
		if menu.ParentID != "" {
			return sendBuyAccountChoices(state, chat, int64(messageID), menu.ParentID)
		}
		return sendBuyConfigurationChoicesForDatacenter(state, chat, int64(messageID), row.PlanCode, row.Datacenter)
	}
	if action == "tb" {
		var meta struct {
			Quantity int `json:"quantity"`
		}
		if err := json.Unmarshal([]byte(row.ConfigInfo), &meta); err != nil {
			return err
		}
		order := &telegram.OrderInfo{PlanCode: row.PlanCode, Datacenter: row.Datacenter, Quantity: meta.Quantity, Options: db.ParseTelegramButtonOptions(row.Options)}
		if !sendTextOrderAccountChoices(state, chat, int64(messageID), order, true) {
			return fmt.Errorf("无法恢复账户选择")
		}
		return nil
	}
	a, ok := state.FindAccount(row.AccountID)
	if !ok {
		return fmt.Errorf("所选账户已不存在")
	}
	display := menu.Display
	if display == "" {
		display = strings.Join(db.ParseTelegramButtonOptions(row.Options), ", ")
	}
	dc := row.Datacenter
	if dc == "" {
		dc = "所有可用机房"
	}
	text := fmt.Sprintf("是否自动付款？\n\n型号：%s\n配置：%s\n机房：%s\n账户：%s\n\n选择后将提交抢购任务。\n选「是」将请求使用该账户的默认支付方式自动付款；选「否」只下单，不付款。", row.PlanCode, display, dc, accountMenuLabel(a))
	return editBuyMenu(state, chat, int64(messageID), text, telegramPaymentKeyboard(id, action == "text"))
}
