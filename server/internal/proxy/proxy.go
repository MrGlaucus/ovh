// Package proxy 提供全局出站代理:
//
//   - 环境变量 OUTBOUND_PROXY 配置(http / https / socks5 / socks5h,支持 user:pass@)
//   - 共享 Transport + HTTPClient(timeout) 工厂,所有出网请求统一走它
//   - 回环地址(127.0.0.1 / localhost / ::1)强制直连,绕过代理
//   - 对外部 host 的连通性探测(带 60s 缓存),供 /api/proxy/status 展示
//
// 为什么不用 http.ProxyFromEnvironment:它内部有 sync.Once 缓存,且会受系统
// 环境变量(HTTP_PROXY / HTTPS_PROXY)干扰 —— Windows 上"设置 → 网络代理"写入的
// 系统级 HTTP_PROXY 会让没配任何东西的程序悄悄走系统代理,行为不可预测。
// 独立变量 + 显式解析,配了就生效、没配就是直连,一眼看得懂。
package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// EnvVar 环境变量名。写在 server/.env 里,重启生效。
const EnvVar = "OUTBOUND_PROXY"

// CheckTTL 探测结果缓存时长。探测本身要走完整链路(最坏 ~10s),
// 前端 60s 轮询一次,缓存不新鲜才真探测。
const CheckTTL = 60 * time.Second

// probeTimeout 单个 host 的探测总超时
const probeTimeout = 10 * time.Second

// supportedSchemes 标准库 Transport.Proxy 支持的代理协议。
// socks5h 与 socks5 的区别:DNS 解析在代理端做。
var supportedSchemes = map[string]bool{
	"http": true, "https": true, "socks5": true, "socks5h": true,
}

var (
	mu        sync.RWMutex
	proxyURL  *url.URL // nil = 未配置(直连)
	rawAddr   string   // 原始配置值,报错时引用
	transport *http.Transport

	// 探测状态缓存
	probeMu   sync.Mutex
	probeLast Status
)

// Init 在启动早期调用一次(godotenv.Load 之后)。raw 为空表示不启用代理。
// 返回 error 表示 URL 非法,调用方决定是告警继续直连还是拒绝启动。
func Init(raw string) error {
	mu.Lock()
	defer mu.Unlock()

	if strings.TrimSpace(raw) == "" {
		proxyURL = nil
		rawAddr = ""
	} else {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("解析代理地址失败: %w", err)
		}
		if !supportedSchemes[strings.ToLower(u.Scheme)] {
			return fmt.Errorf("不支持的代理协议 %q(支持 http/https/socks5/socks5h)", u.Scheme)
		}
		if u.Host == "" || u.Hostname() == "" {
			return fmt.Errorf("代理地址缺少主机: %q", raw)
		}
		proxyURL = u
		rawAddr = raw
	}

	// Transport 无条件构建(未配置时代理回调返回 nil=直连):
	// http.Client.Transport 若被赋成 typed-nil 的 *http.Transport,
	// net/http 会当它非空直接调方法 —— 未配置代理时直接 panic。
	// Dialer 超时给短一些 —— 代理不可达时让请求快速失败,而不是挂 30 秒,
	// 抢购链路对延迟极其敏感。
	transport = &http.Transport{
		Proxy: proxyFunc,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	return nil
}

// proxyFunc 是 Transport 的 Proxy 回调:回环地址永远直连,其余走代理。
// 回环必须直连:monitor 通过 127.0.0.1:19998 自调 quick-order,走代理会
// 绕一圈甚至失败;代理软件(如本机 clash)往往也不处理指向自己的流量。
func proxyFunc(req *http.Request) (*url.URL, error) {
	mu.RLock()
	defer mu.RUnlock()
	if proxyURL == nil {
		return nil, nil
	}
	if isLoopback(req.URL.Hostname()) {
		return nil, nil
	}
	return proxyURL, nil
}

// isLoopback 判定主机名是否回环(含裸 IPv6 与 ::1)。
func isLoopback(host string) bool {
	h := strings.Trim(host, "[]")
	if h == "" {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(h, "localhost")
}

// Configured 是否配置了代理。
func Configured() bool {
	mu.RLock()
	defer mu.RUnlock()
	return proxyURL != nil
}

// Address 原始配置值(未脱敏)。仅内部使用,不要回传给前端。
func Address() string {
	mu.RLock()
	defer mu.RUnlock()
	return rawAddr
}

// MaskedAddress 脱敏后的展示值:userinfo 段整个替换成 ***,
// 避免把代理的账号密码随 /api/proxy/status 下发。
// 不用 url.User("***") —— userinfo 里的 * 会被转义成 %2A%2A%2A,可读性差。
func MaskedAddress() string {
	mu.RLock()
	defer mu.RUnlock()
	if proxyURL == nil {
		return ""
	}
	if proxyURL.User != nil {
		scheme := proxyURL.Scheme + "://"
		rest := proxyURL.String()[len(scheme):]
		if at := strings.Index(rest, "@"); at >= 0 {
			return scheme + "***@" + rest[at+1:]
		}
	}
	return proxyURL.String()
}

// Scheme 代理协议(http/socks5/...);未配置返回 "direct"。
func Scheme() string {
	mu.RLock()
	defer mu.RUnlock()
	if proxyURL == nil {
		return "direct"
	}
	return strings.ToLower(proxyURL.Scheme)
}

// ParseAccountProxy 校验账户级代理配置。账户级代理的请求使用专属 Transport，绝不共享全局连接池。
func ParseAccountProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("账户代理地址无效: %w", err)
	}
	if !supportedSchemes[strings.ToLower(u.Scheme)] || u.Hostname() == "" {
		return nil, fmt.Errorf("账户代理必须是有效的 http/https/socks5/socks5h 地址")
	}
	return u, nil
}

