package db

import (
	"testing"

	"github.com/ovh-buy/server/internal/types"
)

// 退款关联字段必须真的落库（refund / refund_checked_at 两列）。
//
// ListHistory 是 SELECT * + sqlx 严格映射，列和结构体字段缺一个都会直接报错；
// 但"只加结构体不加列"是静默的：historyToRow 写不进、读回永远 nil ——
// "已退款"标记重启即丢，用户以为没退，可能去重复申请。
// 这个测试锁住往返一致性，防止以后加字段又忘了加列。
func TestHistoryRefundRoundTrip(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	amount := 35.99
	want := types.PurchaseHistoryEntry{
		ID: "h-1", AccountID: "acc-1", PlanCode: "ks-a",
		Datacenter: "gra", Options: []string{"ram-64g-ecc-2133"},
		Status: "success", OrderID: "12345678",
		PurchaseTime: types.NowISO(),
		Price:        &types.PriceInfo{WithTax: &amount, CurrencyCode: "EUR"},
		Refund: &types.RefundInfo{
			ID:     "987654",
			Date:   "2026-09-14T15:04:05+02:00",
			Price:  &types.PriceInfo{WithTax: &amount, CurrencyCode: "EUR"},
			PDFURL: "https://example.invalid/refund.pdf",
		},
		RefundCheckedAt: types.NowISO(),
	}
	if err := database.ReplaceHistory([]types.PurchaseHistoryEntry{want}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := database.ListHistory()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("期望 1 条,实际 %d", len(got))
	}
	g := got[0]
	if g.Refund == nil {
		t.Fatal("Refund 没存住 —— 已退款标记重启即丢")
	}
	if g.Refund.ID != "987654" || g.Refund.PDFURL != want.Refund.PDFURL || g.Refund.Date != want.Refund.Date {
		t.Errorf("退款字段不对: %+v", g.Refund)
	}
	if g.Refund.Price == nil || g.Refund.Price.WithTax == nil || *g.Refund.Price.WithTax != amount || g.Refund.Price.CurrencyCode != "EUR" {
		t.Errorf("退款金额没存住: %+v", g.Refund.Price)
	}
	if g.RefundCheckedAt == "" {
		t.Error("RefundCheckedAt 没存住 —— 节流失效,后台会每轮全量重查")
	}
}

// 未退款的单读回来 Refund 必须是 nil（而不是空结构体）：
// 前端靠 nil 渲染占位符、刷新循环靠 nil 判断"还没标记、要继续查"，
// 读回一个 ID 为空的 RefundInfo 会让这张单永远不再被查退款。
func TestHistoryRefundEmptyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	want := types.PurchaseHistoryEntry{
		ID: "h-2", PlanCode: "ks-b", Datacenter: "gra",
		Status: "success", OrderID: "87654321",
		PurchaseTime: types.NowISO(),
	}
	if err := database.ReplaceHistory([]types.PurchaseHistoryEntry{want}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := database.ListHistory()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("期望 1 条,实际 %d", len(got))
	}
	if got[0].Refund != nil {
		t.Errorf("未退款的单读回 Refund 应为 nil,实际 %+v", got[0].Refund)
	}
}
