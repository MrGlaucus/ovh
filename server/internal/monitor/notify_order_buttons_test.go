package monitor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ovh-buy/server/internal/telegram"
	"github.com/ovh-buy/server/internal/types"
)

func TestCombinedOrderButtonsPersistExactAccountAndConfiguration(t *testing.T) {
	m := renderTestMonitor(t)
	m.state.Accounts = []types.OVHAccount{{ID: "a", Name: "账户A", Zone: "IE"}, {ID: "b", Name: "账户B", Zone: "FR"}, {ID: "us", Name: "US", Zone: "US"}}
	dcs := []map[string]interface{}{{"dc": "lim", "raw_status": "1H-high"}, {"dc": "gra", "raw_status": "1H-high"}}
	cfg := map[string]interface{}{"config_key": "plan.ram64.disk", "account_id": "a"}
	msg, markup := m.buildAvailabilityAlert("plan", dcs, cfg, "KS-2", "", "", "")
	decode := func(markup interface{}) [][]notificationOrderButton {
		raw, _ := json.Marshal(markup)
		var parsed struct {
			Rows [][]notificationOrderButton `json:"inline_keyboard"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatal(err)
		}
		return parsed.Rows
	}
	rows := decode(markup)
	if len(rows) != 2 || len(rows[0]) != 2 || len(rows[1]) != 2 {
		t.Fatalf("wrong account/DC layout: %+v", rows)
	}
	for _, row := range rows {
		for _, button := range row {
			var cb map[string]string
			json.Unmarshal([]byte(button.CallbackData), &cb)
			if cb["a"] != "add_to_queue" || len(button.CallbackData) > 64 {
				t.Fatal("not a direct order callback")
			}
			stored, exists, err := m.state.DB.GetTelegramButton(cb["u"])
			if err != nil || !exists || (stored.AccountID != "a" && stored.AccountID != "b") {
				t.Fatalf("wrong account %+v %v", stored, err)
			}
			var info map[string]interface{}
			json.Unmarshal([]byte(stored.ConfigInfo), &info)
			if info["config_key"] != "plan.ram64.disk" || info["explicit_options"] != true {
				t.Fatal("configuration identity lost")
			}
			if !strings.Contains(button.Text, "账户") || !strings.Contains(button.Text, DisplayDatacenterShortName(stored.Datacenter)) {
				t.Fatal("missing account/DC label")
			}
		}
	}
	m.saveTelegramAvailabilitySession("plan", cfg, msg, telegram.MessageRef{ChatID: "123", MessageID: 42}, dcs)
	snapshot, err := m.state.DB.TelegramNotificationForMessage("123", 42)
	if err != nil {
		t.Fatal(err)
	}
	for i := range snapshot.Datacenters {
		if snapshot.Datacenters[i].Datacenter == "lim" {
			snapshot.Datacenters[i].ClosedAt = snapshot.Session.CreatedAt + 5
		}
	}
	_, updated := renderTelegramNotificationUnavailable(snapshot)
	remaining := decode(updated)
	if len(remaining) != 1 || len(remaining[0]) != 2 {
		t.Fatalf("closed DC buttons retained %+v", remaining)
	}
	for i, b := range remaining[0] {
		if b.CallbackData != rows[1][i].CallbackData {
			t.Fatal("remaining DC callbacks were regenerated or changed")
		}
	}
}
