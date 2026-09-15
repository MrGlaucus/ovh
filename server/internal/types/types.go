package types

import (
	"strings"
	"time"
)

// chinaLocation 是用户可见时间的统一时区。持久化仍使用带偏移的 RFC3339，
// 但 Telegram、通知和旧的无时区数据必须显式按北京时间展示，不能依赖 Docker 的 Local。
var chinaLocation = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return loc
}()

func ChinaLocation() *time.Location { return chinaLocation }

func NowChina() time.Time { return time.Now().In(chinaLocation) }

func InChina(t time.Time) time.Time { return t.In(chinaLocation) }

type Config struct {
	AppKey      string `json:"appKey"`
	AppSecret   string `json:"appSecret"`
	ConsumerKey string `json:"consumerKey"`
	Endpoint    string `json:"endpoint"`
	TgToken     string `json:"tgToken"`
	TgChatID    string `json:"tgChatId"`
	// TgAllowedUserIDs 是允许处理消息/按钮回调的 Telegram 数字 User ID，逗号分隔；空值即拒绝全部用户。
	TgAllowedUserIDs string `json:"tgAllowedUserIds"`
	// WebhookURL 是 Telegram 回调到本服务的公网地址；保存后自动注册并启用 secret_token。
	WebhookURL string `json:"webhookUrl,omitempty"`
	IAM        string `json:"iam"`
	Zone       string `json:"zone"`

	// TgWebhookSecret Telegram setWebhook 的 secret_token。Telegram 会在每次回调里带
	// X-Telegram-Bot-Api-Secret-Token 头，用它证明请求真的来自 Telegram。
	// 首次需要时自动生成并落库；GetSettings 不会把它回给前端。
	TgWebhookSecret string `json:"tgWebhookSecret,omitempty"`
	// NotifyWebhookURL 第二条通知通道:一个接收 JSON POST 的地址(钉钉/飞书/Bark/自建都行)。
	// 补货监控的全部价值就是"有货那一刻你能收到消息",单通道意味着 Telegram 一挂就全盲。
	NotifyWebhookURL string `json:"notifyWebhookUrl,omitempty"`
	// TgWebhookSecretRegistered secret 是否已经推给 Telegram（setWebhook 成功过）。
	// false 时 webhook 处于兼容模式：不强制校验 secret，避免升级后老用户的按钮直接全挂。
	TgWebhookSecretRegistered bool `json:"tgWebhookSecretRegistered,omitempty"`

	// DefaultRetryInterval 新建抢购任务的默认重试间隔(秒)。
	// 网页弹窗、TG /buy、上架通知里的一键下单按钮不显式指定时都用它。
	// 以前四条入队路径各写各的(30 / 30 / 30,前端弹窗还显示 60),用户既改不了也对不上。
	DefaultRetryInterval int `json:"defaultRetryInterval,omitempty"`
	// QuickOrderRetryInterval 监控触发的自动下单(/watch 自动抢)用的重试间隔(秒)。
	// 单独一个值是因为场景不同:货刚出现那一刻要抢,窗口可能只有几十秒,
	// 所以默认比普通任务激进得多;但太密会吃 OVH 的 429,这里交给用户自己权衡。
	QuickOrderRetryInterval int `json:"quickOrderRetryInterval,omitempty"`
}

// 重试间隔的默认值与合法区间(秒)。
const (
	DefaultTaskRetryInterval  = 60
	DefaultQuickRetryInterval = 2
	MinRetryInterval          = 1
	MaxRetryInterval          = 86400
)

// ClampRetryInterval 把重试间隔夹到合法区间;<= 0 视为"没设",退回 fallback。
//
// 处理器、入队路径、设置保存都走这一个函数。0 必须兜住:处理器的就绪判断是
// `now - last >= interval`,间隔为 0 时恒真,任务会每秒重试一次把 OVH 刷到 429 ——
// 旧库里 retry_interval 列后加的行、任何忘了设这个字段的入队路径都会踩到。
func ClampRetryInterval(v, fallback int) int {
	if v <= 0 {
		v = fallback
	}
	if v < MinRetryInterval {
		return MinRetryInterval
	}
	if v > MaxRetryInterval {
		return MaxRetryInterval
	}
	return v
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Endpoint:                "ovh-eu",
		IAM:                     "go-ovh-ie",
		Zone:                    "IE",
		DefaultRetryInterval:    DefaultTaskRetryInterval,
		QuickOrderRetryInterval: DefaultQuickRetryInterval,
	}
}

