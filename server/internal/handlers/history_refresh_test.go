package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func refreshRequest(t *testing.T, h gin.HandlerFunc, method string) (int, historyRefreshStatus) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/purchase-history/refresh-status", nil)
	h(c)
	var status historyRefreshStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return w.Code, status
}

func waitRefresh(t *testing.T, h gin.HandlerFunc) historyRefreshStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, s := refreshRequest(t, h, http.MethodGet)
		if s.Status != "running" {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker did not complete")
	return historyRefreshStatus{}
}

func TestHistoryRefreshDoesNotWaitForWorkerOrDuplicateIt(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	var calls atomic.Int32
	h := newHistoryRefreshHandler(func(phase func(string)) (int, int, error) {
		calls.Add(1)
		phase("refunds")
		<-gate
		return 7, 2, nil
	}, func(error) {})
	_, idle := refreshRequest(t, h, http.MethodGet)
	if idle.Status != "idle" {
		t.Fatal(idle)
	}
	code, started := refreshRequest(t, h, http.MethodPost)
	if code != 202 || started.Status != "running" || started.ID == "" {
		t.Fatal(code, started)
	}
	code, repeated := refreshRequest(t, h, http.MethodPost)
	if code != 202 || repeated.ID != started.ID {
		t.Fatal(code, repeated)
	}
	_, polled := refreshRequest(t, h, http.MethodGet)
	if polled.ID != started.ID || polled.Status != "running" {
		t.Fatal(polled)
	}
	gate <- struct{}{}
	finished := waitRefresh(t, h)
	if finished.Status != "completed" || finished.Updated != 7 || finished.RefundFailed != 2 || calls.Load() != 1 {
		t.Fatal(finished, calls.Load())
	}
	code, _ = refreshRequest(t, h, http.MethodPost)
	if code != 429 {
		t.Fatalf("expected throttling, got %d", code)
	}
}

func TestHistoryRefreshReportsWorkerFailure(t *testing.T) {
	for _, panicWorker := range []bool{false, true} {
		h := newHistoryRefreshHandler(func(func(string)) (int, int, error) {
			if panicWorker {
				panic("test")
			}
			return 0, 0, errors.New("save failed")
		}, func(error) {})
		refreshRequest(t, h, http.MethodPost)
		s := waitRefresh(t, h)
		if s.Status != "failed" || s.Error == "" {
			t.Fatal(s)
		}
	}
}
