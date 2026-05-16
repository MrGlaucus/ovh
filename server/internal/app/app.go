package app

import (
	"sync"
	"time"

	"github.com/ovh-buy/server/internal/config"
	"github.com/ovh-buy/server/internal/logger"
	"github.com/ovh-buy/server/internal/ovh"
	"github.com/ovh-buy/server/internal/storage"
	"github.com/ovh-buy/server/internal/types"
)

// ServerListCache 服务器列表内存缓存（对应 Python server_list_cache）
type ServerListCache struct {
	mu        sync.RWMutex
	Data      []types.ServerPlan
	Timestamp *time.Time
	TTL       time.Duration
}

// NewServerListCache 默认 2 小时 TTL
func NewServerListCache() *ServerListCache {
	return &ServerListCache{TTL: 2 * time.Hour}
}

// Get 返回缓存副本和是否有效
func (s *ServerListCache) Get() ([]types.ServerPlan, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.Timestamp == nil {
		return nil, false
	}
	valid := time.Since(*s.Timestamp) < s.TTL
	cp := make([]types.ServerPlan, len(s.Data))
	copy(cp, s.Data)
	return cp, valid
}

// Set 更新缓存
func (s *ServerListCache) Set(data []types.ServerPlan) {
	s.mu.Lock()
	now := time.Now()
	s.Data = data
	s.Timestamp = &now
	s.mu.Unlock()
}

// State 聚合所有共享运行状态（取代 Python 模块级全局变量）
type State struct {
	Paths       storage.Paths
	Config      *config.Store
	OVH         *ovh.Factory
	Logger      *logger.Logger
	ServerCache *ServerListCache

	// 启动时配置（来自 .env / 环境变量）
	APIKey string // 用于内部 HTTP 回调时的 X-API-Key
	Port   string // 本地监听端口（默认 19998），用于监控器回调自身

	// 抢购队列
	QueueMu sync.Mutex
	Queue   []types.QueueItem

	// 抢购历史
	HistoryMu sync.Mutex
	History   []types.PurchaseHistoryEntry

	// 服务器目录（最近一次刷新结果）
	ServerPlansMu sync.RWMutex
	ServerPlans   []types.ServerPlan

	// 删除任务标记
	DeletedTaskIDsMu sync.Mutex
	DeletedTaskIDs   map[string]struct{}

	// 配置绑定狙击任务
	ConfigSniperMu    sync.Mutex
	ConfigSniperTasks []types.ConfigSniperTask

	// VPS 订阅
	VPSSubsMu        sync.Mutex
	VPSSubscriptions []types.VPSSubscription
	VPSCheckInterval int

	// 监控运行状态（监控线程会在后续批次填充）
	MonitorRunning bool

	// 队列处理器是否在运行（守护线程，主程序里始终 true）
	QueueProcessorRunning bool
}

// NewState 构造应用状态
func NewState(paths storage.Paths, cfg *config.Store, lg *logger.Logger) *State {
	return &State{
		Paths:                 paths,
		Config:                cfg,
		Logger:                lg,
		OVH:                   ovh.NewFactory(cfg),
		ServerCache:           NewServerListCache(),
		DeletedTaskIDs:        make(map[string]struct{}),
		Queue:                 []types.QueueItem{},
		History:               []types.PurchaseHistoryEntry{},
		ServerPlans:           []types.ServerPlan{},
		ConfigSniperTasks:     []types.ConfigSniperTask{},
		VPSSubscriptions:      []types.VPSSubscription{},
		VPSCheckInterval:      60,
		QueueProcessorRunning: true,
	}
}

