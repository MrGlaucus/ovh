package refundreminder

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/catalog"
	"github.com/ovh-buy/server/internal/ovh"
	"github.com/ovh-buy/server/internal/purchase"
	"github.com/ovh-buy/server/internal/telegram"
	"github.com/ovh-buy/server/internal/types"
)

var china = time.FixedZone("Asia/Shanghai", 8*3600)

// Deadline mirrors the existing history window, with an explicit timezone.
// This is the panel's estimate, not an assertion of eligibility for a refund.
func Deadline(h types.PurchaseHistoryEntry) time.Time {
	var t time.Time
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, h.PurchaseTime); err == nil {
			t = parsed
			break
		}
	}
	if t.IsZero() {
		return time.Time{}
	}
	t = t.In(china)
	return time.Date(t.Year(), t.Month(), t.Day()+15, 0, 0, 0, 0, china)
}

type Item struct {
	History       types.PurchaseHistoryEntry `json:"history"`
	Account       string                     `json:"account"`
	Name          string                     `json:"name"`
	Configuration string                     `json:"configuration"`
	Deadline      time.Time                  `json:"deadline"`
	URL           string                     `json:"url"`
	Warning       string                     `json:"warning,omitempty"`
}

func itemKey(h types.PurchaseHistoryEntry) string { return h.AccountID + ":" + h.OrderID }

