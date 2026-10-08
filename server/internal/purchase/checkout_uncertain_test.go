package purchase

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/logger"
	"github.com/ovh-buy/server/internal/storage"
	"github.com/ovh-buy/server/internal/types"
	ovhsdk "github.com/ovh/go-ovh/ovh"
)

func TestCheckoutUncertain(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409, 429} {
		if checkoutUncertain(fmt.Errorf("wrapped: %w", &ovhsdk.APIError{Code: code})) {
			t.Fatalf("explicit rejection %d marked uncertain", code)
		}
	}
	for _, err := range []error{io.EOF, &ovhsdk.APIError{Code: 408}, &ovhsdk.APIError{Code: 500}, &ovhsdk.APIError{Code: 504}} {
		if !checkoutUncertain(err) {
			t.Fatalf("ambiguous checkout %v will retry", err)
		}
	}
}

func TestCheckoutPauseAndHistoryArePersistedBeforeReturn(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	lg := logger.New(filepath.Join(dir, "test.log"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	state := app.NewState(storage.Paths{DataDir: dir}, config.New(database), lg, database)
	item := types.QueueItem{ID: "task", Status: "running", PlanCode: "ks", Datacenter: "gra"}
	state.Queue = []types.QueueItem{item}
	done := make(chan Outcome, 1)
	go func() { done <- pauseUnknownCheckout(state, &item, "cart-123", "timeout") }()
	select {
	case out := <-done:
		if !out.PauseTask {
			t.Fatal("task not paused")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("history/queue persistence deadlocked")
	}
	rows, err := database.ListQueue()
	if err != nil || len(rows) != 1 || rows[0].Status != "paused" {
		t.Fatalf("pause not persisted: %+v %v", rows, err)
	}
	history, err := database.ListHistory()
	if err != nil || len(history) != 1 || history[0].ErrorMessage == nil {
		t.Fatalf("history not persisted: %+v %v", history, err)
	}
	// 成功记录也必须在返回前落库，不依赖异步调度。
	recordSuccess(state, &item, "123", "", "", nil)
	history, err = database.ListHistory()
	if err != nil || len(history) != 1 || history[0].OrderID != "123" {
		t.Fatalf("success not persisted: %+v %v", history, err)
	}
}
