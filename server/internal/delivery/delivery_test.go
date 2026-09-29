package delivery

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/logger"
	"github.com/ovh-buy/server/internal/telegram"
	"github.com/ovh-buy/server/internal/types"
)

type fakeClient struct {
	names []string
	fail  string
	paths []string
	hook  func(string)
}

func (f *fakeClient) Get(path string, out interface{}) error {
	f.paths = append(f.paths, path)
	if f.hook != nil {
		f.hook(path)
	}
	if path == f.fail {
		return errors.New("proxy unavailable")
	}
	var value interface{}
	switch {
	case path == "/dedicated/server":
		value = f.names
	case strings.HasSuffix(path, "/specifications/hardware"):
		value = map[string]interface{}{"processorName": "ACTUAL CPU", "memorySize": map[string]interface{}{"value": 64, "unit": "GB"}, "diskGroups": []interface{}{map[string]interface{}{"numberOfDisks": 2, "diskType": "NVMe", "diskSize": map[string]interface{}{"value": 960, "unit": "GB"}}}}
	case strings.HasSuffix(path, "/specifications/network"):
		value = map[string]interface{}{"switching": map[string]interface{}{"name": "actual-switch"}, "bandwidth": map[string]interface{}{"OvhToInternet": map[string]interface{}{"value": 500, "unit": "Mbps"}}}
	default:
		value = map[string]interface{}{"commercialRange": "ACTUAL MODEL", "datacenter": "bhs", "ip": "192.0.2.1"}
	}
	b, _ := json.Marshal(value)
	return json.Unmarshal(b, out)
}
func fixture(t *testing.T) (*watcher, *fakeClient, *[]string, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := &app.State{DB: database, Accounts: []types.OVHAccount{{ID: "a", Name: "Account A", Zone: "FR"}, {ID: "b", Name: "Account B"}}, Logger: logger.New(filepath.Join(dir, "log.json"), slog.New(slog.NewTextHandler(io.Discard, nil)))}
	for _, a := range state.Accounts {
		if err := database.UpsertAccount(a); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { state.DB.Close() })
	fake := &fakeClient{names: []string{"old"}}
	messages := []string{}
	w := &watcher{state: state, client: func(id string) (Getter, error) {
		if id != "a" {
			t.Fatalf("wrong account %s", id)
		}
		return fake, nil
	}, send: func(msg string, markup map[string]interface{}) (telegram.MessageRef, error) {
		messages = append(messages, msg)
		return telegram.MessageRef{ChatID: "123", MessageID: 44}, nil
	}}
	return w, fake, &messages, dir
}
func TestDisabledBaselineRestartAndActualConfiguration(t *testing.T) {
	w, f, messages, dir := fixture(t)
	w.state.DB.SetDeliveryEnabled("a", false)
	w.scan("a")
	if len(f.paths) != 0 {
		t.Fatal("disabled account requested OVH")
	}
	if err := w.state.DB.SetDeliveryEnabled("a", true); err != nil {
		t.Fatal(err)
	}
	w.scan("a")
	if len(*messages) != 0 || len(f.paths) != 1 {
		t.Fatal("baseline notified or loaded details")
	}
	f.names = append(f.names, "new-server")
	w.scan("a")
	if len(*messages) != 1 {
		t.Fatal("new server not notified")
	}
	for _, part := range []string{"Account A", "new-server", "ACTUAL MODEL", "ACTUAL CPU", "64 GB", "960 GB", "actual-switch", "500 Mbps", "192.0.2.1"} {
		if !strings.Contains((*messages)[0], part) {
			t.Fatalf("missing actual field %s: %s", part, (*messages)[0])
		}
	}
	for _, path := range f.paths {
		if strings.Contains(path, "availability") || strings.Contains(path, "email") {
			t.Fatal("wrong API source")
		}
	}
	w.state.DB.Close()
	database, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	w.state.DB = database
	w.scan("a")
	if len(*messages) != 1 {
		t.Fatal("restart repeated delivery")
	}
}
func TestFailedListDoesNotInitializeAndDisabledStopsMidScan(t *testing.T) {
	w, f, messages, _ := fixture(t)
	w.state.DB.SetDeliveryEnabled("a", true)
	f.fail = "/dedicated/server"
	w.scan("a")
	s, _ := w.state.DB.DeliverySettings("a")
	if s.Initialized {
		t.Fatal("failed listing initialized baseline")
	}
	f.fail = ""
	w.scan("a")
	f.names = append(f.names, "new")
	f.hook = func(path string) {
		if path == "/dedicated/server/new" {
			w.state.DB.SetDeliveryEnabled("a", false)
		}
	}
	w.scan("a")
	if len(*messages) != 0 {
		t.Fatal("disabled during details still sent")
	}
	for _, path := range f.paths {
		if strings.Contains(path, "/specifications/") {
			t.Fatal("continued requests after disable")
		}
	}
}
func TestPendingDeliveryRetriesAfterRestartEvenWhenOVHUnavailable(t *testing.T) {
	w, f, messages, dir := fixture(t)
	w.state.DB.SetDeliveryEnabled("a", true)
	w.scan("a")
	f.names = append(f.names, "new")
	attempts := 0
	w.send = func(string, map[string]interface{}) (telegram.MessageRef, error) {
		attempts++
		return telegram.MessageRef{}, &telegram.SendError{Message: "429", RetryAfter: 90 * time.Second}
	}
	w.scan("a")
	var pending db.DeliveryServer
	if err := w.state.DB.Get(&pending, "SELECT * FROM delivery_servers WHERE service_name='new'"); err != nil {
		t.Fatal(err)
	}
	if pending.SentAt != 0 || pending.Payload == "" || pending.NextAttempt < time.Now().Add(85*time.Second).Unix() {
		t.Fatal("pending retry not durable or retry_after ignored")
	}
	w.scan("a")
	if attempts != 1 {
		t.Fatal("retry deadline ignored")
	}
	w.state.DB.Close()
	database, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	w.state.DB = database
	w.state.DB.Exec("UPDATE delivery_servers SET next_attempt=0 WHERE id=?", pending.ID)
	w.client = func(string) (Getter, error) { return nil, errors.New("account proxy unavailable") }
	w.send = func(msg string, _ map[string]interface{}) (telegram.MessageRef, error) {
		*messages = append(*messages, msg)
		return telegram.MessageRef{ChatID: "123", MessageID: 9}, nil
	}
	w.scan("a")
	r, err := w.state.DB.GetDelivery(pending.ID)
	if err != nil || r.SentAt <= 0 || len(*messages) != 1 {
		t.Fatal("cached delivery did not retry independently of OVH")
	}
	claimed, err := w.state.DB.ClaimDeliveryReboot(r.ID)
	if err != nil || !claimed {
		t.Fatal("reboot claim failed")
	}
	claimed, _ = w.state.DB.ClaimDeliveryReboot(r.ID)
	if claimed {
		t.Fatal("duplicate reboot allowed")
	}
}
func TestButtonsFitTelegramLimit(t *testing.T) {
	rows := buttons("12345678-1234-1234-1234-123456789012")["inline_keyboard"].([][]map[string]string)
	if len(rows) != 1 || len(rows[0]) != 3 {
		t.Fatal(rows)
	}
	for _, b := range rows[0] {
		if len([]byte(b["callback_data"])) > 64 {
			t.Fatal("callback exceeds Telegram limit")
		}
	}
}

