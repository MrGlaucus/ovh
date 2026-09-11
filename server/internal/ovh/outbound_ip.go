package ovh

import (
	"context"
	"encoding/json"
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
	outboundIPURL = "https://uapis.cn/api/v1/network/myip"
	outboundIPTTL = 30 * time.Second
)

// OutboundIPStatusWriter persists an outbound-IP check without writing any
// credential fields back to SQLite.
type OutboundIPStatusWriter func(accountID, actualIP, status, checkedAt, reason string) error

type outboundIPResult struct {
	actualIP string
	status   string
	reason   string
	checked  time.Time
}

type outboundIPChecker struct {
	lookup AccountLookup
	write  OutboundIPStatusWriter

	mu      sync.Mutex
	results map[string]outboundIPResult
	locks   map[string]*sync.Mutex
	url     string
	now     func() time.Time
}

func newOutboundIPChecker(lookup AccountLookup) *outboundIPChecker {
	return &outboundIPChecker{
		lookup:  lookup,
		results: make(map[string]outboundIPResult),
		locks:   make(map[string]*sync.Mutex),
		url:     outboundIPURL,
		now:     time.Now,
	}
}

func (c *outboundIPChecker) reset(accountID string) {
	c.mu.Lock()
	delete(c.results, accountID)
	c.mu.Unlock()
}

func (c *outboundIPChecker) resetAll() {
	c.mu.Lock()
	c.results = make(map[string]outboundIPResult)
	c.mu.Unlock()
}

func (c *outboundIPChecker) accountLock(accountID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock := c.locks[accountID]
	if lock == nil {
		lock = &sync.Mutex{}
		c.locks[accountID] = lock
	}
	return lock
}

func validIPv4(value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value))
	return ip != nil && ip.To4() != nil && ip.String() == value
}

func (c *outboundIPChecker) cached(accountID string, force bool) (outboundIPResult, bool) {
	if force {
		return outboundIPResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	result, ok := c.results[accountID]
	return result, ok && c.now().Sub(result.checked) < outboundIPTTL
}

func (c *outboundIPChecker) store(accountID string, result outboundIPResult) {
	c.mu.Lock()
	c.results[accountID] = result
	c.mu.Unlock()
}

func (c *outboundIPChecker) statusWriter() OutboundIPStatusWriter {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.write
}

func (c *outboundIPChecker) check(ctx context.Context, accountID string, force bool) error {
	if c.lookup == nil {
		return fmt.Errorf("出口 IP 未确认，OVH 请求已阻断：账户查询器不可用")
	}
	acc, ok := c.lookup(accountID)
	if !ok || acc.ID == "" {
		return fmt.Errorf("出口 IP 未确认，OVH 请求已阻断：账户不存在")
	}
	if !validIPv4(acc.ExpectedOutboundIP) {
		return fmt.Errorf("出口 IP 未确认，OVH 请求已阻断：未配置有效的预期出口 IPv4")
	}
	if cached, ok := c.cached(acc.ID, force); ok {
		if cached.status == "verified" {
			return nil
		}
		return fmt.Errorf("出口 IP 未确认，OVH 请求已阻断：%s", cached.reason)
	}

	lock := c.accountLock(acc.ID)
	lock.Lock()
	defer lock.Unlock()
	if cached, ok := c.cached(acc.ID, force); ok {
		if cached.status == "verified" {
			return nil
		}
		return fmt.Errorf("出口 IP 未确认，OVH 请求已阻断：%s", cached.reason)
	}

	actual, reason := c.detect(ctx, acc)
	status := "verified"
	if reason != "" {
		status = "blocked"
	} else if actual != acc.ExpectedOutboundIP {
		status = "blocked"
		reason = "实际出口 IP 与预期不一致"
	}
	result := outboundIPResult{actualIP: actual, status: status, reason: reason, checked: c.now()}
	if writer := c.statusWriter(); writer != nil {
		if err := writer(acc.ID, actual, status, result.checked.Format(time.RFC3339), reason); err != nil {
			return fmt.Errorf("出口 IP 未确认，OVH 请求已阻断：保存校验状态失败")
		}
	}
	c.store(acc.ID, result)
	if status != "verified" {
		return fmt.Errorf("出口 IP 未确认，OVH 请求已阻断：%s", reason)
	}
	return nil
}

// ProbeOutboundIP performs an unauthenticated one-off probe for a prospective
// account form. It accepts only the account proxy setting and never persists or
// reads OVH credentials.
func ProbeOutboundIP(ctx context.Context, proxyURL string) (string, error) {
	checker := newOutboundIPChecker(nil)
	actual, reason := checker.detect(ctx, types.OVHAccount{ProxyURL: proxyURL})
	if reason != "" {
		return "", fmt.Errorf("%s", reason)
	}
	return actual, nil
}

func (c *outboundIPChecker) detect(ctx context.Context, acc types.OVHAccount) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var client *http.Client
	var err error
	if strings.TrimSpace(acc.ProxyURL) != "" {
		client, err = proxy.AccountHTTPClient(acc.ProxyURL, 10*time.Second)
		if err != nil {
			return "", "账户代理配置无效"
		}
	} else {
		client = proxy.DirectHTTPClient(10 * time.Second)
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return "", "出口 IP 查询请求构造失败"
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "出口 IP 查询失败"
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", "出口 IP 查询返回非成功状态"
	}
	var body struct {
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&body); err != nil {
		return "", "出口 IP 查询响应无有效 IP"
	}
	if !validIPv4(body.IP) {
		return "", "出口 IP 查询响应无有效 IPv4"
	}
	return body.IP, ""
}

// guardedTransport refuses signed OVH calls unless the account's independently
// checked outbound IP is still verified. The IP probe always uses another
// client/transport, so it cannot recurse through this gate or reuse OVH pools.
type guardedTransport struct {
	base  http.RoundTripper
	check func(context.Context) error
}

func (t guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("X-Ovh-Application") != "" {
		if err := t.check(req.Context()); err != nil {
			return nil, err
		}
	}
	return t.base.RoundTrip(req)
}
