package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/catalog"
	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/logger"
	"github.com/ovh-buy/server/internal/storage"
	"github.com/ovh-buy/server/internal/telegram"
	"github.com/ovh-buy/server/internal/types"
)

func TestFinalOrderButtonsAcceptNewClicksButNotRedelivery(t *testing.T) {
	oldAvailability, oldOrder, oldTransport := telegramButtonAvailability, telegramButtonProcessOrder, http.DefaultTransport
	t.Cleanup(func() {
		telegramButtonAvailability = oldAvailability
		telegramButtonProcessOrder = oldOrder
		http.DefaultTransport = oldTransport
	})
	http.DefaultTransport = paymentTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.telegram.org" {
			return nil, fmt.Errorf("unexpected network: %s", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":10}}`)), Header: http.Header{}}, nil
	})
	for index, action := range []string{"add_to_queue", "text"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			database, err := db.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			state := app.NewState(storage.Paths{DataDir: dir}, config.New(database), logger.New(filepath.Join(dir, "log"), slog.New(slog.NewTextHandler(io.Discard, nil))), database)
			chat := fmt.Sprint(987650 + index)
			state.Config.Set(types.Config{TgToken: "test", TgChatID: chat, TgAllowedUserIDs: chat})
			state.Accounts = []types.OVHAccount{{ID: "account", Name: "Selected", Zone: "IE", Endpoint: "ovh-eu"}}
			metadata := map[string]interface{}{"quantity": 1, "config_key": "plan.ram"}
			if err := database.UpsertTelegramButtonForAccount("button", "account", "plan", "gra", []string{"ram"}, metadata, float64(time.Now().Unix())); err != nil {
				t.Fatal(err)
			}
			// 兼容升级前已点过、但仍在有效期内的账户按钮。
			if _, ok, err := database.ClaimTelegramButton("button"); err != nil || !ok {
				t.Fatal("seed used button", err)
			}
			available := true
			calls := 0
			telegramButtonAvailability = func(_ *app.State, plan, account string) map[string]*catalog.ConfigAvailability {
				calls++
				if plan != "plan" || account != "account" {
					t.Fatal("selected account/config changed")
				}
				status := "1H-high"
				if !available {
					status = "unavailable"
				}
				return map[string]*catalog.ConfigAvailability{"plan.ram": {FQN: "plan.ram", Options: []string{"ram"}, Datacenters: map[string]string{"gra": status}}}
			}
			telegramButtonProcessOrder = func(s *app.State, account, plan, dc string, n int, options []string) telegram.OrderResult {
				calls++
				if account != "account" || plan != "plan" || dc != "gra" || n != 1 || len(options) != 1 || options[0] != "ram" {
					t.Fatal("selection changed")
				}
				if !available {
					return telegram.OrderResult{Message: "out of stock"}
				}
				item := types.QueueItem{ID: uuid.NewString(), AccountID: account, PlanCode: plan, Datacenter: dc, Options: options, Status: "running", FromTelegram: true}
				if err := s.EnqueueItems([]types.QueueItem{item}, false); err != nil {
					t.Fatal(err)
				}
				return telegram.OrderResult{Success: true, CreatedOrders: 1, TotalOrders: 1}
			}
			callback, _ := json.Marshal(map[string]string{"a": action, "u": "button"})
			update := func(id int64) map[string]interface{} {
				return map[string]interface{}{
					"update_id": id, "callback_query": map[string]interface{}{"id": fmt.Sprint(id), "data": string(callback), "from": map[string]interface{}{"id": chat}, "message": map[string]interface{}{"message_id": float64(5), "chat": map[string]interface{}{"id": chat}}},
				}
			}
			for _, id := range []int64{1001, 1002} {
				if got := ProcessUpdate(state, nil, update(id)); got.status != 200 {
					t.Fatalf("new click rejected: %+v", got)
				}
				if got := ProcessUpdate(state, nil, update(id)); got.body["duplicate"] != true {
					t.Fatalf("redelivery not blocked: %+v", got)
				}
			}
			rows, err := database.ListQueue()
			if err != nil || len(rows) != 2 || calls != 2 {
				t.Fatalf("want 2 durable tasks for 2 clicks: %d tasks, %d calls, %v", len(rows), calls, err)
			}
			if rows[0].ID == rows[1].ID || rows[0].AutoPay || rows[1].AutoPay {
				t.Fatal("tasks must be independent and unpaid")
			}
			available = false
			if got := ProcessUpdate(state, nil, update(1003)); got.status == 200 {
				t.Fatal("out of stock click accepted")
			}
			rows, _ = database.ListQueue()
			if len(rows) != 2 {
				t.Fatal("out of stock created task")
			}
			available = true
			if got := ProcessUpdate(state, nil, update(1004)); got.status != 200 {
				t.Fatal("failed click consumed button")
			}
			rows, _ = database.ListQueue()
			if len(rows) != 3 {
				t.Fatal("subsequent click did not create task")
			}
			if err := database.UpsertTelegramButtonForAccount("button", "account", "plan", "gra", []string{"ram"}, metadata, float64(time.Now().Add(-25*time.Hour).Unix())); err != nil {
				t.Fatal(err)
			}
			if got := ProcessUpdate(state, nil, update(1005)); got.status != 410 {
				t.Fatal("expired button accepted")
			}
			database.Close()
			before := calls
			if got := ProcessUpdate(state, nil, update(1006)); got.status != 503 || calls != before {
				t.Fatal("dedup DB failure must not execute order")
			}
		})
	}
}
