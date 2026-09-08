package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// 每个用例结束都归位:别的用例可能没调 Init,残留的全局代理配置会串场。
func resetProxy(t *testing.T) {
	t.Helper()
	if err := Init(""); err != nil {
		t.Fatalf("reset proxy: %v", err)
	}
}

func TestInitParse(t *testing.T) {
	cases := []struct {
		raw    string
		ok     bool
		masked string
		scheme string
	}{
		{"socks5://127.0.0.1:10086", true, "socks5://127.0.0.1:10086", "socks5"},
		{"socks5://user:pass@127.0.0.1:10086", true, "socks5://***@127.0.0.1:10086", "socks5"},
		{"http://127.0.0.1:7890", true, "http://127.0.0.1:7890", "http"},
		{"HTTPS://proxy.example.com:443", true, "https://proxy.example.com:443", "https"},
		{"ftp://127.0.0.1:21", false, "", ""},
		{"socks5://", false, "", ""},
		{"://nohost", false, "", ""},
	}
	for _, c := range cases {
		err := Init(c.raw)
		if c.ok != (err == nil) {
			t.Errorf("Init(%q) err=%v, 期望 ok=%v", c.raw, err, c.ok)
			continue
		}
		if !c.ok {
			continue
		}
		if got := MaskedAddress(); got != c.masked {
			t.Errorf("MaskedAddress() = %q, 期望 %q", got, c.masked)
		}
		if got := Scheme(); got != c.scheme {
			t.Errorf("Scheme() = %q, 期望 %q", got, c.scheme)
		}
		if !Configured() {
			t.Errorf("Init(%q) 后 Configured() 应为 true", c.raw)
		}
	}
	resetProxy(t)
}

func TestInitEmptyDisables(t *testing.T) {
	if err := Init("socks5://127.0.0.1:10086"); err != nil {
		t.Fatal(err)
	}
	if err := Init(""); err != nil {
		t.Fatal(err)
	}
	if Configured() {
		t.Error("空配置后 Configured() 应为 false")
	}
	if got := Scheme(); got != "direct" {
		t.Errorf("空配置后 Scheme() = %q, 期望 direct", got)
	}
	if MaskedAddress() != "" {
		t.Error("空配置后 MaskedAddress() 应为空串")
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":    true,
		"127.0.0.2":    true, // 整个 127/8 都是回环
		"::1":          true,
		"[::1]":        true,
		"localhost":    true,
		"LOCALHOST":    true,
		"192.168.1.1":  false,
		"10.0.0.1":     false,
		"eu.api.ovh.com": false,
		"":             false,
	}
	for host, want := range cases {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %v, 期望 %v", host, got, want)
		}
	}
}

// TestProxyRouting 端到端验证代理路由:
// 非回环目标走代理,回环目标直连(代理收不到)。
func TestProxyRouting(t *testing.T) {
	resetProxy(t)

	var viaProxy atomic.Bool
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viaProxy.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer proxySrv.Close()

	if err := Init(proxySrv.URL); err != nil {
		t.Fatal(err)
	}
	client := HTTPClient(5 * time.Second)

	// 1. 非回环目标 → 必须经过代理(example.test 不解析也没关系,代理直接应答)
	resp, err := client.Get("http://example.test/x")
	if err != nil {
		t.Fatalf("经代理请求失败: %v", err)
	}
	resp.Body.Close()
	if !viaProxy.Load() {
		t.Error("非回环目标没有经过代理")
	}

	// 2. 回环目标 → 必须直连(代理不应再收到新请求)
	viaProxy.Store(false)
	var directHit atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	resp2, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("回环直连请求失败: %v", err)
	}
	resp2.Body.Close()
	if !directHit.Load() {
		t.Error("回环目标没有被目标服务器收到")
	}
	if viaProxy.Load() {
		t.Error("回环目标经过了代理,应直连")
	}

	resetProxy(t)
}

// TestCheckCached 缓存语义:TTL 内第二次 Check(false) 不重新探测。
// 用返回的 CheckedAt 是否变化来判定。
func TestCheckCached(t *testing.T) {
	resetProxy(t)
	st1, err := Check(true)
	if err != nil {
		t.Fatal(err)
	}
	if st1.Configured {
		t.Error("未配置代理时 Configured 应为 false")
	}
	if len(st1.Targets) != len(Targets) {
		t.Fatalf("Targets 数量 = %d, 期望 %d", len(st1.Targets), len(Targets))
	}
	st2, err := Check(false)
	if err != nil {
		t.Fatal(err)
	}
	if st1.CheckedAt != st2.CheckedAt {
		t.Errorf("TTL 内 Check(false) 不应重新探测: %v → %v", st1.CheckedAt, st2.CheckedAt)
	}
	// 未配置时 Proxy 应为空串、Mode 为 direct
	if st1.Proxy != "" || st1.Mode != "direct" {
		t.Errorf("未配置时 Proxy=%q Mode=%q, 期望空串+direct", st1.Proxy, st1.Mode)
	}
}
