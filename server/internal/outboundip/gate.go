// Package outboundip verifies that an account's dedicated network path has the expected IPv4 address.
package outboundip

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ovh-buy/server/internal/proxy"
	"github.com/ovh-buy/server/internal/types"
)

const (
	TTL     = 30 * time.Second
	timeout = 10 * time.Second
)

// serviceURL is fixed in production; package-local tests replace it with an httptest endpoint.
var serviceURL = "https://www.cloudflare.com/cdn-cgi/trace"

type Status struct {
	ExpectedIP string
	ActualIP   string
	State      string // pending / verified / mismatch / failed
	CheckedAt  time.Time
	Error      string
}

func (s Status) Allowed() bool {
	return s.State == "direct" || (s.State == "verified" && s.ExpectedIP != "")
}

func (s Status) Reason() string {
	if s.Error != "" {
		return s.Error
	}
	if s.ExpectedIP == "" {
		return "未配置有效的预期出口 IPv4"
	}
	if s.State == "mismatch" {
		return fmt.Sprintf("实际出口 IP %s 与预期 %s 不一致", s.ActualIP, s.ExpectedIP)
	}
	return "出口 IP 尚未验证"
}

type flight struct{ done chan struct{} }

type Gate struct {
	mu       sync.Mutex
	statuses map[string]Status
	flights  map[string]*flight
}

func NewGate() *Gate {
	return &Gate{statuses: map[string]Status{}, flights: map[string]*flight{}}
}

func validIPv4(raw string) string {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil || ip.To4() == nil {
		return ""
	}
	return ip.To4().String()
}

func fromAccount(a types.OVHAccount) Status {
	checked, _ := time.Parse(time.RFC3339Nano, a.OutboundIPCheckedAt)
	return Status{
		ExpectedIP: validIPv4(a.ExpectedOutboundIP),
		ActualIP:   validIPv4(a.ActualOutboundIP),
		State:      a.OutboundIPStatus,
		CheckedAt:  checked,
		Error:      a.OutboundIPError,
	}
}

// Seed restores the most recently persisted result on reload. A stale successful
// result is never trusted: Ensure will check it again before an OVH request.
func (g *Gate) Seed(a types.OVHAccount) {
	g.mu.Lock()
	g.statuses[a.ID] = fromAccount(a)
	g.mu.Unlock()
}

func (g *Gate) Invalidate(id string) {
	g.mu.Lock()
	delete(g.statuses, id)
	g.mu.Unlock()
}

func (g *Gate) Status(a types.OVHAccount) Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := g.statuses[a.ID]; ok {
		return s
	}
	return fromAccount(a)
}

// Ensure verifies when forced, missing, or older than TTL. It is single-flight
// per account so concurrent purchase requests cannot stampede the IP service.
func (g *Gate) Ensure(a types.OVHAccount, force bool) Status {
	// 直连账户没有代理出口绑定：不调用第三方查询、不做拦截。
	if strings.TrimSpace(a.ProxyURL) == "" {
		return Status{State: "direct"}
	}
	for {
		g.mu.Lock()
		s, exists := g.statuses[a.ID]
		if !exists || s.ExpectedIP != validIPv4(a.ExpectedOutboundIP) {
			s = fromAccount(a)
			g.statuses[a.ID] = s
		}
		if !force && s.Allowed() && time.Since(s.CheckedAt) < TTL {
			g.mu.Unlock()
			return s
		}
		if running := g.flights[a.ID]; running != nil {
			done := running.done
			g.mu.Unlock()
			<-done
			force = false
			continue
		}
		g.flights[a.ID] = &flight{done: make(chan struct{})}
		g.mu.Unlock()

		result := safeVerify(a)
		g.mu.Lock()
		g.statuses[a.ID] = result
		running := g.flights[a.ID]
		delete(g.flights, a.ID)
		close(running.done)
		g.mu.Unlock()
		return result
	}
}

// Verify performs one unsigned Cloudflare trace request through the account path.
func Verify(a types.OVHAccount) Status { return safeVerify(a) }

// safeVerify guarantees a failed probe cannot strand concurrent waiters if a dependency panics.
func safeVerify(a types.OVHAccount) (out Status) {
	defer func() {
		if recover() != nil {
			out = Status{ExpectedIP: validIPv4(a.ExpectedOutboundIP), State: "failed", CheckedAt: time.Now().UTC(), Error: "出口 IP 检测发生内部错误"}
		}
	}()
	if strings.TrimSpace(a.ProxyURL) == "" {
		return Status{State: "direct"}
	}
	return verify(a)
}

func verify(a types.OVHAccount) Status {
	expected := validIPv4(a.ExpectedOutboundIP)
	out := Status{ExpectedIP: expected, State: "failed", CheckedAt: time.Now().UTC()}
	if expected == "" {
		out.Error = "未配置有效的预期出口 IPv4"
		return out
	}
	var client *http.Client
	var err error
	if strings.TrimSpace(a.ProxyURL) != "" {
		client, err = proxy.AccountHTTPClient(a.ProxyURL, timeout)
	} else {
		client = proxy.DirectHTTPClient(timeout)
	}
	// 查询目标是固定 allowlist；拒绝服务端重定向，避免被上游响应带去内网地址。
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if err != nil {
		out.Error = "账户代理配置无效"
		return out
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serviceURL, nil)
	if err != nil {
		out.Error = "创建出口 IP 查询请求失败"
		return out
	}
	resp, err := client.Do(req)
	if err != nil {
		out.Error = "出口 IP 查询失败"
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.Error = fmt.Sprintf("出口 IP 查询返回 HTTP %d", resp.StatusCode)
		return out
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		out.Error = "出口 IP 查询响应读取失败"
		return out
	}
	for _, line := range strings.Split(string(body), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && key == "ip" {
			out.ActualIP = validIPv4(value)
			break
		}
	}
	if out.ActualIP == "" {
		out.Error = "出口 IP 查询未解析到有效 IPv4"
		return out
	}
	if out.ActualIP != expected {
		out.State = "mismatch"
		out.Error = fmt.Sprintf("实际出口 IP %s 与预期 %s 不一致", out.ActualIP, expected)
		return out
	}
	out.State = "verified"
	out.Error = ""
	return out
}

// GuardedTransport checks the account's outbound IP immediately before every
// signed OVH HTTP request. On failure it returns before any OVH request exists.
type GuardedTransport struct {
	Base    http.RoundTripper
	Gate    *Gate
	Account types.OVHAccount
}

func (t GuardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	status := t.Gate.Ensure(t.Account, false)
	if !status.Allowed() {
		return nil, fmt.Errorf("账户出口 IP 未确认，携带账户鉴权的 OVH 请求已阻断：%s", status.Reason())
	}
	return t.Base.RoundTrip(req)
}
