package handlers

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/monitor"
	"github.com/ovh-buy/server/internal/telegram"
	"github.com/ovh-buy/server/internal/types"
)

// Telegram 命令。
//
// 以前这个 bot 只认一种输入:恰好符合下单格式的那一行文本。别的一律
// Debug 一行日志然后静默丢掉 —— 用户发 "hi"、发 "?"、发错格式,
// 屏幕上什么都不会发生,他没有任何办法知道自己该发什么。
// 一个能花钱下单的 bot,连 /help 都没有是说不过去的。
//
// 这里的命令都只读或只做"撤销"(取消任务),不新增花钱的动作 ——
// 花钱的入口仍然只有两个:上架通知里的一键下单按钮,和下单格式的文本。

// tgMaxReplyLen Telegram 单条消息上限 4096 字符,留点余量。
const tgMaxReplyLen = 3800

// tgMaxListItems 列表类命令最多列几条,超了给个总数。
const tgMaxListItems = 15

// handleCommand 处理 / 开头的命令。返回 false 表示这不是命令,交给下单解析。
func handleCommand(state *app.State, mon *monitor.Monitor, chatID interface{}, messageID int64, text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	// Telegram 群里命令会带 @botname 后缀
	fields := strings.Fields(text)
	cmd := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if i := strings.Index(cmd, "@"); i >= 0 {
		cmd = cmd[:i]
	}
	args := fields[1:]

	var reply string
	switch cmd {
	case "start", "help", "h", "?":
		reply = helpText(state)
	case "status", "s":
		reply = statusText(state, mon)
	case "queue", "q":
		reply = queueText(state)
	case "cancel":
		reply = cancelText(state, args)
	case "watch", "w":
		reply = watchText(state, mon, args)
	case "unwatch", "uw":
		reply = unwatchText(state, mon, args)
	case "accounts", "acc":
		reply = accountsText(state)
	case "subs", "sub":
		reply = subsText(mon)
	case "recent", "history":
		reply = recentText(state)
	default:
		reply = "❓ 不认识的命令: /" + cmd + "\n\n发 /help 看能用什么。"
	}
	telegram.SendReply(state, chatID, clampReply(reply), messageID)
	return true
}

func clampReply(s string) string {
	if len(s) <= tgMaxReplyLen {
		return s
	}
	return s[:tgMaxReplyLen] + "\n…(内容过长已截断，完整信息请看控制台)"
}

func helpText(state *app.State) string {
	var b strings.Builder
	b.WriteString("🤖 OVH 抢购助手\n\n")
	b.WriteString("【下单】直接发一行文本：\n")
	b.WriteString("  <型号> [机房] [数量] [配置,逗号分隔]\n\n")
	b.WriteString("例子：\n")
	b.WriteString("  24sk602            → 所有有货机房各 1 台\n")
	b.WriteString("  24sk602 gra        → 只买 gra\n")
	b.WriteString("  24sk602 gra 2      → gra 买 2 台\n")
	b.WriteString("  24sk602 gra 2 ram-64g,softraid-2x480ssd\n\n")
	b.WriteString(fmt.Sprintf("数量上限 %d 台/次，一条消息最多创建 %d 个任务。\n",
		telegram.MaxOrderQuantity, telegram.MaxOrderFanout))
	b.WriteString("机房代码是 3-4 位小写字母（gra / rbx / sbg / bhs / waw…）。\n\n")
	b.WriteString("⚠️ 上面这种是「现在就买」，机器当下没货会直接失败。\n")
	b.WriteString("   想等补货请用 /watch。\n\n")
	b.WriteString("【盯补货】机器现在没货时用这个：\n")
	b.WriteString("  /watch 24sk602         补货就通知我\n")
	b.WriteString("  /watch 24sk602 gra x1  gra 补货就自动抢 1 台\n")
	b.WriteString("  /unwatch 24sk602       不盯了\n\n")
	b.WriteString("【命令】\n")
	b.WriteString("  /status   监控与队列总览\n")
	b.WriteString("  /queue    正在抢的任务\n")
	b.WriteString("  /cancel <任务号|all>  取消任务\n")
	b.WriteString("  /subs     在盯哪些型号\n")
	b.WriteString("  /accounts 可用的 OVH 账户\n")
	b.WriteString("  /recent   最近的抢购结果\n\n")
	b.WriteString("💡 上架通知里的按钮可以直接下单，比打字快。\n")

	cfg := state.Config.Get()
	if cfg.IsPollingMode() {
		b.WriteString("\n当前收取方式：长轮询（不需要公网地址）")
	}
	return b.String()
}

