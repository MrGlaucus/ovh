package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/logger"
	"github.com/ovh-buy/server/internal/ovh"
	"github.com/ovh-buy/server/internal/types"
	ovhsdk "github.com/ovh/go-ovh/ovh"
)

func TestOfficialOrdersBackgroundRefreshAndPersistence(t *testing.T) {
	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	gate := make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(gate) }) }
	defer unblock()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/time":
			fmt.Fprint(w, time.Now().Unix())
		case "/me/order/100":
			calls.Add(1)
			<-gate
			fmt.Fprint(w, `{"orderId":100}`)
		case "/me/order/100/status":
			fmt.Fprint(w, `"notPaid"`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	defer unblock()
	acc := types.OVHAccount{ID: "a", Endpoint: srv.URL, AppKey: "test-app", AppSecret: "test-secret", ConsumerKey: "test-consumer"}
	state := &app.State{DB: database, Accounts: []types.OVHAccount{acc}, Logger: logger.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))}
	state.OVH = ovh.NewFactory(nil, state.FindAccount)
	request := func(h gin.HandlerFunc, method string) (string, int) {
		t.Helper()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, "/ovh/account/orders?account=a&order=100", nil)
		h(c)
		var body struct {
			Status string         `json:"status"`
			Items  []accountOrder `json:"items"`
			Error  string         `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || body.Error != "" {
			t.Fatalf("response %d %s", w.Code, w.Body.String())
		}
		return body.Status, len(body.Items)
	}
	h := AccountOrders(state)
	if status, _ := request(h, http.MethodGet); status != "running" {
		t.Fatalf("initial status %s", status)
	}
	if status, _ := request(h, http.MethodPost); status != "running" {
		t.Fatalf("repeated status %s", status)
	}
	unblock()
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, n := request(h, http.MethodGet)
		if status == "completed" {
			if n != 1 {
				t.Fatal("missing order")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate job: %d", calls.Load())
	}
	if status, n := request(AccountOrders(state), http.MethodGet); status != "completed" || n != 1 {
		t.Fatal("persisted snapshot not reused")
	}
	if calls.Load() != 1 {
		t.Fatal("snapshot started unnecessary network request")
	}
}

func TestOfficialOrderRefundBranches(t *testing.T) {
	for _, scenario := range []string{"unpaid", "cancelled unpaid", "cancelled paid", "status failure", "known refund"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			calls := map[string]int{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.RequestURI()
				mu.Lock()
				calls[path]++
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch path {
				case "/auth/time":
					fmt.Fprint(w, time.Now().Unix())
				case "/me/order/100":
					fmt.Fprint(w, `{"orderId":100,"password":"must-not-leak","url":"https://example.test/secret"}`)
				case "/me/order/100/status":
					if scenario == "status failure" {
						w.WriteHeader(503)
						fmt.Fprint(w, `{"message":"unavailable"}`)
					} else if scenario == "unpaid" {
						fmt.Fprint(w, `"notPaid"`)
					} else {
						fmt.Fprint(w, `"cancelled"`)
					}
				case "/me/bill?orderId=100":
					if scenario == "cancelled unpaid" {
						fmt.Fprint(w, `[]`)
					} else {
						fmt.Fprint(w, `["B"]`)
					}
				case "/me/bill/B":
					fmt.Fprint(w, `{"orderId":100}`)
				case "/me/refund":
					fmt.Fprint(w, `["R"]`)
				case "/me/refund/R":
					fmt.Fprint(w, `{"refundId":"R","orderId":900,"originalBillId":"B"}`)
				default:
					t.Errorf("unexpected request %s", path)
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			client, err := ovhsdk.NewClient(srv.URL, "test-app", "test-secret", "test-consumer")
			if err != nil {
				t.Fatal(err)
			}
			state := &app.State{}
			if scenario == "known refund" {
				state.History = []types.PurchaseHistoryEntry{{AccountID: "a", OrderID: "100", Refund: &types.RefundInfo{ID: "R"}}}
			}
			got, err := loadAccountOrders(state, types.OVHAccount{ID: "a", Endpoint: "ovh-eu"}, client, "100", nil)
			if err != nil || len(got.Items) != 1 {
				t.Fatalf("snapshot=%+v err=%v", got, err)
			}
			order := got.Items[0]
			if scenario == "cancelled paid" || scenario == "known refund" {
				if order.Refund == nil || order.Refund.ID != "R" {
					t.Fatalf("refund missing: %+v", order)
				}
			} else if order.Refund != nil {
				t.Fatal("unexpected refund")
			}
			if scenario == "cancelled paid" && order.Refund.RefundOrderID != "900" {
				t.Fatal("wrong refund order association")
			}
			if scenario == "status failure" && (order.StatusError == "" || order.RefundCheckError == "" || got.Failed != 1) {
				t.Fatal("query failure hidden")
			}
			mu.Lock()
			defer mu.Unlock()
			if scenario != "cancelled paid" && calls["/me/refund"] != 0 {
				t.Fatal("unnecessary refund scan")
			}
			if (scenario == "unpaid" || scenario == "status failure" || scenario == "known refund") && calls["/me/bill?orderId=100"] != 0 {
				t.Fatal("unnecessary invoice query")
			}
		})
	}
}

func TestOfficialOrdersHistoryIsolationAndFreshness(t *testing.T) {
	state := &app.State{History: []types.PurchaseHistoryEntry{
		{ID: "matched", AccountID: "a", OrderID: "100", OrderStatus: "delivered", OrderStatusAt: "2026-10-09T02:00:00Z"},
		{ID: "other-account", AccountID: "b", OrderID: "100"},
		{ID: "refund-order", AccountID: "a", OrderID: "900"},
	}}
	orders := []accountOrder{{OrderID: "100", OrderStatus: "delivering", OrderStatusAt: "2026-10-09T01:00:00Z", Refund: &types.RefundInfo{ID: "R", RefundOrderID: "900"}}}
	syncOfficialOrdersToHistory(state, "a", orders)
	if state.History[0].Refund == nil || state.History[0].OrderStatus != "delivered" {
		t.Fatal("refund missing or newer status overwritten")
	}
	if state.History[1].Refund != nil || state.History[2].Refund != nil {
		t.Fatal("cross-account/refund-order false match")
	}
	attachOrderHistory(state, "a", orders)
	if len(orders[0].Histories) != 1 || orders[0].Histories[0].ID != "matched" || orders[0].OrderStatus != "delivered" {
		t.Fatalf("bad reverse link %+v", orders[0])
	}
	a := types.OVHAccount{ID: "a", ConsumerKey: "one"}
	key := accountOrdersCacheKey(a, "")
	a.ConsumerKey = "two"
	if accountOrdersCacheKey(a, "") == key {
		t.Fatal("credential change reused cache")
	}
	a.ConsumerKey = "one"
	a.ID = "b"
	if accountOrdersCacheKey(a, "") == key {
		t.Fatal("account cache not isolated")
	}
}

func TestOfficialOrderFailuresAreNotHiddenByOlderHistory(t *testing.T) {
	state := &app.State{History: []types.PurchaseHistoryEntry{{AccountID: "a", OrderID: "100", OrderStatus: "delivered", OrderStatusAt: "2026-10-08T01:00:00Z", RefundCheckedAt: "2026-10-08T01:00:00Z"}}}
	orders := []accountOrder{{OrderID: "100", StatusError: "status failed", RefundCheckError: "refund failed"}}
	attachOrderHistory(state, "a", orders)
	if orders[0].StatusError == "" || orders[0].RefundCheckError == "" {
		t.Fatal("older history hid current query failure")
	}
	syncOfficialOrdersToHistory(state, "a", orders)
	if state.History[0].RefundCheckError == "" {
		t.Fatal("query failure was not synchronized")
	}
}
