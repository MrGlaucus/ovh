package handlers

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/ovh"
	"github.com/ovh-buy/server/internal/purchase"
	"github.com/ovh-buy/server/internal/types"
	ovhsdk "github.com/ovh/go-ovh/ovh"
)

type accountOrderPrice struct {
	Value        float64 `json:"value"`
	Text         string  `json:"text"`
	CurrencyCode string  `json:"currencyCode"`
}
type orderHistoryLink struct {
	ID         string `json:"id"`
	PlanCode   string `json:"planCode"`
	Datacenter string `json:"datacenter"`
}
type accountOrder struct {
	OrderID          string             `json:"orderId"`
	Date             string             `json:"date"`
	ExpirationDate   string             `json:"expirationDate,omitempty"`
	RetractionDate   string             `json:"retractionDate,omitempty"`
	PDFURL           string             `json:"pdfUrl,omitempty"`
	URL              string             `json:"url,omitempty"`
	Price            *accountOrderPrice `json:"priceWithTax,omitempty"`
	OrderStatus      string             `json:"orderStatus,omitempty"`
	OrderStatusAt    string             `json:"orderStatusAt,omitempty"`
	StatusError      string             `json:"statusError,omitempty"`
	Refund           *types.RefundInfo  `json:"refund,omitempty"`
	RefundCheckedAt  string             `json:"refundCheckedAt,omitempty"`
	RefundCheckError string             `json:"refundCheckError,omitempty"`
	RefundSkipped    bool               `json:"refundSkipped,omitempty"`
	Histories        []orderHistoryLink `json:"histories"`
}
type accountOrdersSnapshot struct {
	Items     []accountOrder `json:"items"`
	UpdatedAt string         `json:"updatedAt,omitempty"`
	Failed    int            `json:"failed"`
	Warning   string         `json:"warning,omitempty"`
}
type accountOrdersJob struct {
	Snapshot  accountOrdersSnapshot
	Status    string
	Error     string
	LastStart time.Time
	Touched   time.Time
}

func accountOrdersCacheKey(acc types.OVHAccount, focus string) string {
	// 凭据、代理和站点变化后不复用旧快照；不把鉴权信息写入键名。
	identity, _ := json.Marshal([]string{acc.ID, acc.Endpoint, acc.AppKey, acc.AppSecret, acc.ConsumerKey, acc.ProxyURL, acc.ExpectedOutboundIP, focus})
	return fmt.Sprintf("official-orders:%x", sha256.Sum256(identity))
}

