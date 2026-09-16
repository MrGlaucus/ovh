package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/catalog"
	"github.com/ovh-buy/server/internal/purchase"
	"github.com/ovh-buy/server/internal/types"
)

// historyPaymentInFlight 防止同一张历史订单被双击或并发请求重复扣款。
// 网络超时后不能由服务端自动重试付款，必须由用户在 OVH 面板确认后决定下一步。
var historyPaymentInFlight sync.Map // history entry ID -> struct{}

// AddQueueItem POST /api/queue
// 多账户:body 必须带 account_id,后端用它确定下单走哪个账户
func AddQueueItem(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			AccountID     string   `json:"account_id"`
			PlanCode      string   `json:"planCode"`
			Datacenter    string   `json:"datacenter"`
			Options       []string `json:"options"`
			RetryInterval int      `json:"retryInterval"`
			// AutoPay 下单成功后用默认支付方式自动付款(显式开关,默认关)
			AutoPay bool `json:"autoPay"`
			// DelaySeconds 入队后延迟多少秒才开始首次检查,0=立即执行
			DelaySeconds int `json:"delaySeconds"`
		}
		_ = c.ShouldBindJSON(&body)
		if body.AccountID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "缺少 account_id"})
			return
		}
		if _, ok := state.FindAccount(body.AccountID); !ok {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "account_id 不存在"})
			return
		}
		body.PlanCode = strings.TrimSpace(body.PlanCode)
		body.Datacenter = strings.TrimSpace(body.Datacenter)
		if body.PlanCode == "" || body.Datacenter == "" {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "缺少 planCode 或 datacenter"})
			return
		}
		// 入队前挡住"这个账户根本买不到这台机器"的任务(跨区 / 非 Eco / planCode 不存在)。
		// 前端的下单对话框有独立的账户选择器(web/src/routes/servers.tsx:646),
		// 机型列表却是按另一个账户拉的 —— 拿欧区机型配美区账户是一键就能做出来的组合。
		// 而这种任务进了队列后:availabilities 返回 200 + 空数组 → PurchaseServer 判"无货"
		// → 按 retryInterval 永远重试,日志里永远只有一句"当前无货",用户看不出错在哪。
		// 所以在这里就说清楚,而不是让它在后台空转到天荒地老。
		// 探测失败(catalog.PlanVerdictUnknown)不拦 —— 一次网络瞬断不该让用户下不了单。
		if verdict, hint := catalog.ClassifyPlan(state, body.AccountID, body.PlanCode, "queue"); hint != "" {
			state.Logger.Warn(fmt.Sprintf("[queue] 拒绝任务(判定 %d): %s", verdict, hint), "queue")
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": hint})
			return
		}
		// 配置必须锁定才能入队:options 为空时下单两难 —— OVH 会按默认配置下单,
		// 而用户以为买的是页面上选的那套(实际发生过"选标准配置买成非标配置")。
		// 探测一次 FQN:存在"<planCode>.<addon...>"形式的多配置组合才拒绝;
		// 裸 planCode 机型(整个机型就是唯一配置)options 为空是正常形态。
		// 探测失败(catalog 瞬断)不拦,执行侧 PurchaseServer 有同样的兜底判定。
		if len(body.Options) == 0 {
			segmented := false
			for _, cfg := range catalog.CheckServerAvailabilityWithConfigs(state, body.PlanCode, body.AccountID) {
				if strings.Contains(cfg.FQN, ".") {
					segmented = true
					break
				}
			}
			if segmented {
				state.Logger.Warn("[queue] 拒绝未指定配置的任务: "+body.PlanCode, "queue")
				c.JSON(http.StatusBadRequest, gin.H{"status": "error",
					"error": "未指定硬件配置（options 为空）：" + body.PlanCode + " 有多套硬件组合，为避免下错配置，请选择具体配置后再创建任务"})
				return
			}
		}
		// 没给 / 给 0 = 用全局默认(设置页可改);超出区间夹回来
		body.RetryInterval = types.ClampRetryInterval(body.RetryInterval, state.Config.RetryInterval())
		item := types.QueueItem{
			ID:            uuid.NewString(),
			AccountID:     body.AccountID,
			PlanCode:      body.PlanCode,
			Datacenter:    body.Datacenter,
			Options:       body.Options,
			Status:        "running",
			CreatedAt:     types.NowISO(),
			UpdatedAt:     types.NowISO(),
			RetryInterval: body.RetryInterval,
			RetryCount:    0,
			LastCheckTime: 0,
			AutoPay:       body.AutoPay,
			DelaySeconds:  body.DelaySeconds,
		}
		// 入队 + 落库是一件事:EnqueueItems 失败会把这条从内存里撤回,
		// 不留"这次能跑但重启就丢"的半成功任务
		if err := state.EnqueueItems([]types.QueueItem{item}, false); err != nil {
			state.Logger.Error("添加任务后保存队列失败,已撤回: "+err.Error(), "queue")
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error",
				"error":  "任务没能写进数据库，已撤回：" + err.Error(),
			})
			return
		}
		state.Logger.Info("添加任务 "+item.ID+" ("+item.PlanCode+" 在 "+item.Datacenter+", 账户 "+body.AccountID+") 到队列并立即启动 (状态: running)", "")
		c.JSON(http.StatusOK, gin.H{"status": "success", "id": item.ID})
	}
}

