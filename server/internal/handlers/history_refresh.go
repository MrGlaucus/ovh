package handlers

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/purchase"
)

type historyRefreshStatus struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Phase        string `json:"phase"`
	Updated      int    `json:"updated"`
	RefundFailed int    `json:"refundFailed"`
	Error        string `json:"error,omitempty"`
}

// RefreshOrderStatuses shares one background job between POST (start) and GET (poll).
// The worker never retains the HTTP context, so proxy timeouts and navigation do not cancel it.
func RefreshOrderStatuses(state *app.State) gin.HandlerFunc {
	return newHistoryRefreshHandler(func(phase func(string)) (int, int, error) {
		n := purchase.RefreshOrderStatuses(state, true)
		phase("refunds")
		n += purchase.RefreshRefundStatuses(state, true)
		failed := 0
		state.HistoryMu.Lock()
		for _, h := range state.History {
			if h.RefundCheckError != "" && h.Refund == nil {
				failed++
			}
		}
		state.HistoryMu.Unlock()
		return n, failed, state.SaveHistory()
	}, func(err error) { state.Logger.Error("刷新历史状态失败: "+err.Error(), "history") })
}

func newHistoryRefreshHandler(run func(func(string)) (int, int, error), logError func(error)) gin.HandlerFunc {
	var mu sync.Mutex
	status := historyRefreshStatus{Status: "idle"}
	var lastStart time.Time
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		mu.Lock()
		if c.Request.Method == http.MethodGet || status.Status == "running" {
			snapshot := status
			mu.Unlock()
			code := http.StatusOK
			if c.Request.Method == http.MethodPost {
				code = http.StatusAccepted
			}
			c.JSON(code, snapshot)
			return
		}
		if wait := 15*time.Second - time.Since(lastStart); wait > 0 {
			mu.Unlock()
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "刷新太频繁，请稍后重试", "retryAfterSeconds": int(wait.Seconds()) + 1})
			return
		}
		lastStart = time.Now()
		status = historyRefreshStatus{ID: uuid.NewString(), Status: "running", Phase: "orders"}
		snapshot := status
		mu.Unlock()
		go func() {
			var updated, failed int
			var err error
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("刷新任务异常: %v", recovered)
				}
				mu.Lock()
				status.Status, status.Phase = "completed", ""
				status.Updated, status.RefundFailed = updated, failed
				if err != nil {
					status.Status, status.Error = "failed", "刷新任务未完成，请查看服务器日志后重试"
				}
				mu.Unlock()
				if err != nil {
					logError(err)
				}
			}()
			updated, failed, err = run(func(phase string) {
				mu.Lock()
				status.Phase = phase
				mu.Unlock()
			})
		}()
		c.JSON(http.StatusAccepted, snapshot)
	}
}
