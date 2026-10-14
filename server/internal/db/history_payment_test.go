package db

import (
	"github.com/ovh-buy/server/internal/types"
	"testing"
)

func TestPaymentEvidenceSurvivesCancellationAndReload(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	h := types.PurchaseHistoryEntry{ID: "order", Status: "success", OrderStatus: "delivering"}
	h.SetOrderStatus("cancelled")
	if !h.PaymentConfirmed {
		t.Fatal("lost payment evidence")
	}
	if err = d.ReplaceHistory([]types.PurchaseHistoryEntry{h}); err != nil {
		t.Fatal(err)
	}
	rows, err := d.ListHistory()
	if err != nil || len(rows) != 1 || !rows[0].PaymentConfirmed {
		t.Fatal("payment evidence not persisted", err)
	}
	h = types.PurchaseHistoryEntry{ID: "checking", Status: "success", OrderStatus: "checking"}
	h.SetOrderStatus("cancelled")
	if h.PaymentConfirmed {
		t.Fatal("checking is not confirmed paid")
	}
}
