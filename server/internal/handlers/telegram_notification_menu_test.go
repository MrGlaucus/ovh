package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/catalog"
	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/types"
)

func TestBoundNotificationNeverSwitchesConfiguration(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state := &app.State{DB: database, Config: config.New(database), Accounts: []types.OVHAccount{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}}
	state.Config.Set(types.Config{TgToken: "test", TgChatID: "123"})
	err = database.CreateTelegramNotificationSession(db.TelegramNotificationSession{ID: "session", PlanCode: "plan", ConfigKey: "plan.ram64", ChatID: "123", MessageID: 42, CreatedAt: float64(time.Now().Unix())}, []db.TelegramNotificationDatacenter{{Datacenter: "gra", LineText: "GRA"}})
	if err != nil {
		t.Fatal(err)
	}
	oldTransport, oldAvailability := http.DefaultTransport, telegramButtonAvailability
	t.Cleanup(func() { http.DefaultTransport = oldTransport; telegramButtonAvailability = oldAvailability })
	available := true
	telegramButtonAvailability = func(_ *app.State, _ string, account string) map[string]*catalog.ConfigAvailability {
		configs := map[string]*catalog.ConfigAvailability{"plan.ram32": {Memory: "32GB", Options: []string{"ram32"}, Datacenters: map[string]string{"gra": "1H-high"}}}
		if available && account == "a" {
			configs["plan.ram64"] = &catalog.ConfigAvailability{Memory: "64GB", Options: []string{"ram64"}, Datacenters: map[string]string{"gra": "1H-high"}}
		}
		return configs
	}
	var payload struct {
		Text   string `json:"text"`
		Markup struct {
			Keyboard [][]telegramMenuButton `json:"inline_keyboard"`
		} `json:"reply_markup"`
	}
	edits := 0
	http.DefaultTransport = paymentTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.telegram.org" || !strings.HasSuffix(r.URL.Path, "/editMessageReplyMarkup") {
			t.Fatalf("unexpected request %s", r.URL)
		}
		edits++
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Text != "" {
			t.Fatal("notification text overwritten")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: http.Header{}}, nil
	})
	if err := sendNotificationAccountChoices(state, "123", 42, "gra"); err != nil {
		t.Fatal(err)
	}
	if len(payload.Markup.Keyboard) != 2 {
		t.Fatal("expected one account and back button")
	}
	var cb map[string]string
	json.Unmarshal([]byte(payload.Markup.Keyboard[0][0].CallbackData), &cb)
	row, exists, err := database.GetTelegramButton(cb["u"])
	if err != nil || !exists || cb["a"] != "add_to_queue" || row.AccountID != "a" || telegramButtonConfigKey(row.ConfigInfo) != "plan.ram64" {
		t.Fatalf("incorrect bound account button %+v %v", row, err)
	}
	opts := db.ParseTelegramButtonOptions(row.Options)
	if len(opts) != 1 || opts[0] != "ram64" {
		t.Fatalf("wrong options %v", opts)
	}
	if !strings.Contains(payload.Markup.Keyboard[1][0].CallbackData, `"stock"`) {
		t.Fatal("back lost configuration binding")
	}
	if err := sendNotificationAccountChoices(state, "123", 42, ""); err != nil {
		t.Fatal(err)
	}
	before := edits
	available = false
	if err := sendNotificationAccountChoices(state, "123", 42, "gra"); err == nil {
		t.Fatal("64GB disappeared but 32GB was selected")
	}
	if edits != before {
		t.Fatal("unavailable configuration edited account menu")
	}
	for _, args := range []struct {
		chat, dc string
		message  int64
	}{{"other", "gra", 42}, {"123", "gra", 999}, {"123", "bhs", 42}} {
		if err := sendNotificationAccountChoices(state, args.chat, args.message, args.dc); err == nil {
			t.Fatal("invalid notification identity accepted")
		}
	}
	_, err = database.Exec(`UPDATE telegram_notification_sessions SET created_at = ? WHERE id = 'session'`, time.Now().Add(-8*24*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := sendNotificationAccountChoices(state, "123", 42, "gra"); err == nil {
		t.Fatal("expired session accepted")
	}
}

func TestNotificationNavigationKeepsMessageBody(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state := &app.State{DB: database, Config: config.New(database), Accounts: []types.OVHAccount{{ID: "a", Name: "A", Zone: "IE"}}}
	state.Config.Set(types.Config{TgToken: "test", TgChatID: "123"})
	oldTransport, oldAvailability := http.DefaultTransport, telegramButtonAvailability
	t.Cleanup(func() { http.DefaultTransport = oldTransport; telegramButtonAvailability = oldAvailability })
	telegramButtonAvailability = func(*app.State, string, string) map[string]*catalog.ConfigAvailability {
		return map[string]*catalog.ConfigAvailability{"plan.ram": {Memory: "32GB", Options: []string{"ram"}, Datacenters: map[string]string{"gra": "1H-high"}}}
	}
	var payload map[string]interface{}
	wantMethod := "editMessageReplyMarkup"
	requests := 0
	http.DefaultTransport = paymentTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Host != "api.telegram.org" || !strings.HasSuffix(r.URL.Path, "/"+wantMethod) {
			t.Fatalf("unexpected request: %s", r.URL)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if wantMethod == "editMessageReplyMarkup" {
			if _, ok := payload["text"]; ok {
				t.Fatal("notification body overwritten")
			}
			if _, ok := payload["parse_mode"]; ok {
				t.Fatal("notification formatting changed")
			}
		}
		if payload["message_id"] != float64(42) {
			t.Fatal("wrong message edited")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: http.Header{}}, nil
	})
	if err := sendBuyDatacenterChoicesForPlan(state, "123", 42, "plan"); err != nil {
		t.Fatal(err)
	}
	if err := sendBuyConfigurationChoicesForDatacenter(state, "123", 42, "plan", "gra"); err != nil {
		t.Fatal(err)
	}
	keyboard := payload["reply_markup"].(map[string]interface{})["inline_keyboard"].([]interface{})
	first := keyboard[0].([]interface{})[0].(map[string]interface{})
	var callback map[string]string
	if err := json.Unmarshal([]byte(first["callback_data"].(string)), &callback); err != nil {
		t.Fatal(err)
	}
	if err := sendBuyAccountChoices(state, "123", 42, callback["u"]); err != nil {
		t.Fatal(err)
	}
	// 返回配置、再返回机房，同样只改键盘。
	if err := sendBuyConfigurationChoicesForDatacenter(state, "123", 42, "plan", "gra"); err != nil {
		t.Fatal(err)
	}
	if err := sendBuyDatacenterChoicesForPlan(state, "123", 42, "plan"); err != nil {
		t.Fatal(err)
	}
	if requests != 5 {
		t.Fatalf("expected five menu edits, got %d", requests)
	}
	// /buy 独立消息仍显示操作指引。
	wantMethod = "editMessageText"
	id, err := saveBuyMenuButton(state, "plan", "gra", "", nil, buyMenuState{Display: "32GB", Candidates: []buyMenuCandidate{{AccountID: "a", Options: []string{"ram"}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sendBuyAccountChoices(state, "123", 42, id); err != nil {
		t.Fatal(err)
	}
	if text, _ := payload["text"].(string); !strings.Contains(text, "选择下单账户") {
		t.Fatal("/buy instructions missing")
	}
}
