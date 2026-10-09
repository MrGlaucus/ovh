package monitor

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ovh-buy/server/internal/ovh"
	"github.com/ovh-buy/server/internal/types"
)

type notificationOrderButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// Persist one immutable account/configuration/DC tuple per button. Actual availability
// is checked for that account on click, avoiding extra signed requests during notification delivery.
func (m *Monitor) notificationOrderButtons(plan, dc string, config map[string]interface{}) []notificationOrderButton {
	key, _ := config["config_key"].(string)
	if key == "" || m.state.DB == nil {
		return nil
	}
	m.state.AccountsMu.RLock()
	accounts := append([]types.OVHAccount(nil), m.state.Accounts...)
	m.state.AccountsMu.RUnlock()
	region := func(a types.OVHAccount) string {
		zone := a.Zone
		if zone == "" {
			zone = ovh.DefaultSubsidiaryForEndpoint(a.Endpoint)
		}
		return ovh.SubsidiaryRegion(zone)
	}
	var sourceRegion string
	if id, _ := config["account_id"].(string); id != "" {
		if a, ok := m.state.FindAccount(id); ok {
			sourceRegion = region(a)
		}
	}
	buttons := []notificationOrderButton{}
	for _, a := range accounts {
		if sourceRegion != "" && region(a) != sourceRegion {
			continue
		}
		id := uuid.NewString()
		info := map[string]interface{}{"config_key": key, "explicit_options": true}
		if err := m.state.DB.UpsertTelegramButtonForAccount(id, a.ID, plan, dc, nil, info, float64(time.Now().Unix())); err != nil {
			m.state.Logger.Warn("保存通知下单按钮失败: "+err.Error(), "monitor")
			continue
		}
		callback, _ := json.Marshal(map[string]string{"a": "add_to_queue", "u": id})
		name := strings.TrimSpace(a.Name)
		if name == "" {
			name = a.ID
		}
		buttons = append(buttons, notificationOrderButton{Text: DisplayDatacenterShortName(dc) + " - " + name, CallbackData: string(callback)})
	}
	return buttons
}