func Items(s *app.State, now time.Time) []Item {
	s.HistoryMu.Lock()
	history := append([]types.PurchaseHistoryEntry(nil), s.History...)
	s.HistoryMu.Unlock()
	out := []Item{}
	seen := map[string]bool{}
	refunded := map[string]bool{}
	for _, h := range history {
		if h.Refund != nil {
			refunded[itemKey(h)] = true
		}
	}
	// Prefer the most recently checked duplicate original order.
	sort.SliceStable(history, func(i, j int) bool { return history[i].OrderStatusAt > history[j].OrderStatusAt })
	for _, h := range history {
		if h.Status != "success" || h.AccountID == "" || h.OrderID == "" {
			continue
		}
		key := itemKey(h)
		if seen[key] {
			continue
		}
		seen[key] = true
		if h.OrderStatus != "delivering" || refunded[key] {
			continue
		}
		deadline := Deadline(h)
		if deadline.IsZero() || deadline.Sub(now) > 72*time.Hour {
			continue
		}
		a, ok := s.FindAccount(h.AccountID)
		if !ok {
			continue
		}
		name := h.PlanCode
		if s.ServerCache != nil {
			plans, _ := s.ServerCache.GetBucket(s.ServerCacheKey(a.ID))
			for _, p := range plans {
				if p.PlanCode == h.PlanCode {
					name = p.Name
					break
				}
			}
		}
		opts := []string{}
		for _, o := range h.Options {
			opts = append(opts, catalog.FormatOptionDisplay(o))
		}
		out = append(out, Item{History: h, Account: a.Name, Name: name, Configuration: strings.Join(opts, " / "), Deadline: deadline, URL: ovh.ManagerOrderURL(a.Endpoint, h.OrderID)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Deadline.Before(out[j].Deadline) })
	return out
}

// Only the daily shortlist is queried, using each order's original account client.
func refresh(s *app.State, now time.Time) []Item {
	warnings := map[string]string{}
	lookups := map[string]func(string) (*types.RefundInfo, error){}
	for _, item := range Items(s, now) {
		if !item.Deadline.After(now) {
			continue
		}
		h := item.History
		key := itemKey(h)
		client, err := s.OVH.ClientFor(h.AccountID)
		if err != nil {
			warnings[key] = "状态未核实：账户连接失败"
			continue
		}
		status, err := purchase.FetchOrderStatus(client, h.OrderID)
		if err != nil || status == "" {
			warnings[key] = "状态未核实：订单状态查询失败"
			continue
		}
		var refund *types.RefundInfo
		var refundErr error
		if status == "delivering" {
			if lookups[h.AccountID] == nil {
				lookups[h.AccountID] = purchase.NewOrderRefundLookup(client)
			}
			refund, refundErr = lookups[h.AccountID](h.OrderID)
			if refundErr != nil {
				warnings[key] = "退款状态未核实：查询失败"
			}
		}
		s.HistoryMu.Lock()
		for i := range s.History {
			current := &s.History[i]
			if current.AccountID != h.AccountID || current.OrderID != h.OrderID {
				continue
			}
			// Do not overwrite a concurrent refresh that completed after our snapshot.
			if current.OrderStatusAt == h.OrderStatusAt {
				current.OrderStatus = status
				current.OrderStatusAt = types.NowISO()
			}
			if refund != nil {
				current.Refund = refund
			}
			if status == "delivering" {
				if refundErr != nil {
					current.RefundCheckError = "每日退款提醒：退款查询失败"
				} else {
					current.RefundCheckedAt = types.NowISO()
					current.RefundCheckError = ""
				}
			}
		}
		s.HistoryMu.Unlock()
	}
	if err := s.SaveHistory(); err != nil {
		s.Logger.Warn("保存退款提醒核实结果失败: "+err.Error(), "refund_reminder")
	}
	items := Items(s, time.Now())
	for i := range items {
		items[i].Warning = warnings[itemKey(items[i].History)]
	}
	return items
}

type daily struct {
	Day  string          `json:"day"`
	Sent map[string]bool `json:"sent"`
	Done bool            `json:"done"`
}
type runner struct {
	dirty       bool
	nextAttempt time.Time
	state       *app.State
	ledger      daily
	key         string
	refresh     func(*app.State, time.Time) []Item
	send        func(string, map[string]interface{}) error
}

func (r *runner) checkpoint() error {
	r.dirty = true
	if err := r.state.DB.SetKV(r.key, r.ledger); err != nil {
		return err
	}
	r.dirty = false
	return nil
}

// One serial worker; successful chunks are checkpointed across container restarts.
func (r *runner) tick(now time.Time) error {
	cfg := r.state.Config.Get()
	if cfg.TgToken == "" || cfg.TgChatID == "" || now.In(china).Hour() < 9 {
		return nil
	}
	day := now.In(china).Format("2006-01-02")
	key := fmt.Sprintf("refund-reminder:%x", sha256.Sum256([]byte(cfg.TgToken+"\x00"+cfg.TgChatID)))
	if r.key != key || r.ledger.Day != day {
		var stored daily
		if _, err := r.state.DB.GetKV(key, &stored); err != nil {
			return err
		}
		if stored.Day != day {
			stored = daily{Day: day, Sent: map[string]bool{}}
		}
		if stored.Sent == nil {
			stored.Sent = map[string]bool{}
		}
		r.ledger = stored
		r.key = key
	}
	// Also retry a failed checkpoint before any more sends.
	if r.dirty {
		if err := r.checkpoint(); err != nil {
			return err
		}
	}
	if r.ledger.Done {
		return nil
	}
	if now.Before(r.nextAttempt) {
		return nil
	}
	items := r.refresh(r.state, now)
	currentCfg := r.state.Config.Get()
	if currentCfg.TgToken != cfg.TgToken || currentCfg.TgChatID != cfg.TgChatID {
		return fmt.Errorf("通知目标已变更，下轮重新核实")
	}
	pending := []Item{}
	for _, i := range items {
		if i.Deadline.After(time.Now()) && !r.ledger.Sent[itemKey(i.History)] {
			pending = append(pending, i)
		}
	}
	for len(pending) > 0 {
		size := 3
		if len(pending) < size {
			size = len(pending)
		}
		batch := pending[:size]
		msg, markup := message(batch, time.Now())
		if cfg.DashboardURL != "" {
			markup["inline_keyboard"] = append(markup["inline_keyboard"].([]interface{}), []map[string]string{{"text": "打开仪表盘", "url": cfg.DashboardURL}})
		}
		if err := r.send(msg, markup); err != nil {
			delay := 30 * time.Second
			var failure *telegram.SendError
			if errors.As(err, &failure) && failure.RetryAfter > delay {
				delay = failure.RetryAfter
			}
			r.nextAttempt = time.Now().Add(delay)
			return err
		}
		for _, i := range batch {
			r.ledger.Sent[itemKey(i.History)] = true
		}
		if err := r.checkpoint(); err != nil {
			return err
		}
		pending = pending[size:]
	}
	r.ledger.Done = true
	return r.checkpoint()
}

func clipped(s string, n int) string {
	v := []rune(s)
	if len(v) > n {
		return string(v[:n]) + "…"
	}
	return s
}
func message(items []Item, now time.Time) (string, map[string]interface{}) {
	var b strings.Builder
	b.WriteString("⏰ 退款期限提醒\n\n以下订单仍在交付中，预计退款窗口将在3天内结束。\n")
	buttons := []interface{}{}
	for _, i := range items {
		remaining := i.Deadline.Sub(now)
		mark := "🟠"
		if remaining <= 24*time.Hour {
			mark = "🔴"
		}
		fmt.Fprintf(&b, "\n%s %s｜%s\n👤 账户：%s\n🧩 配置：%s\n🧾 原订单：<code>%s</code>\n📦 已付款·交付中\n⌛ 剩余：%d天%d小时\n📅 截止：%s（北京时间）\n", mark, html.EscapeString(clipped(i.Name, 100)), html.EscapeString(i.History.Datacenter), html.EscapeString(clipped(i.Account, 60)), html.EscapeString(clipped(i.Configuration, 180)), html.EscapeString(i.History.OrderID), int(remaining.Hours())/24, int(remaining.Hours())%24, i.Deadline.In(china).Format("2006-01-02 15:04"))
		if i.Warning != "" {
			b.WriteString("⚠️ " + html.EscapeString(i.Warning) + "，以上为最后已知状态\n")
		}
		buttons = append(buttons, []map[string]string{{"text": "查看订单 #" + i.History.OrderID, "url": i.URL}})
	}
	b.WriteString("\n截止按面板现有规则估算，请到 OVH 核实能否撤回，及时决定是否继续等待。\n🕘 检查时间：" + now.In(china).Format("2006-01-02 15:04"))
	return b.String(), map[string]interface{}{"inline_keyboard": buttons}
}

func Run(s *app.State) {
	r := runner{state: s, refresh: refresh, send: func(msg string, markup map[string]interface{}) error {
		_, err := telegram.SendHTMLMessageChecked(s, msg, markup)
		return err
	}}
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	for {
		if err := r.tick(time.Now()); err != nil {
			s.Logger.Warn("退款期限提醒失败，30秒后重试: "+err.Error(), "refund_reminder")
		}
		<-timer.C
	}
}