func statusText(state *app.State, mon *monitor.Monitor) string {
	var b strings.Builder
	b.WriteString("📊 当前状态\n\n")

	if mon != nil {
		subs := mon.Snapshot()
		running := "已停止"
		if st, ok := mon.Status()["running"].(bool); ok && st {
			running = "运行中"
		}
		b.WriteString(fmt.Sprintf("监控：%s，%d 个订阅\n", running, len(subs)))
		// 查不到库存的订阅要单独说 —— 那是监控已经失效但看上去一切正常的状态
		bad := 0
		for _, s := range subs {
			if s.LastCheckError != "" {
				bad++
			}
		}
		if bad > 0 {
			b.WriteString(fmt.Sprintf("⚠️ 其中 %d 个订阅最近一次检查失败，发 /subs 看详情\n", bad))
		}
	}

	pending, running, done, failed := queueCounts(state)
	b.WriteString(fmt.Sprintf("队列：%d 进行中，%d 等待，%d 成功，%d 失败\n", running, pending, done, failed))

	ok, fail := state.CountPurchase()
	b.WriteString(fmt.Sprintf("历史：抢到 %d 单，失败 %d 次\n", ok, fail))

	// 未付款的单子最要紧 —— 逾期会自动作废,机器白抢
	if n, earliest := unpaidOrders(state); n > 0 {
		b.WriteString(fmt.Sprintf("\n💳 有 %d 单还没付款", n))
		if earliest != "" {
			b.WriteString("（最早一单下单于 " + earliest + "）")
		}
		b.WriteString("\n逾期未付会自动作废，尽快去 OVH 控制面板付款。\n")
	}

	if fails := state.LoadFailures(); len(fails) > 0 {
		names := make([]string, 0, len(fails))
		for k := range fails {
			names = append(names, k)
		}
		sort.Strings(names)
		b.WriteString("\n🚨 启动时这些数据没读出来，本次运行不会写它们：" + strings.Join(names, "、") + "\n")
	}
	return b.String()
}

func queueCounts(state *app.State) (pending, running, done, failed int) {
	state.QueueMu.Lock()
	defer state.QueueMu.Unlock()
	for _, it := range state.Queue {
		switch it.Status {
		case "running":
			running++
		case "pending", "paused":
			pending++
		case "completed", "success":
			done++
		case "failed":
			failed++
		}
	}
	return
}

// unpaidOrders 数一数成功但还没付款的单。
func unpaidOrders(state *app.State) (int, string) {
	state.HistoryMu.Lock()
	defer state.HistoryMu.Unlock()
	n := 0
	earliest := ""
	for _, h := range state.History {
		if h.Status != "success" || h.OrderID == "" {
			continue
		}
		// delivered / cancelled 是终态,不用再催
		if h.OrderStatus == "delivered" || h.OrderStatus == "cancelled" {
			continue
		}
		n++
		if t, ok := types.ParseTS(h.PurchaseTime); ok {
			s := t.Format("01-02 15:04")
			if earliest == "" || s < earliest {
				earliest = s
			}
		}
	}
	return n, earliest
}

