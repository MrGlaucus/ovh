package purchase

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	ovhsdk "github.com/ovh/go-ovh/ovh"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/types"
)

// 退款关联：把抢购历史里"下单成功了的那笔订单"和 OVH 的退款记录接起来。
//
// OVH 取消订单不会把退款写回订单对象（billing.Order 没有 refund 字段，
// OrderStatusEnum 8 个值也没有退款相关取值），退款是独立对象 billing.Refund：
// 用户手动在面板取消订单、或走撤回权退订后，OVH 生成一条带原订单号（orderId）
// 的退款记录。反向查询就是 GET /me/refund?orderId={orderId} —— 该过滤参数
// EU / US / CA 三区 schema 都有（已逐一核对）。
//
// 退款单没有状态字段（schema 里就没有 status），所以本地标记只有"有/没有"：
// 记录出现即"已退款"。"钱到没到账"的时间差在支付渠道侧（银行卡/PayPal 可能
// 要几天到几十天），API 看不到，不做假状态。

// refundCheckMinInterval 同一单两次查退款的最小间隔。
//
// 不退款是绝大多数订单的常态，而 /me 命名空间和抢购主链路（查库存/建车/结账）
// 共用账户配额 —— 不节流的话每 10 分钟一轮后台会把所有未退款成功单全查一遍，
// 天长日久纯属浪费。手动刷新（force）跳过它："我刚在面板取消完订单"要能立即看到。
const refundCheckMinInterval = time.Hour

// refundMaxAge 超过这么久还没查到退款就不再查。与订单状态刷新同一口径
// （orderStatusMaxAge）：越过撤回权窗口很久还没有退款记录，基本不会再有了。
const refundMaxAge = 30 * 24 * time.Hour

// refundQueryConcurrency 并发数。退款查询是轻量 GET（多数响应只是一个 ID 数组），
// 但账户配额同时服务抢购主链路，不能打太猛。
const refundQueryConcurrency = 8

// FetchOrderRefundIDs 列出某订单关联的退款记录 ID。
// 响应是 ID 数组，且两种形态都见过（["12"] 或 [12]），宽容解析。
func FetchOrderRefundIDs(client *ovhsdk.Client, orderID string) ([]string, error) {
	var raw []interface{}
	if err := client.Get("/me/refund?orderId="+url.QueryEscape(orderID), &raw); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(raw))
	for _, v := range raw {
		if s := billingIDToString(v); s != "" {
			ids = append(ids, s)
		}
	}
	return ids, nil
}

// billingIDToString 把 OVH 数组端点里的 ID（可能是字符串也可能是数字）转成字符串。
func billingIDToString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprintf("%v", x)
	}
}

// refundDetail 退款详情里只取确认与展示要用的字段（billing.Refund 其余字段用不上）。
type refundDetail struct {
	RefundID string `json:"refundId"`
	OrderID  int64  `json:"orderId"`
	Date     string `json:"date"`
	Price    *struct {
		Value        float64 `json:"value"`
		CurrencyCode string  `json:"currencyCode"`
	} `json:"priceWithTax"`
	PDFURL string `json:"pdfUrl"`
}