// LoadAll 启动时从文件加载全部持久化数据
// 与 Python 端语义一致：列表字段在任何情况下都不能是 nil（保证 JSON 序列化为 [] 而不是 null）
func (s *State) LoadAll() {
	// queue
	_ = storage.ReadJSON(s.Paths.File("queue.json"), &s.Queue)
	if s.Queue == nil {
		s.Queue = []types.QueueItem{}
	}
	// history
	_ = storage.ReadJSON(s.Paths.File("history.json"), &s.History)
	if s.History == nil {
		s.History = []types.PurchaseHistoryEntry{}
	}
	// servers
	if err := storage.ReadJSON(s.Paths.File("servers.json"), &s.ServerPlans); err == nil && len(s.ServerPlans) > 0 {
		s.ServerCache.Set(s.ServerPlans)
		s.Logger.Info("已从文件加载 servers.json 并同步到缓存", "system")
	}
	if s.ServerPlans == nil {
		s.ServerPlans = []types.ServerPlan{}
	}
	// config sniper
	_ = storage.ReadJSON(s.Paths.File("config_sniper_tasks.json"), &s.ConfigSniperTasks)
	if s.ConfigSniperTasks == nil {
		s.ConfigSniperTasks = []types.ConfigSniperTask{}
	}
	// vps subscriptions (兼容 Python 的存储格式)
	var vpsBundle struct {
		Subscriptions []types.VPSSubscription `json:"subscriptions"`
		CheckInterval int                     `json:"check_interval"`
	}
	if err := storage.ReadJSON(s.Paths.File("vps_subscriptions.json"), &vpsBundle); err == nil {
		if vpsBundle.Subscriptions != nil {
			s.VPSSubscriptions = vpsBundle.Subscriptions
		}
		if vpsBundle.CheckInterval > 0 {
			s.VPSCheckInterval = vpsBundle.CheckInterval
		}
	}
	if s.VPSSubscriptions == nil {
		s.VPSSubscriptions = []types.VPSSubscription{}
	}
}

// CountActiveQueues 统计未完成的队列项
func (s *State) CountActiveQueues() int {
	s.QueueMu.Lock()
	defer s.QueueMu.Unlock()
	cnt := 0
	for _, it := range s.Queue {
		if it.Status == "running" || it.Status == "pending" || it.Status == "paused" {
			cnt++
		}
	}
	return cnt
}

// CountAvailableServers 统计有库存的型号
func (s *State) CountAvailableServers() int {
	s.ServerPlansMu.RLock()
	defer s.ServerPlansMu.RUnlock()
	cnt := 0
	for _, p := range s.ServerPlans {
		for _, dc := range p.Datacenters {
			if dc.Availability != "unavailable" && dc.Availability != "unknown" {
				cnt++
				break
			}
		}
	}
	return cnt
}

// CountPurchase 统计成功/失败订单数
func (s *State) CountPurchase() (success, failed int) {
	s.HistoryMu.Lock()
	defer s.HistoryMu.Unlock()
	for _, h := range s.History {
		switch h.Status {
		case "success":
			success++
		case "failed":
			failed++
		}
	}
	return
}

// SaveQueue/SaveHistory/SaveServers 落盘
func (s *State) SaveQueue() error {
	s.QueueMu.Lock()
	cp := make([]types.QueueItem, len(s.Queue))
	copy(cp, s.Queue)
	s.QueueMu.Unlock()
	return storage.WriteJSON(s.Paths.File("queue.json"), cp)
}

func (s *State) SaveHistory() error {
	s.HistoryMu.Lock()
	cp := make([]types.PurchaseHistoryEntry, len(s.History))
	copy(cp, s.History)
	s.HistoryMu.Unlock()
	return storage.WriteJSON(s.Paths.File("history.json"), cp)
}

func (s *State) SaveServers() error {
	s.ServerPlansMu.RLock()
	cp := make([]types.ServerPlan, len(s.ServerPlans))
	copy(cp, s.ServerPlans)
	s.ServerPlansMu.RUnlock()
	return storage.WriteJSON(s.Paths.File("servers.json"), cp)
}

// SaveAll 一次性保存所有数据
func (s *State) SaveAll() {
	if err := s.SaveQueue(); err != nil {
		s.Logger.Error("save queue: "+err.Error(), "system")
	}
	if err := s.SaveHistory(); err != nil {
		s.Logger.Error("save history: "+err.Error(), "system")
	}
	if err := s.SaveServers(); err != nil {
		s.Logger.Error("save servers: "+err.Error(), "system")
	}
}
