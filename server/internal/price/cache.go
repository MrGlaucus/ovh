package price

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ovh-buy/server/internal/app"
)

const cacheTTL = 20 * time.Second
const cacheLimit = 256

type cachedPrice struct {
	done    chan struct{}
	result  Result
	expires time.Time
}

type priceCache struct {
	mu      sync.Mutex
	entries map[string]*cachedPrice
}

var sharedPrices = priceCache{entries: make(map[string]*cachedPrice)}

// get 合并同一配置的并发询价；只复用完整成功结果，故障和降级结果不缓存。
func (c *priceCache) get(key string, fetch func() Result) Result {
	c.mu.Lock()
	now := time.Now()
	for k, entry := range c.entries {
		if !entry.expires.IsZero() && !now.Before(entry.expires) {
			delete(c.entries, k)
		}
	}
	if entry := c.entries[key]; entry != nil {
		c.mu.Unlock()
		<-entry.done
		return cloneResult(entry.result)
	}
	// 有界缓存；忙时允许独立请求，不驱逐在途请求。
	if len(c.entries) >= cacheLimit {
		c.mu.Unlock()
		return fetch()
	}
	entry := &cachedPrice{done: make(chan struct{})}
	c.entries[key] = entry
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if entry.result.Success && !entry.result.Degraded && entry.result.Price != nil {
			entry.expires = time.Now().Add(cacheTTL)
		} else {
			delete(c.entries, key)
		}
		close(entry.done)
		c.mu.Unlock()
	}()
	entry.result = cloneResult(fetch())
	return cloneResult(entry.result)
}

func cloneResult(result Result) Result {
	// Result 中含嵌套 map/slice，不让调用方改写其他请求的缓存。
	type snapshot struct {
		Result Result
		Raw    map[string]interface{}
	}
	data, err := json.Marshal(snapshot{Result: result, Raw: result.Raw})
	if err != nil {
		return Result{Error: "价格结果无法缓存"}
	}
	var copy snapshot
	if json.Unmarshal(data, &copy) != nil {
		return Result{Error: "价格结果无法读取"}
	}
	copy.Result.Raw = copy.Raw
	return copy.Result
}

func priceKey(options []string) string {
	sorted := append([]string(nil), options...)
	sort.Strings(sorted)
	b, _ := json.Marshal(sorted)
	return string(b)
}

// GetInternalCached 用于重复的后台/快捷下单询价，主动询价仍使用 GetInternal。
// 每次命中前均复核账户和出口闸门；账户更新会更换 client，自动隔离旧缓存。
func GetInternalCached(state *app.State, accountID, planCode, datacenter string, options []string) Result {
	client, err := state.OVH.ClientFor(accountID)
	if err != nil {
		return Result{Error: err.Error()}
	}
	status, err := state.OVH.CheckOutboundIP(accountID, false)
	if err != nil {
		return Result{Error: err.Error()}
	}
	if !status.Allowed() {
		return Result{Error: "账户出口 IP 校验未通过"}
	}
	acc, ok := state.FindAccount(accountID)
	if !ok {
		return Result{Error: "账户不存在"}
	}
	identity, _ := json.Marshal([]string{acc.ID, acc.Zone, acc.Endpoint, acc.AppKey, acc.AppSecret, acc.ConsumerKey, acc.ProxyURL, acc.ExpectedOutboundIP, planCode, datacenter, priceKey(options)})
	key := fmt.Sprintf("%p:%x", client, sha256.Sum256(identity))
	return sharedPrices.get(key, func() Result { return GetInternal(state, acc.ID, planCode, datacenter, options) })
}