func TestNullListDoesNotCreateBaselineButEmptyListDoes(t *testing.T) {
	w, f, _, _ := fixture(t)
	w.state.DB.SetDeliveryEnabled("a", true)
	f.names = nil
	w.scan("a")
	s, _ := w.state.DB.DeliverySettings("a")
	if s.Initialized {
		t.Fatal("null response initialized baseline")
	}
	f.names = []string{}
	w.scan("a")
	s, _ = w.state.DB.DeliverySettings("a")
	if !s.Initialized {
		t.Fatal("valid empty account could not initialize")
	}
}

func TestDisableClearsPendingAndReenableUsesFreshBaseline(t *testing.T) {
	w, f, messages, _ := fixture(t)
	w.scan("a") // Default on, existing server becomes baseline.
	s, _ := w.state.DB.DeliverySettings("a")
	if !s.Enabled || !s.Initialized {
		t.Fatal("account not enabled by default")
	}
	f.names = append(f.names, "pending")
	w.send = func(string, map[string]interface{}) (telegram.MessageRef, error) {
		return telegram.MessageRef{}, errors.New("offline")
	}
	w.scan("a")
	w.state.DB.SetDeliveryEnabled("a", false)
	s, _ = w.state.DB.DeliverySettings("a")
	if s.Initialized || s.Enabled {
		t.Fatal("disable retained baseline")
	}
	var count int
	w.state.DB.Get(&count, "SELECT count(*) FROM delivery_servers WHERE account_id='a'")
	if count != 0 {
		t.Fatal("baseline or pending event survived disable")
	}
	f.names = append(f.names, "arrived-while-disabled")
	w.state.DB.SetDeliveryEnabled("a", true)
	w.send = func(msg string, _ map[string]interface{}) (telegram.MessageRef, error) {
		*messages = append(*messages, msg)
		return telegram.MessageRef{ChatID: "123", MessageID: 1}, nil
	}
	w.scan("a")
	if len(*messages) != 0 {
		t.Fatal("reenable sent machines in fresh baseline")
	}
	f.names = append(f.names, "after-new-baseline")
	w.scan("a")
	if len(*messages) != 1 || !strings.Contains((*messages)[0], "after-new-baseline") {
		t.Fatal("new baseline did not detect subsequent machine")
	}
}

func TestOldRequestCannotRebuildBaselineAfterToggle(t *testing.T) {
	w, f, _, _ := fixture(t)
	f.hook = func(path string) {
		if path == "/dedicated/server" {
			w.state.DB.SetDeliveryEnabled("a", false)
			w.state.DB.SetDeliveryEnabled("a", true)
		}
	}
	w.scan("a")
	s, _ := w.state.DB.DeliverySettings("a")
	if s.Initialized {
		t.Fatal("stale response established new baseline")
	}
}