// AccountOrders GET 返回本地快照/进度；首次访问和 POST 在后台查询，避免代理 524。
// GET/POST 必须共用同一个 handler 实例。所有 OVH 调用使用确定的账户客户端。
func AccountOrders(state *app.State) gin.HandlerFunc {
	var mu sync.Mutex
	jobs := map[string]*accountOrdersJob{}
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		acc, ok := ovhAccountFor(state, c)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请选择有效账户"})
			return
		}
		focus := strings.TrimSpace(c.Query("order"))
		if focus != "" {
			id, err := strconv.ParseInt(focus, 10, 64)
			if err != nil || id <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "订单号无效"})
				return
			}
			focus = strconv.FormatInt(id, 10)
		}
		key := accountOrdersCacheKey(acc, focus)
		mu.Lock()
		for k, j := range jobs {
			if j.Status != "running" && time.Since(j.Touched) > time.Hour {
				delete(jobs, k)
			}
		}
		job := jobs[key]
		if job == nil {
			job = &accountOrdersJob{Status: "idle", Snapshot: accountOrdersSnapshot{Items: []accountOrder{}}}
			exists, err := state.DB.GetKV(key, &job.Snapshot)
			if err != nil {
				mu.Unlock()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "订单缓存读取失败"})
				return
			}
			if exists {
				job.Status = "completed"
			}
			jobs[key] = job
		}
		job.Touched = time.Now()
		if job.Status != "running" && (job.Status == "idle" || c.Request.Method == http.MethodPost) {
			if !job.LastStart.IsZero() && time.Since(job.LastStart) < 15*time.Second {
				mu.Unlock()
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "刷新太频繁，请稍后重试"})
				return
			}
			job.Status, job.Error, job.LastStart = "running", "", time.Now()
			previous := append([]accountOrder{}, job.Snapshot.Items...)
			go func(job *accountOrdersJob, acc types.OVHAccount, focus, key string) {
				var snapshot accountOrdersSnapshot
				var err error
				defer func() {
					if r := recover(); r != nil {
						err = fmt.Errorf("订单刷新异常: %v", r)
					}
					mu.Lock()
					job.Status = "completed"
					if err != nil {
						job.Status = "failed"
						job.Error = ovh.Explain(err)
					} else {
						job.Snapshot = snapshot
					}
					mu.Unlock()
					if err != nil {
						state.Logger.Warn("账户订单刷新失败: "+err.Error(), "account_management")
					}
				}()
				client, e := state.OVH.ClientFor(acc.ID)
				if e != nil {
					err = e
					return
				}
				snapshot, err = loadAccountOrders(state, acc, client, focus, previous)
				if err != nil {
					return
				}
				// 刷新期间账户删除/更换凭据，不再将旧身份的结果写入历史或缓存。
				current, exists := state.FindAccount(acc.ID)
				if !exists || accountOrdersCacheKey(current, focus) != key {
					err = fmt.Errorf("账户配置已变化，请重新刷新")
					return
				}
				syncOfficialOrdersToHistory(state, acc.ID, snapshot.Items)
				if err = state.SaveHistory(); err != nil {
					return
				}
				err = state.DB.SetKV(key, snapshot)
			}(job, acc, focus, key)
		}
		snapshot := job.Snapshot
		snapshot.Items = append([]accountOrder{}, snapshot.Items...)
		status, jobError := job.Status, job.Error
		mu.Unlock()
		attachOrderHistory(state, acc.ID, snapshot.Items)
		c.JSON(http.StatusOK, gin.H{"accountId": acc.ID, "items": snapshot.Items, "updatedAt": snapshot.UpdatedAt, "failed": snapshot.Failed, "warning": snapshot.Warning, "status": status, "error": jobError})
	}
}

func loadAccountOrders(state *app.State, acc types.OVHAccount, client *ovhsdk.Client, focus string, previous []accountOrder) (accountOrdersSnapshot, error) {
	out := accountOrdersSnapshot{Items: []accountOrder{}}
	known := make(map[string]accountOrder, len(previous))
	attachOrderHistory(state, acc.ID, previous)
	for _, order := range previous {
		known[order.OrderID] = order
	}
	ids := []string{focus}
	if focus == "" {
		var err error
		ids, out.Warning, err = fetchRecentBillingIDs(client, "/me/order", accountBillingListSize)
		if err != nil {
			return out, err
		}
		sortBillingIDsDesc(ids)
		if len(ids) > accountBillingListSize {
			ids = ids[:accountBillingListSize]
		}
	}
	// 白名单详情字段，永远不存储/透传 billing.Order.password 或带口令的 checkout URL。
	results := make([]*accountOrder, len(ids))
	failures := make([]error, len(ids))
	refundLookup := purchase.NewOrderRefundLookup(client)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, id string) {
			defer wg.Done()
			defer func() { <-sem }()
			var detail struct {
				OrderID        int64              `json:"orderId"`
				Date           string             `json:"date"`
				ExpirationDate string             `json:"expirationDate"`
				RetractionDate string             `json:"retractionDate"`
				PDFURL         string             `json:"pdfUrl"`
				Price          *accountOrderPrice `json:"priceWithTax"`
			}
			if err := client.Get("/me/order/"+id, &detail); err != nil {
				failures[i] = err
				return
			}
			if strconv.FormatInt(detail.OrderID, 10) != id {
				failures[i] = fmt.Errorf("订单详情编号不一致: %s", id)
				return
			}
			order := &accountOrder{OrderID: id, Date: detail.Date, ExpirationDate: detail.ExpirationDate, RetractionDate: detail.RetractionDate, PDFURL: detail.PDFURL, Price: detail.Price, URL: ovh.ManagerOrderURL(acc.Endpoint, id), Histories: []orderHistoryLink{}}
			results[i] = order
			if old, ok := known[id]; ok && old.Refund != nil {
				order.Refund, order.RefundCheckedAt = old.Refund, old.RefundCheckedAt
			}
			linked := []accountOrder{*order}
			attachOrderHistory(state, acc.ID, linked)
			if linked[0].Refund != nil {
				order.Refund, order.RefundCheckedAt = linked[0].Refund, linked[0].RefundCheckedAt
			}
			status, err := purchase.FetchOrderStatus(client, id)
			if err != nil || status == "" {
				order.StatusError = "订单状态未能读取"
				if err != nil {
					order.StatusError = ovh.Explain(err)
				}
				if order.Refund == nil {
					order.RefundCheckError = "支付状态未确认，本轮未查询退款"
				}
				return
			}
			order.OrderStatus, order.OrderStatusAt = status, types.NowISO()
			if order.Refund != nil {
				return // 已确认退款不重复查询，仍更新支付状态。
			}
			if status == "notPaid" {
				order.RefundSkipped = true
				return
			}
			// 已取消的未付款订单没有原发票，lookup 不会扫描退款列表。
			order.Refund, err = refundLookup(id)
			if err != nil {
				order.RefundCheckError = ovh.Explain(err)
			} else {
				order.RefundCheckedAt = types.NowISO()
			}
		}(i, id)
	}
	wg.Wait()
	for _, order := range results {
		if order == nil {
			out.Failed++
			continue
		}
		if order.StatusError != "" || order.RefundCheckError != "" {
			out.Failed++
		}
		out.Items = append(out.Items, *order)
	}
	if len(ids) > 0 && len(out.Items) == 0 {
		for _, err := range failures {
			if err != nil {
				return out, fmt.Errorf("订单详情读取失败: %w", err)
			}
		}
	}
	out.UpdatedAt = types.NowISO()
	return out, nil
}

