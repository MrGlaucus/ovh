package purchase

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/numconv"
	"github.com/ovh-buy/server/internal/ovh"
	"github.com/ovh-buy/server/internal/telegram"
	"github.com/ovh-buy/server/internal/types"
)

// PurchaseServer 对应 Python: purchase_server
// 返回是否成功。多账户:用 item.AccountID 取对应 OVH client 和 subsidiary。
func PurchaseServer(state *app.State, item *types.QueueItem) bool {
	client, err := state.OVH.ClientFor(item.AccountID)
	if err != nil {
		state.Logger.Error("PurchaseServer: 取 OVH client 失败 ("+item.AccountID+"): "+err.Error(), "purchase")
		return false
	}

	cartID := ""
	var itemID int64

	state.Logger.Info(fmt.Sprintf("开始为 %s 在 %s 的购买流程，选项: %v",
		item.PlanCode, item.Datacenter, item.Options), "purchase")

	// 检查可用性
	var availabilities []map[string]interface{}
	q := url.Values{}
	q.Set("planCode", item.PlanCode)
	if err := client.Get("/dedicated/server/datacenter/availabilities?"+q.Encode(), &availabilities); err != nil {
		errMsg := err.Error()
		state.Logger.Error(fmt.Sprintf("购买 %s 时发生 OVH API 错误: %s", item.PlanCode, errMsg), "purchase")
		recordFailure(state, item, errMsg)
		return false
	}

	apiDC := ovh.ConvertDisplayDCToAPIDC(item.Datacenter)
	foundAvailable := false
	// 记下"实际可用的那条 FQN"。FQN 格式：<planCode>.<addon1>.<addon2>...
	// 用户没显式指定 options 时，会从这个 FQN 推断 addon，让订单走"有货的那套配置"，
	// 不再退化到 OVH 默认 addon（多半是 HDD / 最小内存）。
	var availableFQN string
	for _, av := range availabilities {
		if dcsRaw, ok := av["datacenters"].([]interface{}); ok {
			for _, dcRaw := range dcsRaw {
				dc, ok := dcRaw.(map[string]interface{})
				if !ok {
					continue
				}
				dcName, _ := dc["datacenter"].(string)
				availStr, _ := dc["availability"].(string)
				if dcName == apiDC && availStr != "unavailable" && availStr != "unknown" {
					foundAvailable = true
					if fqn, ok := av["fqn"].(string); ok {
						availableFQN = fqn
					}
					break
				}
			}
		}
		if foundAvailable {
			break
		}
	}
	if !foundAvailable {
		state.Logger.Info(fmt.Sprintf("服务器 %s 在数据中心 %s 当前无货", item.PlanCode, item.Datacenter), "purchase")
		return false
	}

	// 决定本次下单使用的硬件 options：
	// - 用户显式指定了 options → 直接用（fail-fast 由后面的 eco/options 处理）
	// - 用户没指定 → 从可用 FQN 推断 addon planCode，确保订单走"实际有货的那套配置"
	effectiveOptions := item.Options
	if len(effectiveOptions) == 0 && availableFQN != "" {
		parts := strings.Split(availableFQN, ".")
		if len(parts) > 1 {
			effectiveOptions = parts[1:] // 第一段是 base planCode，其余是 addon planCodes
			state.Logger.Info(fmt.Sprintf("用户未指定硬件选项，从可用 FQN %s 推断 addon: %v",
				availableFQN, effectiveOptions), "purchase")
		}
	}

	// 多账户:购物车 subsidiary 跟着账户走,不再读全局 cfg
	acc, _ := state.FindAccount(item.AccountID)
	subsidiary := acc.Zone
	if subsidiary == "" {
		subsidiary = "IE"
	}

	// 创建购物车
	state.Logger.Info(fmt.Sprintf("为区域 %s 创建购物车 (账户 %s)", subsidiary, acc.Name), "purchase")
	var cartResult map[string]interface{}
	if err := client.Post("/order/cart", map[string]interface{}{
		"ovhSubsidiary": subsidiary,
	}, &cartResult); err != nil {
		state.Logger.Error(fmt.Sprintf("购买 %s 时发生 OVH API 错误: %s", item.PlanCode, err.Error()), "purchase")
		recordFailure(state, item, err.Error())
		return false
	}
	cartID, _ = cartResult["cartId"].(string)
	state.Logger.Info("购物车创建成功，ID: "+cartID, "purchase")

	// 立即绑定购物车到账户 —— 对齐 OVH 官方 PHP / Python 示例的推荐顺序：
	// cart → assign → eco → configuration → options → summary → checkout。
	// 在 add item 之前 assign，OVH 后端不会出现"cart 未绑定就 checkout"的边界错误。
	state.Logger.Info("绑定购物车 "+cartID, "purchase")
	if err := client.Post("/order/cart/"+cartID+"/assign", map[string]interface{}{}, nil); err != nil {
		errMsg := err.Error()
		state.Logger.Error(fmt.Sprintf("购买 %s 时发生 OVH API 错误: %s", item.PlanCode, errMsg), "purchase")
		state.Logger.Error("错误发生时的购物车ID: "+cartID, "purchase")
		recordFailure(state, item, errMsg)
		return false
	}
	state.Logger.Info("购物车绑定成功", "purchase")

	// 添加基础商品 /eco
	state.Logger.Info(fmt.Sprintf("添加基础商品 %s 到购物车 (使用 /eco)", item.PlanCode), "purchase")
	var itemResult map[string]interface{}
	if err := client.Post("/order/cart/"+cartID+"/eco", map[string]interface{}{
		"planCode":    item.PlanCode,
		"pricingMode": "default",
		"duration":    "P1M",
		"quantity":    1,
	}, &itemResult); err != nil {
		state.Logger.Error(fmt.Sprintf("购买 %s 时发生 OVH API 错误: %s", item.PlanCode, err.Error()), "purchase")
		state.Logger.Error(fmt.Sprintf("错误发生时的购物车ID: %s", cartID), "purchase")
		recordFailure(state, item, err.Error())
		return false
	}
	if n, ok := numconv.ToInt64(itemResult["itemId"]); ok {
		itemID = n
	}
	if itemID == 0 {
		errMsg := fmt.Sprintf("无法从购物车响应中解析 itemId（响应: %v）", itemResult)
		state.Logger.Error(fmt.Sprintf("购买 %s 时发生未知错误: %s", item.PlanCode, errMsg), "purchase")
		state.Logger.Error("错误发生时的购物车ID: "+cartID, "purchase")
		recordFailure(state, item, errMsg)
		return false
	}
	state.Logger.Info(fmt.Sprintf("基础商品添加成功，项目 ID: %d", itemID), "purchase")

	// 设置必需配置
	state.Logger.Info(fmt.Sprintf("为项目 %d 设置必需配置", itemID), "purchase")
	region := ovh.RegionForDC(apiDC)

	// 与 Python 一致的顺序：dedicated_datacenter → dedicated_os → (region)
	type kv struct{ label, value string }
	configurations := []kv{
		{"dedicated_datacenter", apiDC},
		{"dedicated_os", "none_64.en"},
	}
	if region != "" {
		configurations = append(configurations, kv{"region", region})
	} else {
		state.Logger.Warn(fmt.Sprintf("无法为数据中心 %s 推断区域，可能导致配置失败", strings.ToLower(apiDC)), "purchase")
		// 对应 Python: 查 requiredConfiguration 看 region 是否必填
		var required []map[string]interface{}
		if err := client.Get(fmt.Sprintf("/order/cart/%s/item/%d/requiredConfiguration", cartID, itemID), &required); err != nil {
			state.Logger.Warn(fmt.Sprintf("获取必需配置失败或区域为必需但未确定: %s", err.Error()), "purchase")
		} else {
			for _, conf := range required {
				if label, _ := conf["label"].(string); label == "region" {
					if req, _ := conf["required"].(bool); req {
						errMsg := "必需的区域配置无法确定。"
						state.Logger.Error(fmt.Sprintf("购买 %s 时发生未知错误: %s", item.PlanCode, errMsg), "purchase")
						recordFailure(state, item, errMsg)
						return false
					}
				}
			}
		}
	}
	// Python 中每个 configuration POST 失败都会抛 OVH API 错误中断整个购买流程
	for _, c := range configurations {
		state.Logger.Info(fmt.Sprintf("配置项目 %d: 设置必需项 %s = %s", itemID, c.label, c.value), "purchase")
		if err := client.Post(fmt.Sprintf("/order/cart/%s/item/%d/configuration", cartID, itemID),
			map[string]interface{}{"label": c.label, "value": c.value}, nil); err != nil {
			errMsg := err.Error()
			state.Logger.Error(fmt.Sprintf("购买 %s 时发生 OVH API 错误: %s", item.PlanCode, errMsg), "purchase")
			state.Logger.Error(fmt.Sprintf("错误发生时的购物车ID: %s", cartID), "purchase")
			state.Logger.Error(fmt.Sprintf("错误发生时的基础商品ID: %d", itemID), "purchase")
			recordFailure(state, item, errMsg)
			return false
		}
		state.Logger.Info(fmt.Sprintf("成功设置必需项: %s = %s", c.label, c.value), "purchase")
	}

	// 硬件选项处理。effectiveOptions 已经包含了：
	//   - 用户显式 options（如果有），或
	//   - 从可用 FQN 推断的 addon planCode（用户没指定时）
	if len(effectiveOptions) > 0 {
		state.Logger.Info(fmt.Sprintf("📦 处理硬件选项（%d个）: %v", len(effectiveOptions), effectiveOptions), "purchase")
		filtered := []string{}
		for _, opt := range effectiveOptions {
			if opt == "" {
				continue
			}
			lc := strings.ToLower(opt)
			skip := false
			// 过滤掉非硬件 / 许可证类（注意 "panel" 不在过滤词里：FQN 推断的 addon
			// 不会撞这词，删了避免误伤；旧版有 "panel" 是因为前端可能塞 cpanel 选项过来）
			for _, term := range []string{"windows-server", "sql-server", "cpanel-license", "plesk-",
				"-license-", "os-", "control-panel", "license", "security"} {
				if strings.Contains(lc, term) {
					skip = true
					break
				}
			}
			if skip {
				state.Logger.Info("跳过非硬件/许可证选项: "+opt, "purchase")
				continue
			}
			filtered = append(filtered, opt)
		}
		if len(filtered) > 0 {
			state.Logger.Info(fmt.Sprintf("过滤后的硬件选项计划代码: %v", filtered), "purchase")
			var availableEcoOpts []map[string]interface{}
			q := url.Values{}
			q.Set("planCode", item.PlanCode)
			if err := client.Get(fmt.Sprintf("/order/cart/%s/eco/options?%s", cartID, q.Encode()), &availableEcoOpts); err != nil {
				// 拉 eco/options 失败 → 中止订单。否则会用基础 plan 默认存储（多半是 HDD）下到错误配置
				errMsg := fmt.Sprintf("获取 Eco 硬件选项列表失败: %s（用户指定了 %d 个选项，无法验证，已取消下单避免下到错误配置）", err.Error(), len(filtered))
				state.Logger.Error(errMsg, "purchase")
				recordFailure(state, item, errMsg)
				return false
			}
			state.Logger.Info(fmt.Sprintf("找到 %d 个可用的 Eco 硬件选项。", len(availableEcoOpts)), "purchase")
			var missing []string
			for _, wanted := range filtered {
				added := false
				for _, avail := range availableEcoOpts {
					availPC, _ := avail["planCode"].(string)
					if availPC != wanted {
						continue
					}
					state.Logger.Info(fmt.Sprintf("找到匹配的 Eco 选项: %s", availPC), "purchase")
					duration := "P1M"
					if d, ok := avail["duration"].(string); ok && d != "" {
						duration = d
					}
					pricingMode := "default"
					if pm, ok := avail["pricingMode"].(string); ok && pm != "" {
						pricingMode = pm
					}
					payload := map[string]interface{}{
						"itemId":      itemID,
						"planCode":    availPC,
						"duration":    duration,
						"pricingMode": pricingMode,
						"quantity":    1,
					}
					state.Logger.Info(fmt.Sprintf("准备添加 Eco 选项: %v", payload), "purchase")
					if err := client.Post(fmt.Sprintf("/order/cart/%s/eco/options", cartID), payload, nil); err != nil {
						// 关键选项添加失败 → 整单失败。不能静默继续 checkout，否则用户选的 NVMe / 内存
						// 会被 OVH 用基础 plan 的默认配置（通常是 HDD / 最小内存）替代，造成"选了 NVMe 却下到 HDD"
						errMsg := fmt.Sprintf("添加 Eco 选项 %s 失败: %s（已取消下单避免下到错误配置）", availPC, err.Error())
						state.Logger.Error(errMsg, "purchase")
						recordFailure(state, item, errMsg)
						return false
					}
					state.Logger.Info(fmt.Sprintf("成功添加 Eco 选项: %s 到购物车 %s", availPC, cartID), "purchase")
					added = true
					break
				}
				if !added {
					missing = append(missing, wanted)
				}
			}
			if len(missing) > 0 {
				// 用户指定了选项但 OVH 的 eco/options 列表里找不到对应 planCode → 整单失败
				// 否则会按基础 plan 的默认配置下单，用户看到"选 NVMe 下到 HDD"的现象
				errMsg := fmt.Sprintf("用户请求的硬件选项 %v 未在 OVH 可用 Eco 选项中找到（已取消下单避免下到错误配置）", missing)
				state.Logger.Error(errMsg, "purchase")
				recordFailure(state, item, errMsg)
				return false
			}
			state.Logger.Info(fmt.Sprintf("共成功添加 %d 个硬件选项。", len(filtered)), "purchase")
		}
	} else {
		state.Logger.Info("⚠️ 用户未提供任何硬件选项，将使用默认配置下单", "purchase")
	}

	// 价格摘要
	var priceInfo *types.PriceInfo
	var summary map[string]interface{}
	if err := client.Get("/order/cart/"+cartID+"/summary", &summary); err == nil && summary != nil {
		if prices, ok := summary["prices"].(map[string]interface{}); ok {
			withTaxRaw := extract(prices["withTax"])
			withoutTaxRaw := extract(prices["withoutTax"])
			taxRaw := extract(prices["tax"])
			currency := ""
			if wt, ok := prices["withTax"].(map[string]interface{}); ok {
				currency, _ = wt["currencyCode"].(string)
			}
			if currency == "" {
				currency, _ = prices["currencyCode"].(string)
			}
			if currency == "" {
				currency = "EUR"
			}
			if withTaxRaw != nil || withoutTaxRaw != nil {
				priceInfo = &types.PriceInfo{
					WithTax:      withTaxRaw,
					WithoutTax:   withoutTaxRaw,
					Tax:          taxRaw,
					CurrencyCode: currency,
				}
				wt := "<nil>"
				if withTaxRaw != nil {
					wt = fmt.Sprintf("%v", *withTaxRaw)
				}
				wot := "<nil>"
				if withoutTaxRaw != nil {
					wot = fmt.Sprintf("%v", *withoutTaxRaw)
				}
				state.Logger.Info(fmt.Sprintf("成功提取价格信息: 含税=%s %s, 不含税=%s %s",
					wt, currency, wot, currency), "purchase")
			}
		}
	}

	// Checkout
	state.Logger.Info("对购物车 "+cartID+" 执行结账", "purchase")
	var checkoutResult map[string]interface{}
	checkoutPayload := map[string]interface{}{
		"autoPayWithPreferredPaymentMethod": false,
		"waiveRetractationPeriod":           true,
	}
	if err := client.Post("/order/cart/"+cartID+"/checkout", checkoutPayload, &checkoutResult); err != nil {
		errMsg := err.Error()
		state.Logger.Error(fmt.Sprintf("购买 %s 时发生 OVH API 错误: %s", item.PlanCode, errMsg), "purchase")
		recordFailure(state, item, errMsg)
		return false
	}

	orderID := numconv.ToString(checkoutResult["orderId"])
	orderURL, _ := checkoutResult["url"].(string)

	// 查询订单详情获取过期时间
	expirationTime := ""
	if orderID != "" {
		var orderInfo map[string]interface{}
		if err := client.Get("/me/order/"+orderID, &orderInfo); err == nil {
			if ret, ok := orderInfo["retractionDate"].(string); ok && ret != "" {
				expirationTime = ret
			} else if exp, ok := orderInfo["expirationDate"].(string); ok {
				expirationTime = exp
			}
			if expirationTime != "" {
				state.Logger.Info(fmt.Sprintf("获取订单 %s 过期时间: %s", orderID, expirationTime), "purchase")
			}
		} else {
			state.Logger.Warn(fmt.Sprintf("查询订单 %s 详情失败，无法获取过期时间: %s", orderID, err.Error()), "purchase")
		}
	}

	// 更新历史记录（成功）
	recordSuccess(state, item, orderID, orderURL, expirationTime, priceInfo)

	state.Logger.Info(fmt.Sprintf("成功购买 %s 在 %s (订单ID: %s, URL: %s)",
		item.PlanCode, item.Datacenter, orderID, orderURL), "purchase")

	// 发送 Telegram 成功通知。TG token / chat id 仍然走全局 state.Config(Telegram 是平台级配置,跨账户共享)
	tgCfg := state.Config.Get()
	if tgCfg.TgToken != "" && tgCfg.TgChatID != "" {
		msg := fmt.Sprintf("🎉 OVH 服务器抢购成功！🎉\n\n服务器型号 (Plan Code): %s\n数据中心: %s\n订单 ID: %s\n订单链接: %s\n",
			item.PlanCode, item.Datacenter, orderID, orderURL)
		if len(item.Options) > 0 {
			msg += "自定义配置: " + strings.Join(item.Options, ", ") + "\n"
		}
		msg += "\n抢购任务ID: " + item.ID
		telegram.SendMessage(state, msg, nil)
		state.Logger.Info("已为订单 "+orderID+" 发送 Telegram 成功通知。", "purchase")
	} else {
		state.Logger.Info("未配置 Telegram Token 或 Chat ID，跳过成功通知发送。", "purchase")
	}
	return true
}

