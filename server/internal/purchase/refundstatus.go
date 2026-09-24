package purchase

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/types"
)

// 退款关联：把抢购历史里"下单成功了的那笔订单"和 OVH 的退款记录接起来。
//
// OVH 取消订单不会把退款写回订单对象（billing.Order 没有 refund 字段，
// OrderStatusEnum 8 个值也没有退款相关取值），退款是独立对象 billing.Refund：
// 退款单的 orderId 可能属于新生成的退款订单，不能与购买订单号直接匹配。
// 按原订单查发票，再用退款单 originalBillId 关联；只在同一账户内匹配。
//
// 退款单没有状态字段（schema 里就没有 status），所以本地标记只有"有/没有"：
// 记录出现即"已退款"。"钱到没到账"的时间差在支付渠道侧（银行卡/PayPal 可能
// 要几天到几十天），API 看不到，不做假状态。

// refundCheckMinInterval 同一单两次查退款的最小间隔。
//
// 退款查询现在只在用户手动刷新（force=true，跳过节流）时执行，没有后台轮询。
// 保留这个间隔只为防御非 force 的批量入口：不退款是绝大多数订单的常态，
// 而 /me 命名空间和抢购主链路（查库存/建车/结账）共用账户配额，不能无节制地重复查。
const refundCheckMinInterval = time.Hour

// refundMaxAge 超过这么久还没查到退款就不再查。与订单状态刷新同一口径
// （orderStatusMaxAge）：越过撤回权窗口很久还没有退款记录，基本不会再有了。
const refundMaxAge = 30 * 24 * time.Hour

// refundQueryConcurrency 并发数。退款查询是轻量 GET（多数响应只是一个 ID 数组），
// 但账户配额同时服务抢购主链路，不能打太猛。
const refundQueryConcurrency = 8

type refundClient interface {
	Get(string, interface{}) error
}

// refundDetail 退款详情里只取确认与展示要用的字段（billing.Refund 其余字段用不上）。
type refundDetail struct {
	OriginalBillID string `json:"originalBillId"`
	RefundID       string `json:"refundId"`
	OrderID        int64  `json:"orderId"`
	Date           string `json:"date"`
	Price          *struct {
		Value        float64 `json:"value"`
		CurrencyCode string  `json:"currencyCode"`
	} `json:"priceWithTax"`
	PDFURL string `json:"pdfUrl"`
}

// FetchOrderRefund 查一张订单是否已有退款记录，有则返回最新一条的摘要。
//
// 核对原发票上的 orderId，再匹配退款单 originalBillId；不比较退款订单号。
func FetchOrderRefund(client refundClient, orderID string) (*types.RefundInfo, error) {
	index := loadRefundIndex(client)
	return index.find(client, orderID)
}

// 一次手动刷新中每账户只读一次退款列表，避免每张历史订单重复扫描。
type refundIndex struct {
	byBill map[string][]refundDetail
	err    error
}

func loadRefundIndex(client refundClient) refundIndex {
	index := refundIndex{byBill: map[string][]refundDetail{}}
	var ids []string
	if err := client.Get("/me/refund", &ids); err != nil {
		index.err = fmt.Errorf("读取账户退款列表失败: %w", err)
		return index
	}
	for _, id := range ids {
		var detail refundDetail
		if err := client.Get("/me/refund/"+url.PathEscape(id), &detail); err != nil {
			index.err = fmt.Errorf("退款详情 %s 读取失败: %w", id, err)
			continue
		}
		if detail.RefundID == "" {
			detail.RefundID = id
		}
		if detail.OriginalBillID == "" {
			index.err = fmt.Errorf("部分退款单缺少原发票号，无法完整核对关联")
			continue
		}
		index.byBill[detail.OriginalBillID] = append(index.byBill[detail.OriginalBillID], detail)
	}
	return index
}

func (index refundIndex) find(client refundClient, orderID string) (*types.RefundInfo, error) {
	var billIDs []string
	if err := client.Get("/me/bill?orderId="+url.QueryEscape(orderID), &billIDs); err != nil {
		return nil, fmt.Errorf("读取原订单发票失败: %w", err)
	}
	var refunds []types.RefundInfo
	for _, billID := range billIDs {
		var bill struct {
			OrderID int64 `json:"orderId"`
		}
		if err := client.Get("/me/bill/"+url.PathEscape(billID), &bill); err != nil {
			return nil, fmt.Errorf("核对原发票 %s 失败: %w", billID, err)
		}
		if strconv.FormatInt(bill.OrderID, 10) != orderID {
			continue
		}
		for _, d := range index.byBill[billID] {
			info := types.RefundInfo{ID: d.RefundID, Date: d.Date, PDFURL: d.PDFURL, OriginalBillID: billID, RefundOrderID: strconv.FormatInt(d.OrderID, 10)}
			if d.Price != nil {
				v := d.Price.Value
				info.Price = &types.PriceInfo{WithTax: &v, CurrencyCode: d.Price.CurrencyCode}
			}
			refunds = append(refunds, info)
		}
	}
	if len(refunds) == 0 {
		return nil, index.err
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
// force=true 跳过节流（手动刷新用，也是目前唯一的调用方式）。返回这次新标记了几条。
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
		if !force && h.OrderStatus == "notPaid" {
			continue
		}
		// 自动调用仍限制账龄；手动刷新允许追溯旧单及延迟退款。
		if pt, ok := types.ParseTS(h.PurchaseTime); !force && ok && now.Sub(pt) > refundMaxAge {
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
	type accountIndex struct {
		once  sync.Once
		index refundIndex
	}
	indexes := map[string]*accountIndex{}
	for _, t := range targets {
		if indexes[t.accountID] == nil {
			indexes[t.accountID] = &accountIndex{}
		}
	}
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t target) {
			defer wg.Done()
			defer func() { <-sem }()
			if t.accountID == "" {
				applyRefundError(state, t.entryID, "缺少原购买账户，无法安全关联退款")
				return
			}
			client, err := state.OVH.ClientFor(t.accountID)
			if err != nil {
				applyRefundError(state, t.entryID, "原购买账户不可用: "+err.Error())
				return
			}
			cache := indexes[t.accountID]
			cache.once.Do(func() { cache.index = loadRefundIndex(client) })
			info, err := cache.index.find(client, t.orderID)
			if err != nil {
				// 网络/权限问题：不更新 checkedAt，下一轮会重试
				state.Logger.Warn(fmt.Sprintf("查询订单 %s 退款记录失败: %s", t.orderID, err.Error()), "purchase")
				applyRefundError(state, t.entryID, err.Error())
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
		state.History[i].RefundCheckError = ""
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

func applyRefundError(state *app.State, entryID, message string) {
	state.HistoryMu.Lock()
	defer state.HistoryMu.Unlock()
	for i := range state.History {
		if state.History[i].ID == entryID {
			state.History[i].RefundCheckError = message
			return
		}
	}
}