func queueText(state *app.State) string {
	state.QueueMu.Lock()
	items := make([]types.QueueItem, 0, len(state.Queue))
	for _, it := range state.Queue {
		if it.Status == "running" || it.Status == "pending" || it.Status == "paused" {
			items = append(items, it)
		}
	}
	state.QueueMu.Unlock()

	if len(items) == 0 {
		return "📭 当前没有正在抢的任务。\n\n发 /help 看怎么下单。"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🛒 正在抢的任务（%d 个）\n\n", len(items)))
	for i, it := range items {
		if i >= tgMaxListItems {
			b.WriteString(fmt.Sprintf("\n…还有 %d 个，完整列表见控制台。\n", len(items)-tgMaxListItems))
			break
		}
		dc := it.Datacenter
		if dc == "" {
			dc = "任意机房"
		}
		b.WriteString(fmt.Sprintf("%d. %s @ %s\n", i+1, it.PlanCode, strings.ToUpper(dc)))
		b.WriteString("   状态 " + it.Status)
		if it.FailureCount > 0 {
			b.WriteString(fmt.Sprintf(" · 已失败 %d 次", it.FailureCount))
		}
		if acc, ok := state.FindAccount(it.AccountID); ok {
			b.WriteString(" · " + acc.Name)
		}
		b.WriteString("\n   取消：/cancel " + shortID(it.ID) + "\n")
	}
	b.WriteString("\n全部取消：/cancel all")
	return b.String()
}

// shortID 任务 ID 是 uuid,在手机上让人照着打完整的不现实。
// 取前 8 位做前缀匹配 —— 冲突概率极低,真撞上了会让用户用更长的前缀。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func cancelText(state *app.State, args []string) string {
	if len(args) == 0 {
		return "用法：/cancel <任务号> 或 /cancel all\n\n发 /queue 看任务号。"
	}
	target := strings.ToLower(strings.TrimSpace(args[0]))

	state.QueueMu.Lock()
	var matched []int
	for i := range state.Queue {
		it := state.Queue[i]
		if it.Status != "running" && it.Status != "pending" && it.Status != "paused" {
			continue
		}
		if target == "all" || strings.HasPrefix(strings.ToLower(it.ID), target) {
			matched = append(matched, i)
		}
	}
	if len(matched) == 0 {
		state.QueueMu.Unlock()
		return "没找到匹配的进行中任务：" + target + "\n\n发 /queue 看当前任务号。"
	}
	if target != "all" && len(matched) > 1 {
		state.QueueMu.Unlock()
		return fmt.Sprintf("任务号 %s 匹配到 %d 个任务，太短了。请多打几位。", target, len(matched))
	}
	// 倒着删,避免前面的删除把后面的下标挪掉
	killed := make([]string, 0, len(matched))
	for i := len(matched) - 1; i >= 0; i-- {
		idx := matched[i]
		it := state.Queue[idx]
		killed = append(killed, it.PlanCode+" @ "+strings.ToUpper(orAny(it.Datacenter)))
		state.DeletedTaskIDsMu.Lock()
		state.DeletedTaskIDs[it.ID] = struct{}{}
		state.DeletedTaskIDsMu.Unlock()
		state.Queue = append(state.Queue[:idx], state.Queue[idx+1:]...)
	}
	state.QueueMu.Unlock()

	// 落库失败必须说 —— 不说的话用户以为取消了,重启后任务原地复活继续抢
	if err := state.SaveQueue(); err != nil {
		state.Logger.Error("Telegram 取消任务后保存队列失败: "+err.Error(), "telegram")
		return fmt.Sprintf("⚠️ 已从运行中的队列移除 %d 个任务，但没能写进数据库，重启后会重新出现：\n%s",
			len(killed), err.Error())
	}
	state.Logger.Info(fmt.Sprintf("Telegram 取消了 %d 个抢购任务", len(killed)), "telegram")

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🛑 已取消 %d 个任务\n\n", len(killed)))
	for i, k := range killed {
		if i >= tgMaxListItems {
			b.WriteString(fmt.Sprintf("…还有 %d 个\n", len(killed)-tgMaxListItems))
			break
		}
		b.WriteString("  • " + k + "\n")
	}
	return b.String()
}