// 只用账户 ID + 原订单号关联，绝不按型号、时间或退款订单号猜测。
func syncOfficialOrdersToHistory(state *app.State, accountID string, orders []accountOrder) {
	byID := map[string]accountOrder{}
	for _, o := range orders {
		byID[o.OrderID] = o
	}
	state.HistoryMu.Lock()
	defer state.HistoryMu.Unlock()
	for i := range state.History {
		h := &state.History[i]
		if h.AccountID != accountID || h.OrderID == "" {
			continue
		}
		o, ok := byID[h.OrderID]
		if !ok {
			continue
		}
		at, valid := types.ParseTS(o.OrderStatusAt)
		old, _ := types.ParseTS(h.OrderStatusAt)
		if valid && !at.Before(old) {
			h.SetOrderStatus(o.OrderStatus)
			h.OrderStatusAt = o.OrderStatusAt
		}
		if o.Refund != nil {
			h.Refund = o.Refund
		}
		checked, valid := types.ParseTS(o.RefundCheckedAt)
		oldChecked, _ := types.ParseTS(h.RefundCheckedAt)
		if valid && !checked.Before(oldChecked) {
			h.RefundCheckedAt = o.RefundCheckedAt
			h.RefundCheckError = ""
		} else if o.RefundCheckError != "" && h.Refund == nil {
			h.RefundCheckError = o.RefundCheckError
		}
	}
}

func attachOrderHistory(state *app.State, accountID string, orders []accountOrder) {
	state.HistoryMu.Lock()
	defer state.HistoryMu.Unlock()
	for i := range orders {
		o := &orders[i]
		o.Histories = []orderHistoryLink{}
		for _, h := range state.History {
			if h.AccountID != accountID || h.OrderID == "" || h.OrderID != o.OrderID {
				continue
			}
			o.Histories = append(o.Histories, orderHistoryLink{ID: h.ID, PlanCode: h.PlanCode, Datacenter: h.Datacenter})
			if at, ok := types.ParseTS(h.OrderStatusAt); ok {
				old, _ := types.ParseTS(o.OrderStatusAt)
				if at.After(old) {
					o.OrderStatus = h.OrderStatus
					o.OrderStatusAt = h.OrderStatusAt
				}
			}
			if h.Refund != nil {
				o.Refund = h.Refund
				o.RefundCheckError = ""
			}
			if at, ok := types.ParseTS(h.RefundCheckedAt); ok {
				old, _ := types.ParseTS(o.RefundCheckedAt)
				if at.After(old) && o.RefundCheckError == "" {
					o.RefundCheckedAt = h.RefundCheckedAt
					o.RefundCheckError = h.RefundCheckError
				}
			}
		}
	}
}
