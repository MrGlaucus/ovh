package telegram

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/types"
)

// 收取 update 只有长轮询一条路（见 poller.go），没有入站端点，
// 「伪造来源」这个问题不存在 —— 唯一要判的是「这条消息是不是配置里那个 chat 发的」，
// 剩下的就只有频率限制。webhook 时代的 secret_token 体系已随入站端点一起删除。
const (
	// MaxTelegramBodyBytes 单次读取 Telegram 响应的上限。update 远小于此。
	MaxTelegramBodyBytes = 64 * 1024
	// UpdateIDRetentionDays update_id 幂等表保留天数
	UpdateIDRetentionDays = 7
	// ButtonTTL 一键下单按钮有效期，与旧的 messageUUIDCacheTTL 保持一致
	ButtonTTL = 24 * time.Hour
	// RateLimitWindow / RateLimitMaxPerWindow 单 chat 的处理频率上限
	RateLimitWindow       = 10 * time.Second
	RateLimitMaxPerWindow = 8
)

// IsAuthorizedActor 同时验证目标 Chat ID 与设置中的 Telegram 用户白名单。
// 白名单为空时拒绝全部用户；长轮询没有伪造来源问题，这道检查是下单动作
// 唯一的身份边界，防止群成员、误发给 Bot 的私聊用户或泄漏的按钮链接触发下单。
func IsAuthorizedActor(state *app.State, chatID, userID interface{}) bool {
	return isAuthorizedActor(state.Config.Get(), chatID, userID)
}

// isAuthorizedActor 将授权规则拆成无副作用函数，便于覆盖私聊、群聊和空白名单场景。
func isAuthorizedActor(cfg types.Config, chatID, userID interface{}) bool {
	wantChat := normalizeID(cfg.TgChatID)
	allow := strings.TrimSpace(cfg.TgAllowedUserIDs)
	if wantChat == "" || allow == "" {
		return false
	}
	gotChat := normalizeID(idToString(chatID))
	gotUser := normalizeID(idToString(userID))
	if !idInCSV(gotUser, allow) {
		return false
	}

	if gotChat != "" && gotChat == wantChat {
		return true
	}
	// 兼容：配置里填的是 User ID，私聊时 chat_id 与之相等。
	return gotUser == wantChat && (gotChat == "" || gotChat == gotUser)
}

func idInCSV(id, csv string) bool {
	if id == "" {
		return false
	}
	// 走 SplitList:这串 chat ID 是用户在设置页手打的,中文输入法打出的全角逗号
	// 会让整条白名单匹配不上任何人 —— 表现是自己被锁在机器人外面,且毫无提示。
	for _, p := range types.SplitList(csv) {
		if normalizeID(p) == id {
			return true
		}
	}
	return false
}

// normalizeID 去掉 @ 前缀和小数点尾巴（JSON 数字解出来是 float64）
func normalizeID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "@")
	if i := strings.Index(s, "."); i >= 0 {
		s = s[:i]
	}
	return s
}

// idToString 把 chat_id / user_id（float64 / json.Number / string）统一成字符串
func idToString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return fmt.Sprintf("%.0f", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case int:
		return fmt.Sprintf("%d", x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// ChatIDString 导出给 handler 做频率限制 key
func ChatIDString(v interface{}) string { return normalizeID(idToString(v)) }

// --- 进程内频率限制 ---

type rateBucket struct {
	windowStart time.Time
	count       int
}

var (
	rateMu   sync.Mutex
	rateByID = map[string]*rateBucket{}
)

// AllowRate 按 chat（取不到则 user）维度限流，返回是否放行。
func AllowRate(id string) bool {
	if id == "" {
		id = "unknown"
	}
	now := time.Now()
	rateMu.Lock()
	defer rateMu.Unlock()
	b, ok := rateByID[id]
	if !ok || now.Sub(b.windowStart) > RateLimitWindow {
		rateByID[id] = &rateBucket{windowStart: now, count: 1}
		return true
	}
	if b.count >= RateLimitMaxPerWindow {
		return false
	}
	b.count++
	return true
}
