package monitor

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/types"
)

type outboxTransport func(*http.Request) (*http.Response, error)

func (f outboxTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func outboxMonitor(t *testing.T) *Monitor {
	m := renderTestMonitor(t)
	m.state.Config = config.New(m.state.DB)
	m.state.Config.Set(types.Config{TgToken: "test-token", TgChatID: "123", NotifyWebhookURL: "https://webhook.invalid"})
	m.AddSubscription("plan", nil, true, true, "Server", nil, nil, false, 0, "", false, 0, nil)
	return m
}

func outboxRows(t *testing.T, m *Monitor) []db.TelegramOutboxEntry {
	t.Helper()
	rows, err := m.state.DB.ListTelegramOutbox("plan")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// Reproduces the incident through the real broadcast and HTTP sender: TG 502,
// webhook succeeds, history/state independent, restart, then TG-only recovery.
func TestAvailabilityOutbox502RestartRecovery(t *testing.T) {
	m := outboxMonitor(t)
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	tgCalls, webhookCalls, orderCalls := 0, 0, 0
	fail := true
	http.DefaultTransport = outboxTransport(func(r *http.Request) (*http.Response, error) {
		code, body := 200, `{"ok":true,"result":{"message_id":42}}`
		switch r.URL.Host {
		case "api.telegram.org":
			tgCalls++
			if fail {
				code, body = 502, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`
			}
		case "webhook.invalid":
			webhookCalls++
		default:
			orderCalls++
			t.Errorf("unexpected request: %s", r.URL.Host)
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	conf := map[string]interface{}{"config_key": "fqn", "display": "16GB / 4TB"}
	m.SendAvailabilityAlertGrouped("plan", []map[string]interface{}{{"dc": "gra", "raw_status": "1H-low", "detected_time": time.Now().Format(time.RFC3339Nano)}}, conf, "Server", "", "", "")
	if tgCalls != 3 || webhookCalls != 1 || len(outboxRows(t, m)) != 1 {
		t.Fatalf("TG=%d webhook=%d pending=%d", tgCalls, webhookCalls, len(outboxRows(t, m)))
	}
	// Persist inventory as the monitor does, and make the retry due without sleep.
	m.subscriptions[0].replaceLastStatus(map[string]string{"gra|fqn": "available"})
	m.SaveToDB()
	rows := outboxRows(t, m)
	var p pendingAvailability
	if err := json.Unmarshal([]byte(rows[0].Payload), &p); err != nil {
		t.Fatal(err)
	}
	p.NextAttempt = 0
	m.savePending(rows[0], p)
	restarted := New(m.state)
	restarted.LoadFromDB()
	fail = false
	restarted.retryPendingAvailability(restarted.subscriptions[0], outboxRows(t, restarted), map[string]string{"gra|fqn": "available"}, nil)
	if tgCalls != 4 || webhookCalls != 1 || orderCalls != 0 || len(outboxRows(t, restarted)) != 0 {
		t.Fatalf("TG=%d webhook=%d orders=%d pending=%d", tgCalls, webhookCalls, orderCalls, len(outboxRows(t, restarted)))
	}
	if restarted.subscriptions[0].statusSnapshot()["gra|fqn"] != "available" {
		t.Fatal("retry changed inventory")
	}
	snapshot, found, err := m.state.DB.CloseTelegramNotificationDatacenter("plan", "fqn", "gra", float64(time.Now().Unix()))
	if err != nil || !found || snapshot.Session.MessageID != 42 {
		t.Fatalf("missing lifecycle mapping: found=%v err=%v", found, err)
	}
}

func TestAvailabilityOutboxCancelsStaleEvents(t *testing.T) {
	for _, scenario := range []string{"sold out", "missing", "new restock", "disabled", "destination changed", "expired", "recreated subscription"} {
		t.Run(scenario, func(t *testing.T) {
			m := outboxMonitor(t)
			entry, p := m.enqueueAvailability("plan", []map[string]interface{}{{"dc": "gra"}}, map[string]interface{}{"config_key": "fqn"}, "Server", "")
			checked := map[string]string{"gra|fqn": "available"}
			restocked := map[string]bool{}
			switch scenario {
			case "sold out":
				checked["gra|fqn"] = "unavailable"
			case "missing":
				delete(checked, "gra|fqn")
			case "new restock":
				restocked["gra|fqn"] = true
			case "disabled":
				m.subscriptions[0].NotifyAvailable = false
			case "destination changed":
				p.Destination = "other"
				m.savePending(entry, p)
			case "expired":
				_, err := m.state.DB.Exec(`UPDATE telegram_availability_outbox SET created_at=?`, time.Now().Add(-16*time.Minute).Unix())
				if err != nil {
					t.Fatal(err)
				}
			case "recreated subscription":
				p.SubscriptionCreatedAt = "old"
				m.savePending(entry, p)
			}
			old := http.DefaultTransport
			http.DefaultTransport = outboxTransport(func(*http.Request) (*http.Response, error) { t.Fatal("stale notification sent"); return nil, nil })
			defer func() { http.DefaultTransport = old }()
			m.retryPendingAvailability(m.subscriptions[0], outboxRows(t, m), checked, restocked)
			if len(outboxRows(t, m)) != 0 {
				t.Fatal("stale event retained")
			}
		})
	}
}

func TestFilterPendingDCsPartialAvailability(t *testing.T) {
	dcs := []map[string]interface{}{{"dc": "gra"}, {"dc": "fra"}, {"dc": "rbx"}}
	keep, ready := filterPendingDCs(dcs, "fqn", map[string]string{"gra|fqn": "available", "fra|fqn": "unavailable", "rbx|fqn": "price_check_failed"}, nil)
	if len(keep) != 2 || len(ready) != 1 || ready[0]["dc"] != "gra" {
		t.Fatalf("keep=%v ready=%v", keep, ready)
	}
}

func TestAvailabilityOutboxPartialRecoveryAndBackoff(t *testing.T) {
	m := outboxMonitor(t)
	entry, pending := m.enqueueAvailability("plan", []map[string]interface{}{{"dc": "gra"}, {"dc": "fra"}, {"dc": "rbx"}}, map[string]interface{}{"config_key": "fqn"}, "Server", "")
	checked := map[string]string{"gra|fqn": "available", "fra|fqn": "unavailable", "rbx|fqn": "price_check_failed"}
	calls := 0
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = outboxTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.telegram.org" {
			t.Fatal("retry broadcast to another channel")
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload.Text, "FRA") || (calls == 1 && strings.Contains(payload.Text, "RBX")) {
			t.Fatalf("sent unavailable DC: %s", payload.Text)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":43}}`)), Header: make(http.Header)}, nil
	})
	pending.NextAttempt = time.Now().Add(time.Hour).Unix()
	m.savePending(entry, pending)
	m.retryPendingAvailability(m.subscriptions[0], outboxRows(t, m), checked, nil)
	if calls != 0 {
		t.Fatal("ignored retry deadline")
	}
	rows := outboxRows(t, m)
	if err := json.Unmarshal([]byte(rows[0].Payload), &pending); err != nil {
		t.Fatal(err)
	}
	if len(pending.DCs) != 2 {
		t.Fatal("sold out DC was not removed during backoff")
	}
	pending.NextAttempt = 0
	m.savePending(entry, pending)
	m.retryPendingAvailability(m.subscriptions[0], outboxRows(t, m), checked, nil)
	rows = outboxRows(t, m)
	if calls != 1 || len(rows) != 1 {
		t.Fatalf("calls=%d remaining=%d", calls, len(rows))
	}
	if err := json.Unmarshal([]byte(rows[0].Payload), &pending); err != nil {
		t.Fatal(err)
	}
	if len(pending.DCs) != 1 || pending.DCs[0]["dc"] != "rbx" {
		t.Fatalf("wrong remaining DCs: %v", pending.DCs)
	}
	checked["rbx|fqn"] = "available"
	m.retryPendingAvailability(m.subscriptions[0], rows, checked, nil)
	if calls != 2 || len(outboxRows(t, m)) != 0 {
		t.Fatal("partial recovery failed")
	}
}
