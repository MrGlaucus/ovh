package types

import "time"

// Config 对应 Python 全局 config dict
type Config struct {
	AppKey      string `json:"appKey"`
	AppSecret   string `json:"appSecret"`
	ConsumerKey string `json:"consumerKey"`
	Endpoint    string `json:"endpoint"`
	TgToken     string `json:"tgToken"`
	TgChatID    string `json:"tgChatId"`
	IAM         string `json:"iam"`
	Zone        string `json:"zone"`
}

// DefaultConfig 与 Python 端默认值保持一致
func DefaultConfig() Config {
	return Config{
		Endpoint: "ovh-eu",
		IAM:      "go-ovh-ie",
		Zone:     "IE",
	}
}

// LogEntry 日志条目，字段名严格匹配 Python 端 JSON 结构
type LogEntry struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Source    string `json:"source"`
}

// Stats 对应 /api/stats 响应
type Stats struct {
	ActiveQueues          int  `json:"activeQueues"`
	TotalServers          int  `json:"totalServers"`
	AvailableServers      int  `json:"availableServers"`
	PurchaseSuccess       int  `json:"purchaseSuccess"`
	PurchaseFailed        int  `json:"purchaseFailed"`
	QueueProcessorRunning bool `json:"queueProcessorRunning"`
	MonitorRunning        bool `json:"monitorRunning"`
}

// QueueItem 抢购队列项
type QueueItem struct {
	ID                  string   `json:"id"`
	PlanCode            string   `json:"planCode"`
	Datacenter          string   `json:"datacenter"`
	Options             []string `json:"options"`
	Status              string   `json:"status"` // running / pending / paused / completed
	CreatedAt           string   `json:"createdAt"`
	UpdatedAt           string   `json:"updatedAt"`
	RetryInterval       int      `json:"retryInterval"`
	RetryCount          int      `json:"retryCount"`
	MaxRetries          int      `json:"maxRetries,omitempty"`
	LastCheckTime       float64  `json:"lastCheckTime"`
	QuickOrder          bool     `json:"quickOrder,omitempty"`
	Priority            int      `json:"priority,omitempty"`
	FromTelegram        bool     `json:"fromTelegram,omitempty"`
	ConfigSniperTaskID  string   `json:"configSniperTaskId,omitempty"`
}

// PriceInfo 价格信息
type PriceInfo struct {
	WithTax      *float64 `json:"withTax"`
	WithoutTax   *float64 `json:"withoutTax"`
	Tax          *float64 `json:"tax"`
	CurrencyCode string   `json:"currencyCode"`
}

// PurchaseHistoryEntry 抢购历史
type PurchaseHistoryEntry struct {
	ID             string     `json:"id"`
	TaskID         string     `json:"taskId"`
	PlanCode       string     `json:"planCode"`
	Datacenter     string     `json:"datacenter"`
	Options        []string   `json:"options"`
	Status         string     `json:"status"` // success / failed
	OrderID        string     `json:"orderId"`
	OrderURL       string     `json:"orderUrl"`
	ErrorMessage   *string    `json:"errorMessage"`
	PurchaseTime   string     `json:"purchaseTime"`
	AttemptCount   int        `json:"attemptCount"`
	ExpirationTime string     `json:"expirationTime,omitempty"`
	Price          *PriceInfo `json:"price,omitempty"`
}

// Datacenter 服务器目录中单个机房可用性
type Datacenter struct {
	Datacenter   string `json:"datacenter"`
	Availability string `json:"availability"`
	DCName       string `json:"dcName,omitempty"`
	Region       string `json:"region,omitempty"`
}

// ServerOption 选项标签
type ServerOption struct {
	Label     string `json:"label"`
	Value     string `json:"value"`
	Family    string `json:"family,omitempty"`
	IsDefault bool   `json:"isDefault,omitempty"`
}

// ServerPlan 服务器目录项
type ServerPlan struct {
	PlanCode         string         `json:"planCode"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	CPU              string         `json:"cpu"`
	Memory           string         `json:"memory"`
	Storage          string         `json:"storage"`
	Bandwidth        string         `json:"bandwidth"`
	VrackBandwidth   string         `json:"vrackBandwidth"`
	Datacenters      []Datacenter   `json:"datacenters"`
	DefaultOptions   []ServerOption `json:"defaultOptions"`
	AvailableOptions []ServerOption `json:"availableOptions"`
}

// SubscriptionHistoryEntry 监控订阅的历史记录条目
type SubscriptionHistoryEntry struct {
	Timestamp   string                 `json:"timestamp"`
	Datacenter  string                 `json:"datacenter"`
	Status      string                 `json:"status"`
	ChangeType  string                 `json:"changeType"`
	OldStatus   interface{}            `json:"oldStatus"`
	Config      map[string]interface{} `json:"config,omitempty"`
}

// Subscription 监控订阅
type Subscription struct {
	PlanCode          string                     `json:"planCode"`
	Datacenters       []string                   `json:"datacenters"`
	NotifyAvailable   bool                       `json:"notifyAvailable"`
	NotifyUnavailable bool                       `json:"notifyUnavailable"`
	LastStatus        map[string]string          `json:"lastStatus"`
	CreatedAt         string                     `json:"createdAt"`
	History           []SubscriptionHistoryEntry `json:"history"`
	ServerName        string                     `json:"serverName,omitempty"`
	AutoOrder         bool                       `json:"autoOrder,omitempty"`
	Quantity          int                        `json:"quantity,omitempty"`
}

// ConfigSniperTask 配置绑定狙击任务
type ConfigSniperTask struct {
	ID              string                 `json:"id"`
	API1PlanCode    string                 `json:"api1_planCode"`
	BoundConfig     map[string]interface{} `json:"bound_config"`
	MatchStatus     string                 `json:"match_status"` // matched / pending_match / completed
	MatchedAPI2     []string               `json:"matched_api2"`
	KnownPlanCodes  []string               `json:"known_plancodes"`
	Enabled         bool                   `json:"enabled"`
	LastCheck       *string                `json:"last_check"`
	CreatedAt       string                 `json:"created_at"`
}

// VPSSubscription VPS 监控订阅
type VPSSubscription struct {
	ID                string                 `json:"id"`
	PlanCode          string                 `json:"planCode"`
	OvhSubsidiary     string                 `json:"ovhSubsidiary"`
	Datacenters       []string               `json:"datacenters"`
	MonitorLinux      bool                   `json:"monitorLinux"`
	MonitorWindows    bool                   `json:"monitorWindows"`
	NotifyAvailable   bool                   `json:"notifyAvailable"`
	NotifyUnavailable bool                   `json:"notifyUnavailable"`
	LastStatus        map[string]string      `json:"lastStatus"`
	History           []map[string]interface{} `json:"history"`
	CreatedAt         string                 `json:"createdAt"`
}

// CacheInfo 服务器列表缓存信息
type CacheInfo struct {
	Cached             bool     `json:"cached"`
	UsingExpiredCache  bool     `json:"usingExpiredCache"`
	CacheAgeMinutes    int      `json:"cacheAgeMinutes"`
	Timestamp          *float64 `json:"timestamp"`
	CacheAge           *int     `json:"cacheAge"`
	CacheDuration      int      `json:"cacheDuration"`
	NextAutoRefresh    *float64 `json:"nextAutoRefresh"`
	AutoRefreshEnabled bool     `json:"autoRefreshEnabled"`
}

// NowISO 返回 ISO8601 时间（与 datetime.now().isoformat() 一致）
func NowISO() string {
	return time.Now().Format("2006-01-02T15:04:05.000000")
}
