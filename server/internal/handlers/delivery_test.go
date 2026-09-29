package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/types"
)

func TestDeliveryCallbackBindsOriginalMessage(t *testing.T) {
	row := db.DeliveryServer{SentAt: 1, ChatID: "123", MessageID: 42}
	if !validDeliveryCallback(row, float64(123), 42) {
		t.Fatal("original callback rejected")
	}
	if validDeliveryCallback(row, 124, 42) || validDeliveryCallback(row, 123, 43) {
		t.Fatal("callback allowed from another message/chat")
	}
	row.SentAt = -1
	if validDeliveryCallback(row, 123, 42) {
		t.Fatal("baseline entry exposed as actionable delivery")
	}
}
func TestDeliverySettingsAccountIsolation(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state := &app.State{DB: database, Accounts: []types.OVHAccount{{ID: "a"}, {ID: "b"}}, Config: config.New(database)}
	state.Config.Set(types.Config{TgToken: "test", TgChatID: "123"})
	for _, a := range state.Accounts {
		if err := database.UpsertAccount(a); err != nil {
			t.Fatal(err)
		}
	}
	handler := DeliveryNotificationSettings(state)
	request := func(method, account, body string) (int, db.DeliverySettings) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, "/settings?account="+account, bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		handler(c)
		var result db.DeliverySettings
		json.Unmarshal(w.Body.Bytes(), &result)
		return w.Code, result
	}
	code, a := request(http.MethodGet, "a", "")
	if code != 200 || !a.Enabled {
		t.Fatal("default should be on")
	}
	code, a = request(http.MethodPut, "a", `{"enabled":true}`)
	if code != 200 || !a.Enabled {
		t.Fatal("enable failed")
	}
	_, b := request(http.MethodGet, "b", "")
	if !b.Enabled {
		t.Fatal("other account should remain enabled")
	}
	code, _ = request(http.MethodPut, "missing", `{"enabled":true}`)
	if code != 400 {
		t.Fatal("unknown account accepted")
	}
	code, a = request(http.MethodPut, "a", `{"enabled":false}`)
	if code != 200 || a.Enabled {
		t.Fatal("disable failed")
	}
}
