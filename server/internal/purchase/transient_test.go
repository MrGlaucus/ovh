package purchase

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"

	ovhsdk "github.com/ovh/go-ovh/ovh"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/logger"
	"github.com/ovh-buy/server/internal/storage"
	"github.com/ovh-buy/server/internal/types"
)

// 429 曾经被算成一次"真正的下单失败尝试"。补货那一刻所有人都在打同一个接口,
// OVH 限流是常态 —— MaxRetries=5 / retryInterval=10s 的任务会在不到一分钟里
// 被自己判死,而那一分钟正是唯一有货的窗口。
func TestTransientDoesNotBurnRetryBudget(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		transient bool
	}{
		{"429 限流", &ovhsdk.APIError{Code: 429, Message: "Too many requests"}, true},
		{"408 超时", &ovhsdk.APIError{Code: 408}, true},
		{"500 OVH 自己挂了", &ovhsdk.APIError{Code: 500}, true},
		{"502 网关", &ovhsdk.APIError{Code: 502}, true},
		{"503 不可用", &ovhsdk.APIError{Code: 503}, true},
		{"504 网关超时", &ovhsdk.APIError{Code: 504}, true},

		{"400 参数错", &ovhsdk.APIError{Code: 400, Message: "Invalid planCode"}, false},
		{"401 凭据错", &ovhsdk.APIError{Code: 401}, false},
		{"403 无权限", &ovhsdk.APIError{Code: 403}, false},
		{"404 机型不在本区", &ovhsdk.APIError{Code: 404}, false},
		{"409 冲突", &ovhsdk.APIError{Code: 409}, false},

		{"连接被重置", errors.New("read tcp 1.2.3.4:443: connection reset by peer"), true},
		{"DNS 解析不了", errors.New("dial tcp: lookup eu.api.ovh.com: no such host"), true},
		{"IO 超时", errors.New("net/http: request canceled (Client.Timeout exceeded)"), true},
		{"EOF", errors.New("unexpected EOF"), true},

		{"业务拒绝", errors.New("this plan is not available in datacenter rbx"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsTransient(c.err); got != c.transient {
				t.Fatalf("IsTransient(%v) = %v, 期望 %v", c.err, got, c.transient)
			}
			// Attempted 是 FailureCount 的开关:transient 必须不计数
			if got := attemptOutcome(c.err).Attempted; got == c.transient && c.err != nil {
				t.Fatalf("attemptOutcome(%v).Attempted = %v,与 transient=%v 矛盾", c.err, got, c.transient)
			}
		})
	}
}

// 包了一层的错误也要认得出来 —— 代码里到处是 fmt.Errorf("...: %w", err)
func TestTransientThroughWrappedError(t *testing.T) {
	wrapped := fmt.Errorf("加购基础商品失败: %w", &ovhsdk.APIError{Code: 429})
	if !IsTransient(wrapped) {
		t.Fatal("包了一层的 429 应当仍被认成 transient")
	}
}

// net.Error 的 Timeout() 路径
type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "some opaque failure" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }

func TestTransientNetTimeout(t *testing.T) {
	var e net.Error = fakeTimeout{}
	if !IsTransient(e) {
		t.Fatal("net.Error.Timeout()==true 应当算 transient")
	}
}

// 408/429 这类瞬时错误曾经也无条件写抢购历史 —— 无货轮询里偶发一次 408,
// 就把这条任务此前有价值的失败原因覆盖成一句 408 HTML。历史按 TaskID 就地
// 覆盖(每任务一条),"型号一直无货、队列没减少、历史里却在涨失败"正是这么来的。
// failOutcome 要求瞬时错误只留在日志里:不写历史、不覆盖旧消息。
func TestFailOutcomeSkipsHistoryForTransient(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(dir)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	lg := logger.New(filepath.Join(dir, "t.log"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	state := app.NewState(storage.Paths{DataDir: dir}, config.New(database), lg, database)

	item := &types.QueueItem{ID: "task-408", PlanCode: "24sk202", Datacenter: "gra"}

	// 408:请求没完整送达,业务层根本没表态 —— 不写历史,也不计 FailureCount
	out := failOutcome(state, item, &ovhsdk.APIError{Code: 408, Message: "408 Request Time-out"}, "购买 24sk202 时发生 OVH API 错误: 408")
	if out.Attempted {
		t.Fatal("408 不该被记成一次真正的失败尝试")
	}
	state.HistoryMu.Lock()
	n := len(state.History)
	state.HistoryMu.Unlock()
	if n != 0 {
		t.Fatalf("408 不应写抢购历史,实际写入了 %d 条", n)
	}

	// 404:确定性业务失败 —— 历史要留痕、计数要累加
	out = failOutcome(state, item, &ovhsdk.APIError{Code: 404, Message: "not available in datacenter"}, "机型不在本区目录")
	if !out.Attempted {
		t.Fatal("404 应当被记成一次真正的失败尝试")
	}
	state.HistoryMu.Lock()
	defer state.HistoryMu.Unlock()
	if len(state.History) != 1 {
		t.Fatalf("404 应当写一条抢购历史,实际 %d 条", len(state.History))
	}
	if state.History[0].TaskID != item.ID || state.History[0].Status != "failed" {
		t.Fatalf("历史条目不符合预期: TaskID=%s Status=%s", state.History[0].TaskID, state.History[0].Status)
	}
}