func extract(v interface{}) *float64 {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]interface{}); ok {
		if f, ok := numconv.ToFloat64(m["value"]); ok {
			return &f
		}
		return nil
	}
	if f, ok := numconv.ToFloat64(v); ok {
		return &f
	}
	return nil
}

func recordSuccess(state *app.State, item *types.QueueItem, orderID, orderURL, expirationTime string, priceInfo *types.PriceInfo) {
	state.HistoryMu.Lock()
	defer state.HistoryMu.Unlock()
	now := types.NowISO()

	for i := range state.History {
		if state.History[i].TaskID == item.ID {
			state.History[i].Status = "success"
			state.History[i].OrderID = orderID
			state.History[i].OrderURL = orderURL
			state.History[i].ErrorMessage = nil
			state.History[i].PurchaseTime = now
			state.History[i].AttemptCount = item.RetryCount
			state.History[i].Options = item.Options
			if expirationTime != "" {
				state.History[i].ExpirationTime = expirationTime
			}
			if priceInfo != nil {
				state.History[i].Price = priceInfo
			}
			state.Logger.Info("更新抢购历史(成功) 任务ID: "+item.ID, "purchase")
			go state.SaveHistory()
			return
		}
	}

	entry := types.PurchaseHistoryEntry{
		ID:           uuid.NewString(),
		TaskID:       item.ID,
		PlanCode:     item.PlanCode,
		Datacenter:   item.Datacenter,
		Options:      item.Options,
		Status:       "success",
		OrderID:      orderID,
		OrderURL:     orderURL,
		PurchaseTime: now,
		AttemptCount: item.RetryCount,
	}
	if expirationTime != "" {
		entry.ExpirationTime = expirationTime
	}
	if priceInfo != nil {
		entry.Price = priceInfo
	}
	state.History = append(state.History, entry)
	state.Logger.Info("创建抢购历史(成功) 任务ID: "+item.ID, "purchase")
	go state.SaveHistory()
}

