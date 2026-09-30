package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/types"
)

type paymentTransport func(*http.Request) (*http.Response, error)

func (f paymentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPaymentMenuAndBackPreserveSelection(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state := &app.State{DB: database, Config: config.New(database), Accounts: []types.OVHAccount{{ID: "a", Name: "Account A", Zone: "FR"}}}
	state.Config.Set(types.Config{TgToken: "test", TgChatID: "123"})
	var payload struct {
		Text        string
		ReplyMarkup struct {
			Keyboard [][]telegramMenuButton `json:"inline_keyboard"`
		} `json:"reply_markup"`
	}
	original := http.DefaultTransport
	http.DefaultTransport = paymentTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.telegram.org" {
			t.Fatal("unexpected network request")
		}
		if e := json.NewDecoder(r.Body).Decode(&payload); e != nil {
			t.Fatal(e)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: http.Header{}}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	cb := map[string]interface{}{"message": map[string]interface{}{"chat": map[string]interface{}{"id": float64(123)}, "message_id": float64(42)}}
	for _, notification := range []bool{false, true} {
		parent, err := saveBuyMenuButton(state, "plan", "gra", "", nil, buyMenuState{Display: "32 GB RAM", NotifyEntry: notification, Candidates: []buyMenuCandidate{{AccountID: "a", Options: []string{"ram32"}, ConfigKey: "plan.ram32"}}}, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := sendBuyAccountChoices(state, 123, 42, parent); err != nil {
			t.Fatal(err)
		}
		var accountButton map[string]string
		json.Unmarshal([]byte(payload.ReplyMarkup.Keyboard[0][0].CallbackData), &accountButton)
		id := accountButton["u"]
		if err := showTelegramPaymentStep(state, cb, id, "add_to_queue"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Text, "是否自动付款") || !strings.Contains(payload.Text, "Account A") {
			t.Fatal("missing payment summary")
		}
		keyboard := payload.ReplyMarkup.Keyboard
		for i, action := range []string{"py", "pn", "pb"} {
			var v map[string]string
			if e := json.Unmarshal([]byte(keyboard[i][0].CallbackData), &v); e != nil || v["a"] != action || v["u"] != id || len(keyboard[i][0].CallbackData) > 64 {
				t.Fatal("invalid payment callback")
			}
		}
		row, exists, e := database.GetTelegramButton(id)
		if e != nil || !exists || row.UsedAt != 0 || telegramButtonConfigKey(row.ConfigInfo) != "plan.ram32" {
			t.Fatal("selection changed or navigation consumed order")
		}
		if err := showTelegramPaymentStep(state, cb, id, "pb"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Text, "选择下单账户") {
			t.Fatal("back did not restore accounts")
		}
		_, ok, e := database.ClaimTelegramButton(id)
		if e != nil || !ok {
			t.Fatal("cannot claim final choice")
		}
		_, ok, e = database.ClaimTelegramButton(id)
		if e != nil || ok {
			t.Fatal("opposite payment choice could claim again")
		}
		if err := showTelegramPaymentStep(state, cb, id, "add_to_queue"); err == nil {
			t.Fatal("used button reopened")
		}
	}
}