// —— 下单规模上限 ——
//
// 这几个数原来只写在 telegram 包里,于是只有 TG 那条路受保护。
// 网页端建任务的循环是 `for dc { for i < qty { POST /queue } }`,两个上界都没有:
// 数量填 9999、选 5 个机房 = 约 5 万次串行请求,浏览器卡死、库里塞 5 万条任务,
// 而每一条都是一次真实的下单尝试。多打一个数字的代价不该是这个。
//
// 放在 types 里是为了让所有入队路径共用同一份约束 —— 之前"能力在一条路径有、
// 另一条没有"已经出过好几次问题了。
const (
	// MaxOrderQuantity 单个机房最多下几台。
	// 没有上限时 "planCode 4000000000" 会让入队方先把 40 亿个 QueueItem
	// append 进一个切片 —— 进程当场 OOM 被杀。
	MaxOrderQuantity = 20
	// MaxOrderFanout 一次操作最多创建多少个抢购任务。
	// 不指定机房时任务数 = 配置数 × 机房数 × 数量,很容易远超用户直觉。
	MaxOrderFanout = 60
	// MaxQueueItems 队列里最多存多少条任务。
	// 这是最后一道闸:不管任务从哪条路进来(网页 / 快速下单 / TG / 监控自动下单),
	// 超过这个数一律拒绝。每条任务都是一次真实下单尝试,正常用法离这个数很远。
	MaxQueueItems = 500
)

// LogEntry 日志条目（字段名与前端 JSON 结构一致）
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

// OVHAccount OVH 账户凭据。多账户场景下每条记录代表一个 OVH 账户。
type OVHAccount struct {
	ID          string `json:"id"`       // UUID
	Name        string `json:"name"`     // 用户起的名字（"主号" / "小号 A"）
	Endpoint    string `json:"endpoint"` // ovh-eu / ovh-us / ovh-ca
	Zone        string `json:"zone"`     // IE/FR/DE/US/CA/...
	AppKey      string `json:"appKey"`
	AppSecret   string `json:"appSecret"`
	ConsumerKey string `json:"consumerKey"`
	IAM         string `json:"iam"` // go-ovh-<zone-lower>
	// ProxyURL 是此账户 OVH API 专用代理。空值表示该账户显式直连；非空时请求绝不回退直连。
	ProxyURL string `json:"proxyUrl"`
	// ExpectedOutboundIP 是该账户带签名 OVH 请求允许使用的唯一 IPv4 出口地址。
	ExpectedOutboundIP  string `json:"expectedOutboundIp"`
	ActualOutboundIP    string `json:"actualOutboundIp"`
	OutboundIPStatus    string `json:"outboundIpStatus"` // pending / verified / mismatch / failed
	OutboundIPCheckedAt string `json:"outboundIpCheckedAt"`
	OutboundIPError     string `json:"outboundIpError"`
	IsDefault           bool   `json:"isDefault"` // 默认账户（未指定时 fallback 用它）
	CreatedAt           string `json:"createdAt"`
}

