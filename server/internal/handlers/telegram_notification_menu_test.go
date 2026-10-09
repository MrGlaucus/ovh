package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/catalog"
	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/types"
)

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
