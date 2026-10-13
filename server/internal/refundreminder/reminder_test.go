package refundreminder

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/logger"
	"github.com/ovh-buy/server/internal/ovh"
	"github.com/ovh-buy/server/internal/types"
)

func TestRefreshRechecksOriginalAccountAndSuppressesDeliveredOrRefunded(t *testing.T) {
	for _, scenario := range []string{"delivered", "refunded", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/auth/time" {
					fmt.Fprint(w, time.Now().Unix())
					return
				}
				if r.Header.Get("X-Ovh-Consumer") != "selected-consumer" {
					t.Error("request used wrong account")
				}
				switch r.URL.RequestURI() {
				case "/me/order/123/status":
					if scenario == "failed" {
						w.WriteHeader(503)
						fmt.Fprint(w, `{"message":"unavailable"}`)
					} else if scenario == "delivered" {
						fmt.Fprint(w, `"delivered"`)
					} else {
						fmt.Fprint(w, `"delivering"`)
					}
				case "/me/bill?orderId=123":
					fmt.Fprint(w, `["B"]`)
				case "/me/bill/B":
					fmt.Fprint(w, `{"orderId":123}`)
				case "/me/refund":
					fmt.Fprint(w, `["R"]`)
				case "/me/refund/R":
					fmt.Fprint(w, `{"refundId":"R","orderId":999,"originalBillId":"B"}`)
				default:
					t.Errorf("unexpected path %s", r.URL)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			database, err := db.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			s := &app.State{DB: database, Logger: logger.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))), Accounts: []types.OVHAccount{{ID: "selected", Endpoint: srv.URL, AppKey: "app", AppSecret: "secret", ConsumerKey: "selected-consumer"}}, History: []types.PurchaseHistoryEntry{{ID: "h", AccountID: "selected", OrderID: "123", Status: "success", OrderStatus: "delivering", PurchaseTime: time.Now().AddDate(0, 0, -13).Format(time.RFC3339)}}}
			s.OVH = ovh.NewFactory(nil, s.FindAccount)
			rows := refresh(s, time.Now())
			if scenario == "failed" {
				if len(rows) != 1 || rows[0].Warning == "" {
					t.Fatal("failure hidden")
				}
			} else if len(rows) != 0 {
				t.Fatal("delivered/refunded order still selected")
			}
		})
	}
}

func TestDeadlineAndExactEligibility(t *testing.T) {
	now := time.Date(2030, 10, 13, 0, 0, 0, 0, china)
	makeHistory := func(id, status, stamp string) types.PurchaseHistoryEntry {
		return types.PurchaseHistoryEntry{ID: id, AccountID: "a", OrderID: id, Status: "success", OrderStatus: status, PurchaseTime: stamp}
	}
	h := makeHistory("1", "delivering", "2030-10-01T16:30:00Z")
	if got := Deadline(h); !got.Equal(time.Date(2030, 10, 17, 0, 0, 0, 0, china)) {
		t.Fatal(got)
	}
	h.PurchaseTime = "2030-10-01T16:30:00"
	if got := Deadline(h); got.Day() != 17 {
		t.Fatal("legacy UTC handling", got)
	}
	s := &app.State{Accounts: []types.OVHAccount{{ID: "a", Endpoint: "ovh-eu"}}, History: []types.PurchaseHistoryEntry{
		makeHistory("72h", "delivering", "2030-10-01T00:00:00+08:00"),
		makeHistory("over72h", "delivering", "2030-10-02T00:00:00+08:00"),
		makeHistory("expired", "delivering", "2030-09-28T00:00:00+08:00"),
		makeHistory("unpaid", "notPaid", "2030-10-01T00:00:00+08:00"),
		makeHistory("delivered", "delivered", "2030-10-01T00:00:00+08:00"),
	}}
	refunded := makeHistory("refunded", "delivering", "2030-10-01T00:00:00+08:00")
	refunded.Refund = &types.RefundInfo{ID: "R"}
	s.History = append(s.History, refunded)
	rows := Items(s, now)
	if len(rows) != 2 || rows[0].History.OrderID != "expired" || rows[1].History.OrderID != "72h" {
		t.Fatalf("wrong selection %+v", rows)
	}
}

func TestDailyRetryRestartAndNoLongerEligible(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := &app.State{DB: database, Config: config.New(database)}
	s.Config.Set(types.Config{TgToken: "test", TgChatID: "123"})
	day := time.Now().In(china).AddDate(1, 0, 0)
	before := time.Date(day.Year(), day.Month(), day.Day(), 8, 59, 0, 0, china)
	nine := before.Add(time.Minute)
	items := []Item{}
	for i := 0; i < 4; i++ {
		items = append(items, Item{History: types.PurchaseHistoryEntry{AccountID: "a", OrderID: fmt.Sprint(i)}, Deadline: nine.Add(48 * time.Hour), URL: "https://example.test/order"})
	}
	reads, sends := 0, 0
	read := func(*app.State, time.Time) []Item { reads++; return items }
	send := func(string, map[string]interface{}) error {
		sends++
		if sends == 2 {
			return errors.New("send failed")
		}
		return nil
	}
	r := runner{state: s, refresh: read, send: send}
	if err := r.tick(before); err != nil || reads != 0 || sends != 0 {
		t.Fatal("ran before 09:00")
	}
	if err := r.tick(nine); err == nil || sends != 2 {
		t.Fatal("expected second chunk failure")
	}
	// Restart resumes only the unsent order; paid/delivered/refunded rows can disappear meanwhile.
	items = items[:3]
	restarted := runner{state: s, refresh: read, send: send}
	if err := restarted.tick(nine.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if sends != 2 || !restarted.ledger.Done {
		t.Fatal("resent successful chunks or stale remaining order")
	}
	again := runner{state: s, refresh: read, send: send}
	if err := again.tick(nine.Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if reads != 2 {
		t.Fatal("same day rechecked after completion")
	}
	if err := again.tick(nine.Add(24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if sends != 3 {
		t.Fatal("next day's reminder missing")
	}
}

func TestMessageEscapesAndWarns(t *testing.T) {
	now := time.Now()
	msg, _ := message([]Item{{Name: "KS-2 <test>", Account: "A&B", History: types.PurchaseHistoryEntry{OrderID: "123", Datacenter: "gra"}, Deadline: now.Add(time.Hour), Warning: "状态未核实"}}, now)
	for _, want := range []string{"&lt;test&gt;", "A&amp;B", "<code>123</code>", "状态未核实", "北京时间"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %s: %s", want, msg)
		}
	}
}