// RemoveQueueItem DELETE /api/queue/:id
func RemoveQueueItem(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		// MarkTaskDeleted 除了打标记,还会取消这条任务正在进行的下单(如果它正跑在
		// PurchaseServer 里)。以前只打标记,处理器要到下一轮才看得见,这一轮照跑到结账。
		state.MarkTaskDeleted(id)
		state.Logger.Info("标记任务 "+id+" 为删除，正在进行的下单已取消，后台线程将停止处理", "system")

		state.QueueMu.Lock()
		var removed *types.QueueItem
		// 重新分配新 slice，避免 [:0] 与原 backing array 共享导致快照读到已被覆盖的元素
		kept := make([]types.QueueItem, 0, len(state.Queue))
		for i := range state.Queue {
			if state.Queue[i].ID == id {
				cp := state.Queue[i]
				removed = &cp
				continue
			}
			kept = append(kept, state.Queue[i])
		}
		state.Queue = kept
		state.QueueMu.Unlock()
		if err := state.SaveQueue(); err != nil {
			// 删除没落库 → 重启后这条任务会"复活"并继续抢。必须告诉用户。
			state.Logger.Error("删除任务后保存队列失败: "+err.Error(), "queue")
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error",
				"error":  "已从运行中的队列移除，但没能写进数据库，重启后这条任务会重新出现：" + err.Error(),
			})
			return
		}
		if removed != nil {
			state.Logger.Info("Removed "+removed.PlanCode+" from queue (ID: "+id+")", "system")
		}
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

// UpdateQueueInterval PUT /api/queue/:id/interval  body: { "retryInterval": 秒 }
// 改一条正在跑的任务的重试间隔。处理器每轮都读 item 上的值,所以改完下一轮就生效。
func UpdateQueueInterval(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var body struct {
			RetryInterval int `json:"retryInterval"`
		}
		if err := c.ShouldBindJSON(&body); err != nil ||
			body.RetryInterval < types.MinRetryInterval || body.RetryInterval > types.MaxRetryInterval {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error",
				"error": fmt.Sprintf("重试间隔必须是 %d ~ %d 之间的整数秒", types.MinRetryInterval, types.MaxRetryInterval)})
			return
		}
		found := false
		state.QueueMu.Lock()
		for i := range state.Queue {
			if state.Queue[i].ID == id {
				state.Queue[i].RetryInterval = body.RetryInterval
				state.Queue[i].UpdatedAt = types.NowISO()
				found = true
				break
			}
		}
		state.QueueMu.Unlock()
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "任务不存在"})
			return
		}
		if err := state.SaveQueue(); err != nil {
			state.Logger.Error("改任务间隔后保存队列失败: "+err.Error(), "queue")
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error",
				"error":  "间隔已在本次运行中改掉，但没能写进数据库，重启后会回到原值：" + err.Error(),
			})
			return
		}
		state.Logger.Info(fmt.Sprintf("任务 %s 重试间隔改为 %d 秒", id, body.RetryInterval), "queue")
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

