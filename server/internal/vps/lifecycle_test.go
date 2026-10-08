package vps

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/logger"
	"github.com/ovh-buy/server/internal/types"
)

func TestRestartWaitsForPreviousLoop(t *testing.T) {
	previous := make(chan struct{})
	runningMu.Lock()
	running = false
	loopDone = previous
	runningMu.Unlock()
	state := &app.State{Logger: logger.New(filepath.Join(t.TempDir(), "test.log"), slog.New(slog.NewTextHandler(io.Discard, nil)))}
	if !Start(state) {
		t.Fatal("start rejected")
	}
	runningMu.Lock()
	done := loopDone
	runningMu.Unlock()
	// 旧循环尚未退出时，新循环不能触碰 Config/DB（这里故意保持 nil）。
	select {
	case <-done:
		t.Fatal("new loop skipped previous loop")
	case <-time.After(20 * time.Millisecond):
	}
	Stop(state)
	close(previous)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stopped successor did not exit")
	}
	runningMu.Lock()
	loopDone = nil
	runningMu.Unlock()
}

func TestRecentVPSPurchaseMatchesSubscriptionAccountAndDC(t *testing.T) {
	now := time.Now()
	sub := types.VPSSubscription{ID: "s", PlanCode: "vps", AutoOrderAccountID: "a"}
	state := &app.State{History: []types.PurchaseHistoryEntry{{TaskID: "vps:s", PlanCode: "vps", AccountID: "a", Datacenter: "gra", Status: "success", PurchaseTime: types.NowISO()}}}
	if !recentVPSPurchase(state, sub, "gra", now.Add(time.Second)) {
		t.Fatal("recent identical purchase not blocked")
	}
	if recentVPSPurchase(state, sub, "sbg", now) {
		t.Fatal("other datacenter incorrectly blocked")
	}
	sub.AutoOrderAccountID = "b"
	if recentVPSPurchase(state, sub, "gra", now) {
		t.Fatal("other account incorrectly blocked")
	}
	sub.AutoOrderAccountID = "a"
	if recentVPSPurchase(state, sub, "gra", now.Add(121*time.Second)) {
		t.Fatal("old purchase incorrectly blocked")
	}
}