// Mask 脱敏任意代理地址，供账户 API 返回值使用。
func Mask(raw string) string {
	u, err := ParseAccountProxy(raw)
	if err != nil || u.User == nil {
		return raw
	}
	scheme := u.Scheme + "://"
	rest := u.String()[len(scheme):]
	if at := strings.Index(rest, "@"); at >= 0 {
		return scheme + "***@" + rest[at+1:]
	}
	return u.String()
}

// AccountHTTPClient 返回账户 OVH API 专用 client。非空代理不可能静默退回直连：
// Transport.Proxy 固定返回该代理，代理不可达时请求报错并被调用方拒绝。
func AccountHTTPClient(raw string, timeout time.Duration) (*http.Client, error) {
	u, err := ParseAccountProxy(raw)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if isLoopback(req.URL.Hostname()) {
				return nil, nil
			}
			return u, nil
		},
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 20, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: timeout}, nil
}

// DirectHTTPClient 明确直连，不读取系统 HTTP_PROXY，账户未配代理时只用它。
func DirectHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 20, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second,
	}, Timeout: timeout}
}

// HTTPClient 返回共享代理 Transport 的 http.Client。
// timeout<=0 表示不限总时长(与 http.Client 默认行为一致),仅受 Transport 层超时约束。
func HTTPClient(timeout time.Duration) *http.Client {
	mu.RLock()
	defer mu.RUnlock()
	c := &http.Client{Timeout: timeout}
	// transport==nil(本包从未被 Init,如独立测试进程)时不能赋值给 Transport:
	// typed-nil 的 *http.Transport 会让 net/http 当它非空直接调方法而 panic。
	// 不设置字段则 net/http 走 DefaultTransport,行为等同直连。
	if transport != nil {
		c.Transport = transport
	}
	return c
}

// ---------------------------------------------------------------------------
// 连通性探测
// ---------------------------------------------------------------------------

// Target 一个待探测的外部 host。
type Target struct {
	Host string // 探测的域名
	Path string // 探测路径(公开端点,不需要凭据)
}

// Targets 需要确认"能走代理"的外部 host 清单。
// 与 README 的「后端出网 host」一致;本机自调(127.0.0.1)不在其中 —— 它强制直连。
var Targets = []Target{
	{Host: "eu.api.ovh.com", Path: "/1.0/auth/time"},      // 公开端点,同时验证 TLS+HTTP 全链路
	{Host: "api.us.ovhcloud.com", Path: "/1.0/auth/time"}, // 同上
	{Host: "ca.api.ovh.com", Path: "/1.0/auth/time"},      // 同上
	{Host: "api.telegram.org", Path: "/"},                 // 无 token 时 404,拿到响应即证明连通
	{Host: "api.github.com", Path: "/"},                   // 检查更新用
	{Host: "github.com", Path: "/"},                       // 下载 release 资产用
}

// Result 单个 host 的探测结果。
type Result struct {
	Host      string `json:"host"`
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	HTTPCode  int    `json:"http_code"` // 0 = 没拿到 HTTP 响应
	Error     string `json:"error"`     // 失败时的原因(网络层错误文本)
}

// Status 完整代理状态,直接作为 /api/proxy/status 的响应体。
type Status struct {
	Configured bool     `json:"configured"`
	Proxy      string   `json:"proxy"` // 脱敏展示值;未配置为空串
	Mode       string   `json:"mode"`  // socks5 / http / direct
	CheckedAt  string   `json:"checked_at"`
	Targets    []Result `json:"targets"`

	// checkedAt 探测发生时刻,缓存新鲜度判定用,不下发前端
	checkedAt time.Time `json:"-"`
}

// Check 返回代理状态。force=true 强制真探测;false 时缓存未过期直接返回上次结果。
// 探测期间并发调用会共享同一次探测(probeMu 挡住),不会叠罗汉打目标。
func Check(force bool) (Status, error) {
	probeMu.Lock()
	defer probeMu.Unlock()

	if !force && !probeLast.checkedAt.IsZero() && time.Since(probeLast.checkedAt) < CheckTTL {
		return probeLast, nil
	}

	now := time.Now()
	st := Status{
		Configured: Configured(),
		Proxy:      MaskedAddress(),
		Mode:       Scheme(),
		CheckedAt:  now.Format(time.RFC3339),
		checkedAt:  now,
		Targets:    make([]Result, 0, len(Targets)),
	}
	if !st.Configured {
		// 未配置代理:直连探测作为基线参考,让右上角始终有信息。
		// Targets 照常填充(结果全部是直连链路)。
	}
	probeAll(&st)

	probeLast = st
	return st, nil
}

// probeAll 并发探测全部目标,结果按 Targets 声明顺序写回。
func probeAll(st *Status) {
	results := make([]Result, len(Targets))
	var wg sync.WaitGroup
	for i, t := range Targets {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			results[i] = probeOne(t)
		}(i, t)
	}
	wg.Wait()
	st.Targets = results
}

// probeOne 探测单个 host。
//
// 判定"连通"的标准:拿到任何 HTTP 响应都算(哪怕 401/404)——
// 代理的职责是通路不是认证,404 说明请求确实到了目标服务器。
// 不跟随重定向:第一跳的响应就足以证明链路,追下去反而把
// "到了 A 站但被跳到 B 站"误判成 A 站连通。
func probeOne(t Target) Result {
	r := Result{Host: t.Host}

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+t.Host+t.Path, nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	req.Header.Set("User-Agent", "OVH-Console-ProxyProbe")

	client := HTTPClient(probeTimeout)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	start := time.Now()
	resp, err := client.Do(req)
	r.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer resp.Body.Close()
	r.OK = true
	r.HTTPCode = resp.StatusCode
	return r
}