// ClearQueue DELETE /api/queue/clear
func ClearQueue(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		state.QueueMu.Lock()
		count := len(state.Queue)
		for _, it := range state.Queue {
			state.MarkTaskDeleted(it.ID) // 同时取消正在进行的下单
		}
		state.Queue = []types.QueueItem{}
		state.QueueMu.Unlock()
		if err := state.SaveQueue(); err != nil {
			state.Logger.Error("清空队列后保存失败: "+err.Error(), "queue")
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error",
				"error":  "已清空运行中的队列，但没能写进数据库，重启后这些任务会重新出现：" + err.Error(),
			})
			return
		}
		state.Logger.Info("Cleared all queue items ("+strconv.Itoa(count)+" items removed)", "")
		c.JSON(http.StatusOK, gin.H{"status": "success", "count": count})
	}
}

// UpdateAllQueueStatuses PUT /api/queue/batch-status
// action=pause 暂停所有可执行任务；action=resume 恢复所有暂停任务。
func UpdateAllQueueStatuses(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Action string `json:"action"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || (body.Action != "pause" && body.Action != "resume") {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "action 必须是 pause 或 resume"})
			return
		}

		now := time.Now()
		updated := 0
		state.QueueMu.Lock()
		for i := range state.Queue {
			item := &state.Queue[i]
			if body.Action == "pause" {
				// completed / failed 是终态；其余可执行状态全部暂停。
				if item.Status != "running" && item.Status != "pending" && item.Status != "delaying" {
					continue
				}
				item.Status = "paused"
			} else {
				if item.Status != "paused" {
					continue
				}
				// 延迟中的任务暂停后仍保留原到期时间：未到点继续等待，
				// 已到点则下一轮先重新确认库存，不重新开始完整延迟。
				if item.OrderNotBefore > 0 {
					if float64(now.Unix()) < item.OrderNotBefore {
						item.Status = "delaying"
					} else {
						item.Status = "running"
						item.DelayReady = true
					}
				} else {
					item.Status = "running"
				}
			}
			item.UpdatedAt = types.NowISO()
			updated++
		}
		state.QueueMu.Unlock()

		if err := state.SaveQueue(); err != nil {
			state.Logger.Error("批量更新队列状态后保存失败: "+err.Error(), "queue")
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "队列状态已在本次运行中更新，但没能写入数据库：" + err.Error()})
			return
		}
		state.Logger.Info(fmt.Sprintf("批量%s队列任务: %d 条", map[string]string{"pause": "暂停", "resume": "恢复"}[body.Action], updated), "queue")
		c.JSON(http.StatusOK, gin.H{"status": "success", "updated": updated})
	}
}

// UpdateQueueStatus PUT /api/queue/:id/status
func UpdateQueueStatus(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var body struct {
			Status string `json:"status"`
		}
		_ = c.ShouldBindJSON(&body)
		if body.Status == "" {
			body.Status = "pending"
		}
		state.QueueMu.Lock()
		for i := range state.Queue {
			if state.Queue[i].ID == id {
				status := body.Status
				// 暂停有货后的延迟任务再恢复时，保留原到期时间：未到点继续等待，
				// 已到点则下一轮直接做二次库存确认，不重新开始完整延迟。
				if status == "running" && state.Queue[i].OrderNotBefore > 0 {
					if float64(time.Now().Unix()) < state.Queue[i].OrderNotBefore {
						status = "delaying"
					} else {
						state.Queue[i].DelayReady = true
					}
				}
				state.Queue[i].Status = status
				state.Queue[i].UpdatedAt = types.NowISO()
				state.Logger.Info("Updated "+state.Queue[i].PlanCode+" status to "+status, "")
				break
			}
		}
		state.QueueMu.Unlock()
		if err := state.SaveQueue(); err != nil {
			state.Logger.Error("改任务状态后保存队列失败: "+err.Error(), "queue")
			c.JSON(http.StatusInternalServerError, gin.H{
				"status": "error",
				"error":  "状态已在本次运行中改掉，但没能写进数据库，重启后会回到原状态：" + err.Error(),
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

// RefreshOrderStatuses POST /api/purchase-history/refresh-status
//
// 手动刷新所有未到终态订单的支付状态(GET /me/order/{id}/status),
// 并给还没标记退款的成功单查退款记录(GET /me/refund?orderId=)。
// 订单状态后台每 10 分钟也会自动刷;退款查询只在手动刷新时执行,后台不轮询 ——
// 这里是给"我刚付完款/刚取消完想马上看到"的场景。
func RefreshOrderStatuses(state *app.State) gin.HandlerFunc {
	// 手动刷新会对每条未终态订单各打一次 /me/order/{id},而 force=true 正是用来
	// 跳过那个 2 分钟节流的 —— 等于把限流闸门交给用户的手速。
	// OVH 对 /me 命名空间有自己的限流,打多了返回 429,而抢购主链路
	// (查库存 / 建车 / 结账)跟它共用同一个账户配额:刷历史把配额刷没了,
	// 补货那一刻就抢不到。所以入口这层必须有自己的节流。
	var (
		mu       sync.Mutex
		lastCall time.Time
	)
	const minInterval = 15 * time.Second

	return func(c *gin.Context) {
		mu.Lock()
		if wait := minInterval - time.Since(lastCall); !lastCall.IsZero() && wait > 0 {
			mu.Unlock()
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error": fmt.Sprintf("刷新太频繁,请等 %d 秒。手动刷新会对每条未完成订单各查一次 OVH,"+
					"把账户配额刷光会影响正在跑的抢购。", int(wait.Seconds())+1),
				"retryAfterSeconds": int(wait.Seconds()) + 1,
			})
			return
		}
		lastCall = time.Now()
		mu.Unlock()

		n := purchase.RefreshOrderStatuses(state, true)
		// 顺带查退款：手动刷新正是"我刚在面板取消完订单,想立刻知道退了没"的场景,
		// 退款查询跳过按小时节流(见 RefreshRefundStatuses 的 force 语义)。
		n += purchase.RefreshRefundStatuses(state, true)
		c.JSON(http.StatusOK, gin.H{"success": true, "updated": n})
	}
}

// PayPurchaseHistoryOrder POST /api/purchase-history/:id/pay
// 仅允许对历史中原账户的待付款订单使用该账户默认支付方式付款。
func PayPurchaseHistoryOrder(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var body struct {
			OrderID string `json:"orderId"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
			return
		}

		state.HistoryMu.Lock()
		var entry types.PurchaseHistoryEntry
		found := false
		for _, item := range state.History {
			if item.ID == id {
				entry = item
				found = true
				break
			}
		}
		state.HistoryMu.Unlock()
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "抢购历史记录不存在"})
			return
		}
		if entry.Status != "success" || entry.OrderID == "" || entry.AccountID == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "这条记录不具备付款所需的订单或账户信息"})
			return
		}
		if strings.TrimSpace(body.OrderID) == "" || body.OrderID != entry.OrderID {
			c.JSON(http.StatusConflict, gin.H{"error": "订单确认信息不匹配，请刷新页面后重试"})
			return
		}
		deadline := time.Time{}
		if entry.ExpirationTime != "" {
			deadline, _ = types.ParseTS(entry.ExpirationTime)
		} else if purchasedAt, ok := types.ParseTS(entry.PurchaseTime); ok {
			deadline = purchasedAt.Add(15 * 24 * time.Hour)
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			c.JSON(http.StatusConflict, gin.H{"error": "订单付款期限已过，请到 OVH 管理面板确认订单状态"})
			return
		}
		if _, loaded := historyPaymentInFlight.LoadOrStore(id, struct{}{}); loaded {
			c.JSON(http.StatusConflict, gin.H{"error": "该订单正在请求付款，请勿重复提交"})
			return
		}
		defer historyPaymentInFlight.Delete(id)

		// 必须按历史记录里的明确账户取 Client，绝不回退默认账户，确保走该账户代理与出口 IP 闸门。
		client, err := state.OVH.ClientFor(entry.AccountID)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "下单账户已不存在或不可用，无法付款"})
			return
		}
		status, err := purchase.FetchOrderStatus(client, entry.OrderID)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "付款前查询 OVH 订单状态失败：" + err.Error()})
			return
		}
		if status != "notPaid" {
			purchase.ApplyOrderStatus(state, entry.ID, status)
			_ = state.SaveHistory()
			c.JSON(http.StatusConflict, gin.H{"error": "订单当前状态为 " + status + "，不能重复付款", "orderStatus": status})
			return
		}
		if err := purchase.PayOrderWithPreferredPaymentMethod(client, entry.OrderID); err != nil {
			state.Logger.Error("历史订单默认付款请求失败 "+entry.OrderID+": "+err.Error(), "purchase")
			// 这两类都是付款请求根本没发出（账户/订单侧校验就失败），零风险：
			// 给明确指引即可，别套"未确认完成、勿重复点击"的警告 ——
			// 那是给"可能已扣款"场景用的。
			if errors.Is(err, purchase.ErrNoUsablePaymentMean) || errors.Is(err, purchase.ErrOrderNotPayableOnline) {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
			// OVH 在订单侧确定性拒绝该支付方式（403 "This order can't be paid with …"）：
			// 同样没有资金动作，属于零风险，提示去面板处理。
			if purchase.IsOrderPaymentRefused(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "OVH 拒绝了本次付款（订单不支持该支付方式），请到 OVH 管理面板完成付款：" + err.Error()})
				return
			}
			// 不能自动重试：超时或网络断开时，OVH 可能已经收到付款请求。
			c.JSON(http.StatusBadGateway, gin.H{"error": "OVH 付款请求未确认完成：" + err.Error() + "。请先到 OVH 管理面板确认，勿重复点击。"})
			return
		}
		status, err = purchase.FetchOrderStatus(client, entry.OrderID)
		if err == nil {
			purchase.ApplyOrderStatus(state, entry.ID, status)
			_ = state.SaveHistory()
		}
		state.Logger.Info("已请求使用默认支付方式付款，订单 "+entry.OrderID+"（账户 "+entry.AccountID+"）", "purchase")
		c.JSON(http.StatusOK, gin.H{"success": true, "orderStatus": status, "message": "已请求使用该账户的默认支付方式付款，请稍后刷新订单状态确认结果"})
	}
}