// QueueItem 抢购队列项
type QueueItem struct {
	ID            string   `json:"id"`
	AccountID     string   `json:"accountId"` // 该任务下单时用的 OVH 账户
	PlanCode      string   `json:"planCode"`
	Datacenter    string   `json:"datacenter"`
	Options       []string `json:"options"`
	Status        string   `json:"status"` // running / pending / paused / completed
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
	RetryInterval int      `json:"retryInterval"`
	RetryCount    int      `json:"retryCount"`
	// FailureCount 只统计"真的向 OVH 提交过并失败"的次数;无货的空轮不算。
	// MaxRetries 封顶用它而不是 RetryCount —— 抢购的常态就是绝大多数轮次都无货,
	// 拿轮次封顶会让任务在还没真正试过几次时就被判死。
	FailureCount  int     `json:"failureCount,omitempty"`
	MaxRetries    int     `json:"maxRetries,omitempty"`
	LastCheckTime float64 `json:"lastCheckTime"`
	QuickOrder    bool    `json:"quickOrder,omitempty"`
	Priority      int     `json:"priority,omitempty"`
	FromTelegram  bool    `json:"fromTelegram,omitempty"`
	// AutoPay 下单成功后让 OVH 用账户默认支付方式自动付款
	// (checkout 的 autoPayWithPreferredPaymentMethod,schema 描述:
	// "order will be automatically paid with preferred payment method")。
	// 默认 false:自动扣钱必须是用户显式打开的开关,不能是隐含行为。
	AutoPay            bool   `json:"autoPay,omitempty"`
	ConfigSniperTaskID string `json:"configSniperTaskId,omitempty"`
	// DelaySeconds 发现目标库存可下单后等待多少秒；0 = 不延迟。
	// 任务在等待期结束时会重新确认库存，再进入创建购物车与结算。
	DelaySeconds int `json:"delaySeconds,omitempty"`
	// OrderNotBefore 是发现有货后允许实际下单的 Unix 时间；落库以支持重启续等。
	OrderNotBefore float64 `json:"orderNotBefore,omitempty"`
	// DelayReady 表示延迟已到期，下一次库存确认成功后直接下单而不重复延迟。
	DelayReady bool `json:"delayReady,omitempty"`
}

// ServerFavorite 是全局关注的服务器型号，不关联账户。
// 型号在不同账户/区域是否可购买由读取时的当前目录决定。
type ServerFavorite struct {
	PlanCode    string `db:"plan_code" json:"planCode"`
	DisplayName string `db:"display_name" json:"displayName"`
	CreatedAt   string `db:"created_at" json:"createdAt"`
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
	ID             string   `json:"id"`
	AccountID      string   `json:"accountId"` // 哪个账户买的
	TaskID         string   `json:"taskId"`
	PlanCode       string   `json:"planCode"`
	Datacenter     string   `json:"datacenter"`
	Options        []string `json:"options"`
	Status         string   `json:"status"` // success / failed
	OrderID        string   `json:"orderId"`
	OrderURL       string   `json:"orderUrl"`
	ErrorMessage   *string  `json:"errorMessage"`
	PurchaseTime   string   `json:"purchaseTime"`
	AttemptCount   int      `json:"attemptCount"`
	ExpirationTime string   `json:"expirationTime,omitempty"`
	// RetractionTime 订单的撤销权截止时间（billing.Order.retractionDate）。
	// 单独开一个字段而不是塞进 ExpirationTime：retractionDate 是"多久内可无理由撤单"，
	// expirationDate 是"订单未付款何时作废"，语义不同，混用会让用户把撤销期当成付款截止期。
	RetractionTime string     `json:"retractionTime,omitempty"`
	Price          *PriceInfo `json:"price,omitempty"`
	// Timing 这一单每个阶段花了多久。抢购输了之后唯一有用的信息就是"慢在哪一步" ——
	// 是 OVH 的库存接口慢、还是自己这台机器建购物车慢、还是最后 checkout 排队了。
	Timing  []PhaseTiming `json:"timing,omitempty"`
	TotalMs int64         `json:"totalMs,omitempty"`
	// OrderStatus OVH 侧的订单状态(billing.order.OrderStatusEnum):
	// notPaid / checking / delivering / delivered / cancelled / cancelling /
	// documentsRequested / unknown。来自 GET /me/order/{orderId}/status,
	// 三区都有。"下单成功"≠"已付款",没有它用户永远不知道订单到底付了没。
	OrderStatus string `json:"orderStatus,omitempty"`
	// OrderStatusAt 上次刷新状态的时间,给节流和"这是多久以前的状态"用
	OrderStatusAt string `json:"orderStatusAt,omitempty"`
	// DelaySeconds 下单时配置的延迟秒数(订阅级/自动下单带入),0=立即下单。
	// 落库让历史页能回溯"这一单当时等了多久才开抢"。
	DelaySeconds int `json:"delaySeconds,omitempty"`
}

// PhaseTiming 抢购链路上一个阶段的墙钟耗时
type PhaseTiming struct {
	Name string `json:"name"`
	Ms   int64  `json:"ms"`
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
	Timestamp  string                 `json:"timestamp"`
	Datacenter string                 `json:"datacenter"`
	Status     string                 `json:"status"`
	ChangeType string                 `json:"changeType"`
	OldStatus  interface{}            `json:"oldStatus"`
	Config     map[string]interface{} `json:"config,omitempty"`
}

