package purchase

import (
	"encoding/json"
	"errors"
	"testing"
)

type refundFixture struct {
	responses map[string]string
	calls     map[string]int
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
