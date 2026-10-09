package handlers

import (
	"fmt"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/catalog"
	"github.com/ovh-buy/server/internal/monitor"
	"github.com/ovh-buy/server/internal/types"
)

// The message's persisted FQN is authoritative. Missing/expired sessions fail closed;
// only legacy menu callbacks retain configuration selection.
func sendNotificationAccountChoices(state *app.State, chatID interface{}, messageID int64, datacenter string) error {
	if state.DB == nil {
		return fmt.Errorf("数据库不可用，请稍后重试")
	}
	snapshot, err := state.DB.TelegramNotificationForMessage(idToString(chatID), messageID)
	if err != nil || snapshot.Session.ConfigKey == "" || snapshot.Session.CreatedAt < float64(time.Now().Add(-7*24*time.Hour).Unix()) {
		return fmt.Errorf("通知配置记录不可用或已过期，请等待新通知，或使用 /buy 重新选择配置")
	}
	if datacenter == "" {
		keyboard := [][]telegramMenuButton{}
		for _, dc := range snapshot.Datacenters {
			if dc.ClosedAt != 0 {
				continue
			}
			keyboard = append(keyboard, []telegramMenuButton{{Text: monitor.DisplayDatacenterShortName(dc.Datacenter) + " 选择账户下单", CallbackData: monitor.NotificationConfigCallback(dc.Datacenter)}})
		}
		if len(keyboard) == 0 {
			return fmt.Errorf("这条通知的配置已全部下架，不会切换其他配置")
		}
		return editNotificationButtons(state, chatID, messageID, keyboard)
	}
	matched := false
	for _, dc := range snapshot.Datacenters {
		if dc.Datacenter == datacenter && dc.ClosedAt == 0 {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("通知中的该配置在所选机房已下架或机房不匹配，不会切换其他配置")
	}
	state.AccountsMu.RLock()
	accounts := append([]types.OVHAccount(nil), state.Accounts...)
	state.AccountsMu.RUnlock()
	menu := buyMenuState{NotifyEntry: true, NotificationBound: true}
	for _, account := range accounts {
		configs := telegramButtonAvailability(state, snapshot.Session.PlanCode, account.ID)
		options, ok := resolveTelegramOrderOptions(configs, snapshot.Session.ConfigKey, datacenter, nil)
		if !ok {
			continue
		}
		cfg := configs[snapshot.Session.ConfigKey]
		menu.Display = catalog.FormatConfigDisplay(cfg.Memory, cfg.Storage)
		menu.Candidates = append(menu.Candidates, buyMenuCandidate{AccountID: account.ID, ConfigKey: snapshot.Session.ConfigKey, Options: options, Datacenters: []string{datacenter}})
	}
	if len(menu.Candidates) == 0 {
		return fmt.Errorf("通知中的该配置在所选机房无货，或库存查询暂不可用；未创建任务，也不会用其他配置代下单。可稍后重试")
	}
	id, err := saveBuyMenuButton(state, snapshot.Session.PlanCode, datacenter, "", nil, menu, snapshot.Session.ConfigKey)
	if err != nil {
		return err
	}
	return sendBuyAccountChoices(state, chatID, messageID, id)
}
