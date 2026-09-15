package handlers

import (
	"testing"

	"github.com/ovh-buy/server/internal/types"
)

func TestStripFailedHistoryKeepsSuccessAndCountsFailed(t *testing.T) {
	entries := []types.PurchaseHistoryEntry{
		{ID: "a", Status: "success"},
		{ID: "b", Status: "failed"},
		{ID: "c", Status: "success"},
		{ID: "d", Status: "failed"},
		{ID: "e", Status: "failed"},
	}
	kept, deleted := stripFailedHistory(entries)
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}
	if len(kept) != 2 || kept[0].ID != "a" || kept[1].ID != "c" {
		t.Fatalf("kept = %+v, want [a c]", kept)
	}
}

// 没有失败记录时不能动原顺序，更不能原地改写入参切片（外层还持着 state.History）。
func TestStripFailedHistoryNoFailuresKeepsOrderAndSource(t *testing.T) {
	entries := []types.PurchaseHistoryEntry{
		{ID: "x", Status: "success"},
		{ID: "y", Status: "success"},
	}
	kept, deleted := stripFailedHistory(entries)
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
	if len(kept) != 2 || kept[0].ID != "x" || kept[1].ID != "y" {
		t.Fatalf("kept = %+v, want [x y]", kept)
	}
	if len(entries) != 2 || entries[0].ID != "x" || entries[1].ID != "y" {
		t.Fatalf("原切片被原地修改: %+v", entries)
	}
}

// 全部失败时返回空切片而不是 nil，序列化后是 [] 而不是 null。
func TestStripFailedHistoryAllFailedGivesEmptySlice(t *testing.T) {
	entries := []types.PurchaseHistoryEntry{
		{ID: "b", Status: "failed"},
	}
	kept, deleted := stripFailedHistory(entries)
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if kept == nil || len(kept) != 0 {
		t.Fatalf("kept = %#v, want 非 nil 空切片", kept)
	}
}