// Subscription 监控订阅（跨账户共享列表;auto-order 触发时按 AutoOrderAccountID 下单）
type Subscription struct {
	PlanCode           string                     `json:"planCode"`
	Datacenters        []string                   `json:"datacenters"`
	NotifyAvailable    bool                       `json:"notifyAvailable"`
	NotifyUnavailable  bool                       `json:"notifyUnavailable"`
	LastStatus         map[string]string          `json:"lastStatus"`
	CreatedAt          string                     `json:"createdAt"`
	History            []SubscriptionHistoryEntry `json:"history"`
	ServerName         string                     `json:"serverName,omitempty"`
	AutoOrder          bool                       `json:"autoOrder,omitempty"`
	Quantity           int                        `json:"quantity,omitempty"`
	AutoOrderAccountID string                     `json:"autoOrderAccountId,omitempty"` // 空 = 触发时只通知不下单
	// AutoPay 下单成功后用默认支付方式自动付款(显式开关,默认关)
	AutoPay bool `json:"autoPay,omitempty"`
	// DelaySeconds 补货后延迟多少秒才下单。0=立即下单。
	// 每个订阅可独立配置，适配不同型号的抢购策略。
	DelaySeconds int `json:"delaySeconds,omitempty"`
}

// VPSSubscription VPS 监控订阅
type VPSSubscription struct {
	ID                 string                   `json:"id"`
	PlanCode           string                   `json:"planCode"`
	OvhSubsidiary      string                   `json:"ovhSubsidiary"`
	Datacenters        []string                 `json:"datacenters"`
	MonitorLinux       bool                     `json:"monitorLinux"`
	MonitorWindows     bool                     `json:"monitorWindows"`
	NotifyAvailable    bool                     `json:"notifyAvailable"`
	NotifyUnavailable  bool                     `json:"notifyUnavailable"`
	LastStatus         map[string]string        `json:"lastStatus"`
	History            []map[string]interface{} `json:"history"`
	CreatedAt          string                   `json:"createdAt"`
	AutoOrderAccountID string                   `json:"autoOrderAccountId,omitempty"` // 空 = 触发时只通知不下单
	// AutoOrder 有货时是否真的下单。和 AutoOrderAccountID 分开:
	// 只填账户不代表要下单,用户可能只是想让通知里带上"用哪个账户能买"。
	AutoOrder bool `json:"autoOrder,omitempty"`
	// Quantity 每次下单几台
	Quantity int `json:"quantity,omitempty"`
	// AutoPay 下单成功后用 OVH 默认支付方式自动付款(用户显式开关)
	AutoPay bool `json:"autoPay,omitempty"`
	// OS 装什么系统。空 = 用 OVH 的默认值。
	// VPS 和独服不同:系统是下单时就要定的配置项,不是买完再装。
	OS string `json:"os,omitempty"`
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

// NowISO 返回带 UTC 时区标识的 RFC3339 时间。持久化时间必须自描述时区，
// 否则 Docker 的 UTC 时间会被浏览器按用户本地时区解释，导致历史记录错八小时。
func NowISO() string {
	return time.Now().UTC().Format(NowISOLayout)
}

// NowISOLayout 是 NowISO 的 RFC3339 布局；解析自家时间戳一律走 ParseTS。
const NowISOLayout = "2006-01-02T15:04:05.000000Z07:00"

// ParseTS 解析本项目自己写出来的时间戳。
//
// 历史上存过两种格式:无时区 NowISO 和带时区的 RFC3339；库里两种都有，
// 所以解析必须两种都认。第二个返回值为 false 表示确实解不出来，
// 调用方要显式决定"解不出来时怎么办",不要再写成静默跳过。
func ParseTS(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, NowISOLayout, "2006-01-02T15:04:05.000000", "2006-01-02T15:04:05"} {
		// 新格式自带 UTC/偏移信息；无时区历史记录统一按北京时间解释，不能受容器 Local 影响。
		if t, err := time.ParseInLocation(layout, s, chinaLocation); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
