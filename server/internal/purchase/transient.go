package purchase

import (
	"errors"
	"net"
	"strings"

	ovhsdk "github.com/ovh/go-ovh/ovh"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/types"
)

// IsTransient 判断一次 OVH 调用的失败是不是"过一会再试就可能成功"。
//
// 为什么要区分:队列处理器拿 Outcome.Attempted 去累加 FailureCount,
// 到 MaxRetries 就把任务置 failed。以前只要走到"向 OVH 发过请求"这一步,
// 无论失败原因一律 Attempted=true —— 包括 429。
//
// 而 429 恰恰是抢购最需要撑住的那一刻:补货瞬间所有人都在打同一个接口,
// OVH 限流是常态。MaxRetries=5、retryInterval=10s 的任务会在 **不到一分钟内**
// 被自己判死,而那一分钟正是唯一有货的窗口。用户第二天看到的是一条
// "连续 5 次下单尝试均失败"的记录,和一台被别人买走的机器。
//
// 判定为 transient 的:
//   - 429 限流
//   - 5xx(OVH 自己挂了/网关超时)
//   - 408 请求超时
//   - 传输层错误:超时、连接被重置、DNS 解析失败、EOF
//   - 出口 IP 闸门阻断(账户出口 IP 未确认):代理没挂对/出口 IP 变了,
//     是用户侧环境问题而不是"这单买不成" —— 修好代理后请求立即恢复
//
// 不算 transient 的是 4xx 业务拒绝(参数错、无权限、无货、机型不在本区目录),
// 那些重试多少次都是同一个答案,该计数就得计数。
func IsTransient(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *ovhsdk.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == 429, apiErr.Code == 408:
			return true
		case apiErr.Code >= 500 && apiErr.Code <= 599:
			return true
		case apiErr.Code > 0:
			// 明确的业务级 4xx —— 重试无益
			return false
		}
	}
	// 非 APIError:多半还没拿到 HTTP 响应
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection reset",
		"connection refused",
		"no such host",
		"i/o timeout",
		"timeout",
		"eof",
		"broken pipe",
		"tls handshake",
		"too many requests",
		"service unavailable",
		"bad gateway",
		"gateway timeout",
		// 出口 IP 闸门阻断(GuardedTransport 在请求出网前返回,没有 HTTP 响应,
		// 上面那些状态码/传输层特征全部命中不了)。判成 transient 的语义:
		// 代理/出口 IP 是用户侧环境配置,修好后重试立刻恢复;不该写抢购历史、
		// 不该计 FailureCount —— 否则代理配错一次,任务就会被 MaxRetries 判死,
		// 历史里还会留下一条与"这单买不成"毫无关系的环境错误。
		"携带账户鉴权的 ovh 请求已阻断",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// attemptOutcome 把一次失败包装成 Outcome。
// transient 的失败不计进 FailureCount —— 它没告诉我们"这单买不成",
// 只告诉我们"这一下没打通"。
func attemptOutcome(err error) Outcome {
	return Outcome{Attempted: !IsTransient(err)}
}

// failOutcome 是"这一步失败了"的统一收尾:非瞬时错误才写抢购历史。
//
// 历史条目是按 TaskID 就地覆盖的(每个任务最多一条),以前连 408/429/5xx
// 这类瞬时错误也无条件写 —— 无货轮询里偶发一次 408,就把这条任务此前
// 有价值的失败原因覆盖成一句 408 HTML;用户看到的是"型号一直无货、
// 队列没减少,历史里却全是抢购失败"。瞬时错误不构成"这单买不成"的结论,
// 只留在日志里,历史保持干净。
func failOutcome(state *app.State, item *types.QueueItem, err error, errMsg string) Outcome {
	if !IsTransient(err) {
		recordFailure(state, item, errMsg)
	}
	return attemptOutcome(err)
}