func recordFailure(state *app.State, item *types.QueueItem, errMsg string) {
	state.HistoryMu.Lock()
	defer state.HistoryMu.Unlock()
	now := types.NowISO()

	for i := range state.History {
		if state.History[i].TaskID == item.ID {
			state.History[i].Status = "failed"
			state.History[i].OrderID = ""
			state.History[i].OrderURL = ""
			em := errMsg
			state.History[i].ErrorMessage = &em
			state.History[i].PurchaseTime = now
			state.History[i].AttemptCount = item.RetryCount
			state.History[i].Options = item.Options
			state.Logger.Info("更新抢购历史(失败) 任务ID: "+item.ID, "purchase")
			go state.SaveHistory()
			return
		}
	}
	em := errMsg
	entry := types.PurchaseHistoryEntry{
		ID:           uuid.NewString(),
		TaskID:       item.ID,
		PlanCode:     item.PlanCode,
		Datacenter:   item.Datacenter,
		Options:      item.Options,
		Status:       "failed",
		ErrorMessage: &em,
		PurchaseTime: now,
		AttemptCount: item.RetryCount,
	}
	state.History = append(state.History, entry)
	state.Logger.Info("创建抢购历史(失败) 任务ID: "+item.ID, "purchase")
	go state.SaveHistory()
}
