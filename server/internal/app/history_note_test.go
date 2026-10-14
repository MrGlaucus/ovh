package app

import (
	"github.com/ovh-buy/server/internal/types"
	"sync"
	"testing"
)

func TestHistoryNoteSurvivesSnapshotsAndCanBeCleared(t *testing.T) {
	s := newTestState(t)
	s.History = []types.PurchaseHistoryEntry{{ID: "order", PlanCode: "test", Status: "success"}}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SaveHistory(); err != nil {
				t.Error(err)
			}
		}()
	}
	note := "用于备份\n等待交付 <tag>"
	if found, err := s.UpdateHistoryNote("order", note); err != nil || !found {
		t.Fatalf("save: %v %v", found, err)
	}
	wg.Wait()
	rows, err := s.DB.ListHistory()
	if err != nil || len(rows) != 1 || rows[0].Note != note {
		t.Fatalf("note lost: %+v %v", rows, err)
	}
	if found, err := s.UpdateHistoryNote("missing", "x"); err != nil || found {
		t.Fatal("missing record accepted")
	}
	if _, err = s.UpdateHistoryNote("order", ""); err != nil {
		t.Fatal(err)
	}
	rows, err = s.DB.ListHistory()
	if err != nil || rows[0].Note != "" {
		t.Fatal("clear failed", err)
	}
	s.DB.Close()
	if _, err = s.UpdateHistoryNote("order", "unsaved"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if s.History[0].Note != "" {
		t.Fatal("failed save changed memory")
	}
}