// RemovePurchaseHistoryItem DELETE /api/purchase-history/:id
func RemovePurchaseHistoryItem(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		state.HistoryMu.Lock()
		kept := make([]types.PurchaseHistoryEntry, 0, len(state.History))
		found := false
		for _, entry := range state.History {
			if entry.ID == id {
				found = true
				continue
			}
			kept = append(kept, entry)
		}
		if found {
			state.History = kept
		}
		state.HistoryMu.Unlock()
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "error": "抢购历史记录不存在"})
			return
		}
		if err := state.SaveHistory(); err != nil {
			state.Logger.Error("删除抢购历史后保存失败: "+err.Error(), "history")
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "记录已从当前列表移除，但没能写入数据库：" + err.Error()})
			return
		}
		state.Logger.Info("已删除抢购历史记录: "+id, "history")
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

// ClearPurchaseHistory DELETE /api/purchase-history
func ClearPurchaseHistory(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		state.HistoryMu.Lock()
		state.History = state.History[:0]
		state.HistoryMu.Unlock()
		_ = state.SaveHistory()
		state.Logger.Info("Purchase history cleared", "")
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

// stripFailedHistory 剔除失败记录，返回保留列表和被删条数。
// 抽成纯函数是为了单测：handler 本身要一整个 State（库、日志）才能跑。
func stripFailedHistory(entries []types.PurchaseHistoryEntry) ([]types.PurchaseHistoryEntry, int) {
	kept := make([]types.PurchaseHistoryEntry, 0, len(entries))
	deleted := 0
	for _, entry := range entries {
		if entry.Status == "failed" {
			deleted++
			continue
		}
		kept = append(kept, entry)
	}
	return kept, deleted
}

// ClearFailedPurchaseHistory DELETE /api/purchase-history/failed
//
// 只删 failed 记录，成功单（尤其还没付款的）一律保留 —— 清空按钮一键全清，
// 用户想清掉刷屏的失败记录又怕误伤待付款订单时没有别的选择。
func ClearFailedPurchaseHistory(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		state.HistoryMu.Lock()
		kept, deleted := stripFailedHistory(state.History)
		state.History = kept
		state.HistoryMu.Unlock()
		if deleted == 0 {
			c.JSON(http.StatusOK, gin.H{"status": "success", "deleted": 0})
			return
		}
		if err := state.SaveHistory(); err != nil {
			state.Logger.Error("清除失败抢购历史后保存失败: "+err.Error(), "history")
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "失败记录已从当前列表移除，但没能写入数据库：" + err.Error()})
			return
		}
		state.Logger.Info(fmt.Sprintf("已清除 %d 条失败抢购历史", deleted), "history")
		c.JSON(http.StatusOK, gin.H{"status": "success", "deleted": deleted})
	}
}
