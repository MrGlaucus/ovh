package monitor

import (
	"encoding/json"

	"github.com/ovh-buy/server/internal/storage"
)

type persistShape struct {
	Subscriptions []*Subscription `json:"subscriptions"`
	KnownServers  []string        `json:"known_servers"`
	CheckInterval int             `json:"check_interval"`
}

// LoadFromFile 启动时从 subscriptions.json 加载
func (m *Monitor) LoadFromFile(path string) {
	var data persistShape
	if err := storage.ReadJSON(path, &data); err != nil {
		m.state.Logger.Warn("加载订阅文件失败: "+err.Error(), "monitor")
		return
	}
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	if data.Subscriptions != nil {
		m.subscriptions = data.Subscriptions
	}
	known := map[string]struct{}{}
	for _, k := range data.KnownServers {
		known[k] = struct{}{}
	}
	m.knownServers = known
	// 全局强制 5 秒
	m.checkInterval = 5
	m.state.Logger.Info("检查间隔已强制设置为: 5秒（全局固定值）", "monitor")
	m.state.Logger.Info("已加载订阅", "monitor")
}

// SaveToFile 保存订阅
func (m *Monitor) SaveToFile(path string) {
	m.subsMu.Lock()
	subs := make([]*Subscription, len(m.subscriptions))
	copy(subs, m.subscriptions)
	known := make([]string, 0, len(m.knownServers))
	for k := range m.knownServers {
		known = append(known, k)
	}
	m.checkInterval = 5
	m.subsMu.Unlock()
	data := persistShape{
		Subscriptions: subs,
		KnownServers:  known,
		CheckInterval: 5,
	}
	if err := storage.WriteJSON(path, data); err != nil {
		m.state.Logger.Error("保存订阅数据失败: "+err.Error(), "monitor")
	} else {
		m.state.Logger.Info("订阅数据已保存（检查间隔固定为5秒）", "monitor")
	}
}

// SubscriptionAsJSON 帮助 handler 返回订阅
func (m *Monitor) SubscriptionAsJSON(planCode string) ([]byte, bool) {
	sub := m.FindSubscription(planCode)
	if sub == nil {
		return nil, false
	}
	b, _ := json.Marshal(sub)
	return b, true
}