// FetchOrderRefund 查一张订单是否已有退款记录，有则返回最新一条的摘要。
//
// 服务端已按 orderId 过滤，但详情里的 orderId 仍会再核对一次：万一某个区
// 没实现该过滤参数而返回全量列表，也不能把别的订单的退款标到这单头上 ——
// 错标退款比不标更糟。
func FetchOrderRefund(client *ovhsdk.Client, orderID string) (*types.RefundInfo, error) {
	ids, err := FetchOrderRefundIDs(client, orderID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var refunds []types.RefundInfo
	for _, id := range ids {
		var d refundDetail
		if err := client.Get("/me/refund/"+url.PathEscape(id), &d); err != nil {
			// 列表说有、详情拉不到：这轮先跳过，不影响已有数据
			continue
		}
		if strconv.FormatInt(d.OrderID, 10) != orderID {
			continue
		}
		info := types.RefundInfo{ID: d.RefundID, Date: d.Date, PDFURL: d.PDFURL}
		if info.ID == "" {
			info.ID = id
		}
		if d.Price != nil {
			v := d.Price.Value
			info.Price = &types.PriceInfo{WithTax: &v, CurrencyCode: d.Price.CurrencyCode}
		}
		refunds = append(refunds, info)
	}
	if len(refunds) == 0 {
		return nil, nil
	}
	// 一单理论上可能有多条退款（整单/部分），列表对顺序没有承诺 —— 按日期取最新。
	sort.SliceStable(refunds, func(i, j int) bool {
		ti, oki := types.ParseTS(refunds[i].Date)
		tj, okj := types.ParseTS(refunds[j].Date)
		if oki && okj {
			return ti.After(tj)
		}
		return refunds[i].Date > refunds[j].Date
	})
	return &refunds[0], nil
}

// RefreshRefundStatuses 给还没标记退款的成功单各查一次退款记录，命中就写回。
// force=true 跳过节流（手动刷新用）。返回这次新标记了几条。
func RefreshRefundStatuses(state *app.State, force bool) int {
	// 先拍快照，网络请求不能拿着 HistoryMu
	state.HistoryMu.Lock()
	type target struct {
		entryID, orderID, accountID string
	}
	var targets []target
	now := time.Now()
	for _, h := range state.History {
		if h.Status != "success" || h.OrderID == "" || h.Refund != nil {
			continue
		}
		// 没付款的订单不会产生退款：未付款被取消是作废，不是退款
		if h.OrderStatus == "notPaid" {
			continue
		}
		// 太老的订单不再查（与订单状态刷新同一口径）
		if pt, ok := types.ParseTS(h.PurchaseTime); ok && now.Sub(pt) > refundMaxAge {
			continue
		}
		// 按小时节流；手动刷新跳过
		if !force && h.RefundCheckedAt != "" {
			if at, ok := types.ParseTS(h.RefundCheckedAt); ok && now.Sub(at) < refundCheckMinInterval {
				continue
			}
		}
		targets = append(targets, target{h.ID, h.OrderID, h.AccountID})
	}
	state.HistoryMu.Unlock()

	if len(targets) == 0 {
		return 0
	}

	var (
		wg      sync.WaitGroup
		sem     = make(chan struct{}, refundQueryConcurrency)
		mu      sync.Mutex
		updated int
	)
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t target) {
			defer wg.Done()
			defer func() { <-sem }()
			client, err := state.OVH.ClientFor(t.accountID)
			if err != nil {
				// 账户删了 —— 这单的退款永远查不到了，不用每轮都报
				return
			}
			info, err := FetchOrderRefund(client, t.orderID)
			if err != nil {
				// 网络/权限问题：不更新 checkedAt，下一轮会重试
				state.Logger.Warn(fmt.Sprintf("查询订单 %s 退款记录失败: %s", t.orderID, err.Error()), "purchase")
				return
			}
			if ApplyRefundCheck(state, t.entryID, info) {
				mu.Lock()
				updated++
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()

	// 每条目标的 checkedAt 都刷新过（命中与否都算查过），统一落一次盘
	go state.SaveHistory()
	return updated
}

// ApplyRefundCheck 写回一条历史本轮退款查询的结果：刷新查询时间；
// 有退款且是新发现时写入退款信息。返回是否为"新标记退款"。
func ApplyRefundCheck(state *app.State, entryID string, info *types.RefundInfo) bool {
	state.HistoryMu.Lock()
	defer state.HistoryMu.Unlock()
	for i := range state.History {
		if state.History[i].ID != entryID {
			continue
		}
		state.History[i].RefundCheckedAt = types.NowISO()
		if info == nil {
			return false
		}
		if state.History[i].Refund != nil && state.History[i].Refund.ID == info.ID {
			return false
		}
		state.History[i].Refund = info
		state.Logger.Info(fmt.Sprintf("订单 %s 已退款(退款单 %s, %s)", state.History[i].OrderID, info.ID, info.Date), "purchase")
		return true
	}
	return false
}