func orAny(dc string) string {
	if dc == "" {
		return "任意机房"
	}
	return dc
}

func accountsText(state *app.State) string {
	state.AccountsMu.RLock()
	accs := make([]types.OVHAccount, len(state.Accounts))
	copy(accs, state.Accounts)
	state.AccountsMu.RUnlock()

	if len(accs) == 0 {
		return "还没有配置 OVH 账户。请到控制台「设置 → OVH 账户」添加。"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("👤 OVH 账户（%d 个）\n\n", len(accs)))
	for _, a := range accs {
		b.WriteString("  • " + a.Name)
		if a.IsDefault {
			b.WriteString("（默认）")
		}
		// 大区决定能买到什么 —— 三区目录互不相通,同型号代码都不一样
		b.WriteString("\n    子公司 " + strings.ToUpper(a.Zone))
		b.WriteString("\n")
	}
	b.WriteString("\n注意：三个大区的机型目录互不相通，同一台机器在不同区的型号代码不一样。")
	return b.String()
}

func subsText(mon *monitor.Monitor) string {
	if mon == nil {
		return "监控未初始化。"
	}
	subs := mon.Snapshot()
	if len(subs) == 0 {
		return "📭 还没有监控订阅。到控制台「服务器监控」添加。"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🔔 监控订阅（%d 个）\n\n", len(subs)))
	for i, s := range subs {
		if i >= tgMaxListItems {
			b.WriteString(fmt.Sprintf("\n…还有 %d 个。\n", len(subs)-tgMaxListItems))
			break
		}
		b.WriteString("  • " + s.PlanCode)
		if len(s.Datacenters) > 0 {
			b.WriteString(" @ " + strings.ToUpper(strings.Join(s.Datacenters, "/")))
		}
		if s.AutoOrderAccountID != "" {
			b.WriteString(" · 自动下单")
		}
		b.WriteString("\n")
		// 检查失败要显式说:表现和"一直无货"一模一样,不说用户永远发现不了
		if s.LastCheckError != "" {
			b.WriteString("    ⚠️ 最近一次检查失败：" + truncate(s.LastCheckError, 80) + "\n")
		}
	}
	return b.String()
}

func recentText(state *app.State) string {
	state.HistoryMu.Lock()
	n := len(state.History)
	start := n - tgMaxListItems
	if start < 0 {
		start = 0
	}
	recent := make([]types.PurchaseHistoryEntry, 0, n-start)
	for i := n - 1; i >= start; i-- {
		recent = append(recent, state.History[i])
	}
	state.HistoryMu.Unlock()

	if len(recent) == 0 {
		return "📭 还没有抢购记录。"
	}
	var b strings.Builder
	b.WriteString("📜 最近的抢购结果\n\n")
	for _, h := range recent {
		icon := "❌"
		if h.Status == "success" {
			icon = "✅"
		}
		b.WriteString(icon + " " + h.PlanCode + " @ " + strings.ToUpper(orAny(h.Datacenter)))
		if t, ok := types.ParseTS(h.PurchaseTime); ok {
			b.WriteString("  " + t.Format("01-02 15:04"))
		}
		b.WriteString("\n")
		if h.Status == "success" {
			// 付款状态是这里最该说的一件事:抢到但没付款,逾期就作废了
			switch h.OrderStatus {
			case "delivered":
				b.WriteString("    订单 " + h.OrderID + " · 已交付\n")
			case "cancelled":
				b.WriteString("    订单 " + h.OrderID + " · 已取消\n")
			case "":
				b.WriteString("    订单 " + h.OrderID + " · ⚠️ 付款状态未知，请去面板确认\n")
			default:
				b.WriteString("    订单 " + h.OrderID + " · " + h.OrderStatus + "（未付款会逾期作废）\n")
			}
		} else if h.ErrorMessage != nil && *h.ErrorMessage != "" {
			b.WriteString("    " + truncate(*h.ErrorMessage, 90) + "\n")
		}
	}
	return b.String()
}
