package outboundip

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/types"
)

func withIPService(t *testing.T, ip string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "fl=582f232\ncolo=HKG\nip="+ip+"\nwarp=off\n")
	}))
	old := serviceURL
	serviceURL = srv.URL
	t.Cleanup(func() { serviceURL = old; srv.Close() })
	return srv.URL
}

func TestGateAllowsMatchingIPv4(t *testing.T) {
	proxyURL := withIPService(t, "198.51.100.10")
	status := NewGate().Ensure(types.OVHAccount{ID: "a", ProxyURL: proxyURL, ExpectedOutboundIP: "198.51.100.10"}, true)
	if !status.Allowed() || status.ActualIP != "198.51.100.10" {
		t.Fatalf("expected verified matching IP, got %#v", status)
	}
}

func TestGateSkipsDirectAccount(t *testing.T) {
	status := NewGate().Ensure(types.OVHAccount{ID: "direct"}, true)
	if !status.Allowed() || status.State != "direct" {
		t.Fatalf("expected direct account to bypass IP verification, got %#v", status)
	}
}

func TestGateBlocksMismatchAndInvalidExpectedIP(t *testing.T) {
	proxyURL := withIPService(t, "198.51.100.10")
	mismatch := NewGate().Ensure(types.OVHAccount{ID: "a", ProxyURL: proxyURL, ExpectedOutboundIP: "198.51.100.11"}, true)
	if mismatch.Allowed() || mismatch.State != "mismatch" {
		t.Fatalf("expected mismatch block, got %#v", mismatch)
	}
	invalid := NewGate().Ensure(types.OVHAccount{ID: "b", ProxyURL: proxyURL, ExpectedOutboundIP: "not-an-ip"}, true)
	if invalid.Allowed() || invalid.State != "failed" {
		t.Fatalf("expected invalid configuration block, got %#v", invalid)
	}
}

type countingTransport struct{ calls atomic.Int32 }

func (t *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return nil, io.EOF
}

func TestGuardedTransportDoesNotSendWhenIPIsUnverified(t *testing.T) {
	base := &countingTransport{}
	guard := GuardedTransport{
		Base:    base,
		Gate:    NewGate(),
		Account: types.OVHAccount{ID: "blocked", ProxyURL: "http://127.0.0.1:1", ExpectedOutboundIP: "not-an-ip"},
	}
	req := httptest.NewRequest(http.MethodGet, "https://api.ovh.com/1.0/me", nil)
	_, err := guard.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "已阻断") {
		t.Fatalf("expected blocked request error, got %v", err)
	}
	if base.calls.Load() != 0 {
		t.Fatalf("OVH base transport was called despite blocked IP state")
	}
}

// withCountingIPService 与 withIPService 相同,但统计探测请求次数,
// 用于验证失败态负缓存真的少发了探测。
func withCountingIPService(t *testing.T, ip string) (*atomic.Int32, string) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "fl=582f232\nip="+ip+"\n")
	}))
	old := serviceURL
	serviceURL = srv.URL
	t.Cleanup(func() { serviceURL = old; srv.Close() })
	return &calls, srv.URL
}

// 失败结论必须进负缓存:没有它,出口不匹配时每个签名请求都要复检,
// 监控一轮 N 个请求 = N 次串行探测,「检查可能停滞」就是这么来的。
func TestGateFailureIsNegativelyCached(t *testing.T) {
	calls, proxyURL := withCountingIPService(t, "198.51.100.10")
	gate := NewGate()
	acc := types.OVHAccount{ID: "a", ProxyURL: proxyURL, ExpectedOutboundIP: "198.51.100.11"}

	if s := gate.Ensure(acc, false); s.Allowed() || s.State != "mismatch" {
		t.Fatalf("expected mismatch, got %#v", s)
	}
	if s := gate.Ensure(acc, false); s.Allowed() || s.State != "mismatch" {
		t.Fatalf("expected cached mismatch, got %#v", s)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("negative cache missed: probe called %d times, want 1", got)
	}
}

// force 路径(后台 30 秒复检 / 手动检测)必须穿透负缓存:恢复要快。
func TestGateForcedCheckBypassesNegativeCache(t *testing.T) {
	calls, proxyURL := withCountingIPService(t, "198.51.100.10")
	gate := NewGate()
	acc := types.OVHAccount{ID: "a", ProxyURL: proxyURL, ExpectedOutboundIP: "198.51.100.11"}

	_ = gate.Ensure(acc, false)
	_ = gate.Ensure(acc, true)
	if got := calls.Load(); got != 2 {
		t.Fatalf("forced check must bypass negative cache: probe called %d times, want 2", got)
	}
}

// 负缓存过期后下一个请求必须立即真实探测,恢复不被拖慢。
func TestGateNegativeCacheExpires(t *testing.T) {
	calls, proxyURL := withCountingIPService(t, "198.51.100.10")
	gate := NewGate()
	acc := types.OVHAccount{ID: "a", ProxyURL: proxyURL, ExpectedOutboundIP: "198.51.100.11"}

	_ = gate.Ensure(acc, false)
	old := FailedTTL
	FailedTTL = 20 * time.Millisecond
	t.Cleanup(func() { FailedTTL = old })
	time.Sleep(30 * time.Millisecond)
	_ = gate.Ensure(acc, false)
	if got := calls.Load(); got != 2 {
		t.Fatalf("expired negative cache must re-probe: probe called %d times, want 2", got)
	}
}
