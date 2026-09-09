package ovh

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/ovh/go-ovh/ovh"

	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/outboundip"
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
	lookup   AccountLookup
	fallback *config.Store // 兼容老 Client() 调用,等所有 callsite 迁完可移除

	mu    sync.Mutex
	cache map[string]*ovh.Client // accountID → client
	gate  *outboundip.Gate
}

// NewFactory 构造工厂。lookup 由 State 闭包注入。
func NewFactory(cfg *config.Store, lookup AccountLookup) *Factory {
	return &Factory{
		lookup:   lookup,
		fallback: cfg,
		cache:    map[string]*ovh.Client{},
		gate:     outboundip.NewGate(),
	}
}

// ClientFor 返回指定账户的 OVH client。accountID="" 走默认账户。
// 凭据缺失 / 账户不存在返回 error;同账户重复调用复用缓存实例。
func (f *Factory) ClientFor(accountID string) (*ovh.Client, error) {
	if f.lookup == nil {
		// State 还没把 lookup 注入(理论上不会发生)→ 退到 fallback
		return f.Client()
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
	var clientTransport = proxy.DirectHTTPClient(0).Transport
	if acc.ProxyURL != "" {
		client, err := proxy.AccountHTTPClient(acc.ProxyURL, 0)
		if err != nil {
			return nil, err
		}
		clientTransport = client.Transport
	}
	// 每个签名请求都会在真正出网前通过出口 IP 闸门；失败时 RoundTrip 直接返回，
	// 不会向 OVH 发送任何请求。
	cli.Client = &http.Client{Transport: outboundip.GuardedTransport{Base: clientTransport, Gate: f.gate, Account: acc}}
	f.cache[acc.ID] = cli
	return cli, nil
}

// CheckOutboundIP 主动执行一次出口 IP 校验，供后台守护和账户状态 API 使用。
func (f *Factory) CheckOutboundIP(accountID string, force bool) (outboundip.Status, error) {
	acc, ok := f.lookup(accountID)
	if !ok {
		return outboundip.Status{}, fmt.Errorf("ovh account %s not found", accountID)
	}
	return f.gate.Ensure(acc, force), nil
}

func (f *Factory) OutboundIPStatus(accountID string) (outboundip.Status, error) {
	acc, ok := f.lookup(accountID)
	if !ok {
		return outboundip.Status{}, fmt.Errorf("ovh account %s not found", accountID)
	}
	return f.gate.Status(acc), nil
}

// Invalidate 清掉指定账户的缓存 client(更新 / 删除账户后调,避免拿到旧凭据)
func (f *Factory) Invalidate(accountID string) {
	f.mu.Lock()
	delete(f.cache, accountID)
	f.mu.Unlock()
	f.gate.Invalidate(accountID)
}

// InvalidateAll 清全部缓存(比如重置 OVH 配置时)
func (f *Factory) InvalidateAll() {
	f.mu.Lock()
	f.cache = map[string]*ovh.Client{}
	f.mu.Unlock()
}

// Client 老接口,等价于 ClientFor("")(默认账户)。
// 未迁移到 ClientFor 的旧调用站点先用它;新代码不要用这个。
//
// Deprecated: 调用方应明确传 accountID。
func (f *Factory) Client() (*ovh.Client, error) {
	// 优先走 lookup 拿默认账户
	if f.lookup != nil {
		if cli, err := f.ClientFor(""); err == nil {
			return cli, nil
		}
		// lookup 找不到任何账户,退到旧 cfg
	}
	// fallback: 从 config.Store 拿凭据(老逻辑,P2 完全迁移后可删)
	if f.fallback == nil {
		return nil, fmt.Errorf("no default OVH account configured")
	}
	// 老配置没有账户出口 IP 绑定，保守拒绝其带签名请求，要求迁移到账户配置。
	return nil, fmt.Errorf("legacy OVH config has no verified account outbound IP")
}
