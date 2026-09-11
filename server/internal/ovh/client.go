package ovh

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/ovh/go-ovh/ovh"

	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/proxy"
	"github.com/ovh-buy/server/internal/types"
)

// AccountLookup 由 State 提供,根据 id 找账户:
//
//	id == ""  → 找默认账户(或第一个);适合"未指定账户时 fallback"
//	id == "x" → 精确找 x
type AccountLookup func(id string) (types.OVHAccount, bool)

// Factory OVH client 工厂。按 accountID 缓存 client 实例;
// 同一账户多次取拿到同一个 client;Invalidate 失效特定账户的缓存。
//
// 不同账户即使 endpoint 相同(都 ovh-eu)也是独立 client(凭据不同),
// 缓存 key 是 accountID 不是 endpoint。
type Factory struct {
	lookup AccountLookup

	mu       sync.Mutex
	cache    map[string]*ovh.Client // accountID → client
	outbound *outboundIPChecker
}

// NewFactory 构造工厂。lookup 由 State 闭包注入。
func NewFactory(_ *config.Store, lookup AccountLookup) *Factory {
	return &Factory{
		lookup:   lookup,
		cache:    map[string]*ovh.Client{},
		outbound: newOutboundIPChecker(lookup),
	}
}

// SetOutboundIPStatusWriter configures persistence for probe outcomes. State
// wires this after constructing the Factory, avoiding an app↔ovh import cycle.
func (f *Factory) SetOutboundIPStatusWriter(writer OutboundIPStatusWriter) {
	f.outbound.mu.Lock()
	f.outbound.write = writer
	f.outbound.mu.Unlock()
}

// CheckOutboundIP explicitly refreshes an account's independent IP probe. It
// never uses an OVH client and therefore never sends credentials to the probe.
func (f *Factory) CheckOutboundIP(accountID string, force bool) error {
	return f.outbound.check(context.Background(), accountID, force)
}

// ClientFor 返回指定账户的 OVH client。accountID="" 走默认账户。
// 凭据缺失 / 账户不存在返回 error;同账户重复调用复用缓存实例。
func (f *Factory) ClientFor(accountID string) (*ovh.Client, error) {
	if f.lookup == nil {
		return nil, fmt.Errorf("no account lookup configured; OVH requests are blocked")
	}
	acc, ok := f.lookup(accountID)
	if !ok {
		if accountID == "" {
			return nil, fmt.Errorf("no default OVH account configured")
		}
		return nil, fmt.Errorf("ovh account %s not found", accountID)
	}
	if acc.AppKey == "" || acc.AppSecret == "" || acc.ConsumerKey == "" {
		return nil, fmt.Errorf("ovh account %s missing credentials", acc.ID)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if cli, ok := f.cache[acc.ID]; ok {
		return cli, nil
	}
	cli, err := ovh.NewClient(acc.Endpoint, acc.AppKey, acc.AppSecret, acc.ConsumerKey)
	if err != nil {
		return nil, err
	}
	// 账户专属 Transport，绝不使用公共 OUTBOUND_PROXY 或别的账户连接池。
	// 配了代理就固定经该代理；不可达时请求报错，绝不回退直连。
	var client *http.Client
	if acc.ProxyURL != "" {
		client, err = proxy.AccountHTTPClient(acc.ProxyURL, 0)
		if err != nil {
			return nil, err
		}
	} else {
		client = proxy.DirectHTTPClient(0)
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = guardedTransport{
		base: base,
		check: func(ctx context.Context) error {
			return f.outbound.check(ctx, acc.ID, false)
		},
	}
	cli.Client = client
	f.cache[acc.ID] = cli
	return cli, nil
}

// Invalidate 清掉指定账户的缓存 client(更新 / 删除账户后调,避免拿到旧凭据)
func (f *Factory) Invalidate(accountID string) {
	f.mu.Lock()
	delete(f.cache, accountID)
	f.mu.Unlock()
	f.outbound.reset(accountID)
}

// InvalidateAll 清全部缓存(比如重置 OVH 配置时)
func (f *Factory) InvalidateAll() {
	f.mu.Lock()
	f.cache = map[string]*ovh.Client{}
	f.mu.Unlock()
	f.outbound.resetAll()
}

// Client 老接口,等价于 ClientFor("")(默认账户)。
// 未迁移到 ClientFor 的旧调用站点先用它;新代码不要用这个。
//
// Deprecated: 调用方应明确传 accountID。
func (f *Factory) Client() (*ovh.Client, error) {
	// 优先走 lookup 拿默认账户
	if f.lookup != nil {
		// State 已注入账户查询器时，绝不退回旧 config 凭据；否则这条旧接口会
		// 绕过账户出口 IP 闸门。默认账户不存在也必须明确失败。
		return f.ClientFor("")
	}
	return nil, fmt.Errorf("no account lookup configured; OVH requests are blocked")
}
