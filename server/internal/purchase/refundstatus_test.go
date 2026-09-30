package purchase

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/types"
)

type refundFixture struct {
	responses map[string]string
	calls     map[string]int
}

func TestManualRefundRefreshSkipsUnpaid(t *testing.T) {
	state := &app.State{History: []types.PurchaseHistoryEntry{{ID: "unpaid", Status: "success", OrderID: "100", AccountID: "a", OrderStatus: "notPaid"}}}
	// No client or DB is configured: a manual refresh must make no requests.
	if got := RefreshRefundStatuses(state, true); got != 0 {
		t.Fatalf("updated=%d", got)
	}
	if state.History[0].RefundCheckedAt != "" {
		t.Fatal("unpaid order marked as queried")
	}
}

func TestRefundRequiresVerifiedOriginalInvoice(t *testing.T) {
	for _, scenario := range []string{"no invoice", "unrelated invoice", "invoice error", "paid then cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			f := &refundFixture{calls: map[string]int{}, responses: map[string]string{
				"/me/bill?orderId=100": `[]`,
				"/me/bill/B":           `{"orderId":100}`,
				"/me/refund":           `["R"]`,
				"/me/refund/R":         `{"refundId":"R","orderId":900,"originalBillId":"B"}`,
			}}
			switch scenario {
			case "unrelated invoice":
				f.responses["/me/bill?orderId=100"] = `["B"]`
				f.responses["/me/bill/B"] = `{"orderId":999}`
			case "invoice error":
				delete(f.responses, "/me/bill?orderId=100")
			case "paid then cancelled":
				f.responses["/me/bill?orderId=100"] = `["B"]`
			}
			info, err := FetchOrderRefund(f, "100")
			if (err != nil) != (scenario == "invoice error") {
				t.Fatalf("unexpected error: %v", err)
			}
			if scenario == "paid then cancelled" {
				if info == nil || info.ID != "R" || f.calls["/me/refund"] != 1 {
					t.Fatal("paid cancelled order refund missed")
				}
			} else if info != nil || f.calls["/me/refund"] != 0 || f.calls["/me/refund/R"] != 0 {
				t.Fatal("refund API called without verified invoice")
			}
		})
	}
}

func (f *refundFixture) Get(path string, out interface{}) error {
	f.calls[path]++
	raw, ok := f.responses[path]
	if !ok {
		return errors.New("fixture request failed: " + path)
	}
	return json.Unmarshal([]byte(raw), out)
}

func TestRefundMatchesOriginalInvoiceNotRefundOrder(t *testing.T) {
	f := &refundFixture{calls: map[string]int{}, responses: map[string]string{
		"/me/refund":    `["R1","R2","R3"]`,
		"/me/refund/R1": `{"refundId":"R1","orderId":900,"originalBillId":"B1","date":"2026-09-24T09:00:00Z","priceWithTax":{"value":23.99,"currencyCode":"EUR"}}`,
		// Same purchase order number is NOT sufficient if the invoice is different.
		"/me/refund/R2":        `{"refundId":"R2","orderId":100,"originalBillId":"OTHER","date":"2026-09-25T09:00:00Z"}`,
		"/me/refund/R3":        `{"refundId":"R3","orderId":901,"originalBillId":"B1","date":"2026-09-23T09:00:00Z"}`,
		"/me/bill?orderId=100": `["B1"]`,
		"/me/bill/B1":          `{"orderId":100}`,
		"/me/bill?orderId=101": `["B2"]`,
		"/me/bill/B2":          `{"orderId":101}`,
	}}
	index := loadRefundIndex(f)
	info, err := index.find(f, "100")
	if err != nil || info == nil {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if info.ID != "R1" || info.OriginalBillID != "B1" || info.RefundOrderID != "900" || *info.Price.WithTax != 23.99 {
		t.Fatalf("incorrect match: %+v", info)
	}
	other, err := index.find(f, "101")
	if err != nil || other != nil {
		t.Fatalf("unrelated order matched: %+v %v", other, err)
	}
	if f.calls["/me/refund"] != 1 || f.calls["/me/refund/R1"] != 1 {
		t.Fatal("account scan not reused")
	}
}

func TestRefundLookupDoesNotHideMissingEvidence(t *testing.T) {
	for _, scenario := range []string{"list failure", "detail failure", "missing original invoice", "bill failure", "wrong bill order", "no refunds"} {
		t.Run(scenario, func(t *testing.T) {
			f := &refundFixture{calls: map[string]int{}, responses: map[string]string{
				"/me/refund": `["R"]`, "/me/refund/R": `{"refundId":"R","orderId":900,"originalBillId":"B"}`,
				"/me/bill?orderId=100": `["B"]`, "/me/bill/B": `{"orderId":100}`,
			}}
			wantError := true
			switch scenario {
			case "list failure":
				delete(f.responses, "/me/refund")
			case "detail failure":
				delete(f.responses, "/me/refund/R")
			case "missing original invoice":
				f.responses["/me/refund/R"] = `{"refundId":"R","orderId":100}`
			case "bill failure":
				delete(f.responses, "/me/bill/B")
			case "wrong bill order":
				f.responses["/me/bill/B"] = `{"orderId":999}`
				wantError = false
			case "no refunds":
				f.responses["/me/refund"] = `[]`
				wantError = false
			}
			info, err := FetchOrderRefund(f, "100")
			if info != nil || (err != nil) != wantError {
				t.Fatalf("info=%+v error=%v", info, err)
			}
		})
	}
}
