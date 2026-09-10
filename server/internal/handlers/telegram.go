package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/monitor"
	"github.com/ovh-buy/server/internal/ovh"
	"github.com/ovh-buy/server/internal/telegram"
	"github.com/ovh-buy/server/internal/types"
)

// SetTelegramWebhook POST /api/telegram/set-webhook
func SetTelegramWebhook(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			WebhookURL string `json:"webhook_url"`
		}
		_ = c.ShouldBindJSON(&body)
		if body.WebhookURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "缺少 webhook_url 参数"})
			return
		}
		ok, msg, info := telegram.SetWebhook(state, body.WebhookURL)
		if ok {
			c.JSON(http.StatusOK, gin.H{
				"success":      true,
				"message":      "Webhook 设置成功",
				"webhook_url":  msg,
				"webhook_info": info,
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "设置失败: " + msg})
	}
}

// GetTelegramWebhookInfo GET /api/telegram/get-webhook-info
func GetTelegramWebhookInfo(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, info, errMsg := telegram.GetWebhookInfo(state)
		if !ok {
			status := http.StatusBadRequest
			if strings.Contains(errMsg, "未配置") {
				status = http.StatusBadRequest
			}
			c.JSON(status, gin.H{"success": false, "error": errMsg})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "webhook_info": info})
	}
}

// legacyWarnMu / lastLegacyWarn 兼容模式告警节流，避免每条回调刷一行日志
var (
	legacyWarnMu   sync.Mutex
	lastLegacyWarn time.Time
)

func warnLegacyWebhook(state *app.State) {
	legacyWarnMu.Lock()
	due := time.Since(lastLegacyWarn) > 10*time.Minute
	if due {
		lastLegacyWarn = time.Now()
	}
	legacyWarnMu.Unlock()
	if due {
		state.Logger.Warn("Telegram webhook 处于兼容模式（未校验 secret_token）："+
			"请在设置页重新注册一次 Webhook 以启用强校验", "telegram")
	}
}

// TelegramWebhook POST /api/telegram/webhook
// 这条路由在鉴权白名单里（Telegram 不可能带 X-API-Key），所以安全完全靠下面这条链：
//
//	secret_token → body 上限 → 发送者授权 → update_id 幂等 → 频率限制 → 业务
//
// 少任何一环，知道 URL 的人就能直接伪造回调下单。
//
// 关于「兼容模式」(legacy):webhook 已注册但 secret 尚未注册时,第一环放行。
// 这时候剩下的"授权"校验的是**攻击者自己写的 JSON 字段**(chat.id),
// 而 chat_id 不是密钥 —— 设置页明文显示、日志里到处都是、截图备份里都有,
// 且猜错不限速(限流在授权之后)。所以兼容模式下等于没有鉴权。
//
// 处理办法:兼容模式照常收 Telegram 的消息(否则升级期间通知全断),
// 但**一切会花钱的动作直接拒绝** —— 下单不是"少收一条通知"能类比的损失。
func TelegramWebhook(state *app.State, mon *monitor.Monitor) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1) secret_token：证明请求真的来自 Telegram
		okSecret, legacy := telegram.ValidateWebhookSecret(state, c.GetHeader(telegram.SecretTokenHeader))
		if !okSecret {
			state.Logger.Warn("拒绝 secret_token 无效的 webhook 请求, from="+c.ClientIP(), "telegram")
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "invalid_secret_token"})
			return
		}
		if legacy {
			warnLegacyWebhook(state)
		}
		// 兼容模式标记进 context,下面的下单入口据此拒绝
		c.Set("tgLegacyMode", legacy)

		// 2) body 上限：防止超大 body 打爆内存
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, telegram.MaxTelegramBodyBytes)
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			state.Logger.Warn("webhook body 读取失败或超限: "+err.Error(), "telegram")
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"ok": false, "error": "body_too_large"})
			return
		}
		var data map[string]interface{}
		if err := json.Unmarshal(raw, &data); err != nil {
			// 非法 JSON 直接吞掉返回 200，否则 Telegram 会一直重投
			c.JSON(http.StatusOK, gin.H{"ok": true})
			return
		}

		// 3) 发送者授权 —— 必须排在幂等写入之前。
		//
		// 以前顺序是「幂等 → 授权」,于是未授权请求也会先往 telegram_updates 写一行。
		// 兼容模式下(见下面 legacy 的说明)任何人都能走到这一步,于是可以:
		//   · 无限插行撑爆磁盘(update_id 是任意 int64,清理只在 %50==0 时抽样触发)
		//   · **投毒**:预占一段连续的 update_id,之后 Telegram 真发来的同号 update
		//     被判成"重复投递"直接丢弃 —— 一键下单和文本下单静默失效,
		//     日志里只有一行「忽略重复投递」。对抢购工具来说这是最坏的失败模式。
		authorized := telegram.IsAuthorizedActor(state, actorChatID(data), actorUserID(data))
		if !authorized {
			state.Logger.Warn("拒绝未授权的 Telegram 请求(未写入幂等表)", "telegram")
			if cb, ok := data["callback_query"].(map[string]interface{}); ok {
				telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "无权限", true)
			}
			c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "unauthorized_actor"})
			return
		}

		// 4) update_id 幂等：Telegram 没收到 200 就会重投同一条 update，
		//    没有这一步，一次网络抖动就会重复下单。
		if updateID := parseUpdateID(data["update_id"]); updateID > 0 && state.DB != nil {
			claimed, err := state.DB.TryClaimTelegramUpdate(updateID)
			if err != nil {
				state.Logger.Warn("update_id 幂等写入失败: "+err.Error(), "telegram")
			} else if !claimed {
				state.Logger.Info(fmt.Sprintf("忽略重复投递的 update_id=%d", updateID), "telegram")
				if cb, ok := data["callback_query"].(map[string]interface{}); ok {
					telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "该操作已处理", false)
				}
				c.JSON(http.StatusOK, gin.H{"ok": true, "duplicate": true})
				return
			}
			// 顺带清理超过保留期的旧记录（抽样触发，避免每条都删一次）
			if updateID%50 == 0 {
				before := float64(time.Now().Add(-time.Duration(telegram.UpdateIDRetentionDays) * 24 * time.Hour).Unix())
				if n, err := state.DB.CleanupTelegramUpdates(before); err == nil && n > 0 {
					state.Logger.Debug(fmt.Sprintf("已清理 %d 条过期 update_id", n), "telegram")
				}
			}
		}

		// 处理 callback_query（一键下单按钮）
		if cb, ok := data["callback_query"].(map[string]interface{}); ok {
			handleTelegramCallback(state, mon, c, cb)
			return
		}

		// 处理普通消息（文本下单）
		if msg, ok := data["message"].(map[string]interface{}); ok {
			handleTelegramMessage(state, c, msg)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// refuseInLegacyMode 兼容模式下拒绝花钱的动作。
//
// 兼容模式(secret 未注册)时唯一的"鉴权"是请求体里的 chat_id,
// 而那不是密钥。收通知可以将就,下单不行 —— 一单就是真实订单,
// 占库存、已弃 14 天撤销期。
func refuseInLegacyMode(state *app.State, c *gin.Context) bool {
	if legacy, _ := c.Get("tgLegacyMode"); legacy == true {
		state.Logger.Error("兼容模式(webhook secret 未注册)下拒绝执行下单动作。"+
			"请到设置页点一次「注册 Webhook」启用强校验", "telegram")
		c.JSON(http.StatusForbidden, gin.H{
			"ok":    false,
			"error": "legacy_mode_order_refused",
		})
		return true
	}
	return false
}

// actorChatID / actorUserID 从 update 里取出发送者标识,
// callback_query 和 message 两种形态各取各的位置。
// 提前取是为了把授权判断挪到幂等写入之前(见 webhook handler 里的说明)。
func actorChatID(data map[string]interface{}) interface{} {
	if cb, ok := data["callback_query"].(map[string]interface{}); ok {
		msg, _ := cb["message"].(map[string]interface{})
		return getNested(msg, "chat", "id")
	}
	if msg, ok := data["message"].(map[string]interface{}); ok {
		return getNested(msg, "chat", "id")
	}
	return nil
}

func actorUserID(data map[string]interface{}) interface{} {
	if cb, ok := data["callback_query"].(map[string]interface{}); ok {
		from, _ := cb["from"].(map[string]interface{})
		return from["id"]
	}
	if msg, ok := data["message"].(map[string]interface{}); ok {
		from, _ := msg["from"].(map[string]interface{})
		return from["id"]
	}
	return nil
}

// showTelegramAccountChoices 将同一机房的按钮切换为可下单账户列表；此操作不消费订单按钮。
func showTelegramAccountChoices(state *app.State, mon *monitor.Monitor, cb map[string]interface{}, buttonID string) error {
	if state.DB == nil {
		return fmt.Errorf("数据库不可用")
	}
	row, exists, err := state.DB.GetTelegramButton(buttonID)
	if err != nil {
		return fmt.Errorf("读取按钮失败: %w", err)
	}
	if !exists || row.UsedAt > 0 || time.Since(time.Unix(int64(row.CreatedAt), 0)) > telegram.ButtonTTL {
		return fmt.Errorf("按钮已失效")
	}
	if row.AccountID == "" {
		return fmt.Errorf("按钮缺少账户归属")
	}
	accounts := mon.CompatibleOrderAccounts(row.AccountID)
	if len(accounts) < 2 {
		return fmt.Errorf("没有两个同区域可用账户")
	}
	message, _ := cb["message"].(map[string]interface{})
	chatID := getNested(message, "chat", "id")
	messageID, _ := getNumOrFloat(message["message_id"])
	type button struct {
		Text         string `json:"text"`
		CallbackData string `json:"callback_data"`
	}
	keyboard := make([][]button, 0, (len(accounts)+1)/2)
	line := make([]button, 0, 2)
	options := db.ParseTelegramButtonOptions(row.Options)
	for _, account := range accounts {
		childID := uuid.NewString()
		if err := state.DB.UpsertTelegramButtonForAccount(childID, account.ID, row.PlanCode, row.Datacenter, options, nil, row.CreatedAt); err != nil {
			state.Logger.Warn("创建 Telegram 账户选择按钮失败: "+err.Error(), "telegram")
			continue
		}
		callback, _ := json.Marshal(map[string]string{"a": "add_to_queue", "u": childID})
		zone := strings.ToUpper(strings.TrimSpace(account.Zone))
		if zone == "" {
			zone = ovh.DefaultSubsidiaryForEndpoint(account.Endpoint)
		}
		line = append(line, button{Text: account.Name + "（" + ovh.SubsidiaryRegion(zone) + "）", CallbackData: string(callback)})
		if len(line) == 2 {
			keyboard = append(keyboard, line)
			line = make([]button, 0, 2)
		}
	}
	if len(line) > 0 {
		keyboard = append(keyboard, line)
	}
	if len(keyboard) == 0 {
		return fmt.Errorf("创建账户选择按钮失败")
	}
	// Telegram callback_data 的硬上限是 64 bytes。UUID 已占 36 bytes，
	// action 必须使用短码；旧的 back_to_datacenters 会序列化为 70 bytes 并被 Telegram 拒绝。
	backCallback, _ := json.Marshal(map[string]string{"a": "back", "u": buttonID})
	keyboard = append(keyboard, []button{{Text: "‹ 返回机房选择", CallbackData: string(backCallback)}})
	if err := telegram.EditMessageReplyMarkup(state, chatID, int64(messageID), map[string]interface{}{"inline_keyboard": keyboard}); err != nil {
		return err
	}
	return nil
}

// restoreTelegramDatacenterChoices 恢复同一条补货通知中的机房选择按钮。
func restoreTelegramDatacenterChoices(state *app.State, mon *monitor.Monitor, cb map[string]interface{}, buttonID string) error {
	if state.DB == nil {
		return fmt.Errorf("数据库不可用")
	}
	parent, exists, err := state.DB.GetTelegramButton(buttonID)
	if err != nil {
		return fmt.Errorf("读取原机房按钮失败: %w", err)
	}
	if !exists || parent.UsedAt > 0 || time.Since(time.Unix(int64(parent.CreatedAt), 0)) > telegram.ButtonTTL {
		return fmt.Errorf("原机房按钮已失效")
	}
	var config map[string]interface{}
	_ = json.Unmarshal([]byte(parent.ConfigInfo), &config)
	menuID, _ := config["telegram_menu_id"].(string)
	buttons := []db.TelegramButtonRow{parent}
	if menuID != "" {
		if rows, err := state.DB.ListTelegramButtonsByMenuID(menuID); err == nil && len(rows) > 0 {
			buttons = rows
		} else if err != nil {
			return err
		}
	}

	type button struct {
		Text         string `json:"text"`
		CallbackData string `json:"callback_data"`
	}
	keyboard := make([][]button, 0, (len(buttons)+1)/2)
	line := make([]button, 0, 2)
	for _, item := range buttons {
		if item.UsedAt > 0 || time.Since(time.Unix(int64(item.CreatedAt), 0)) > telegram.ButtonTTL {
			continue
		}
		action := "add_to_queue"
		label := monitor.DisplayDatacenterShortName(item.Datacenter) + " 一键下单"
		if accounts := mon.CompatibleOrderAccounts(item.AccountID); len(accounts) > 1 {
			action = "choose"
			label = monitor.DisplayDatacenterShortName(item.Datacenter) + " 选择账户下单"
		}
		callback, _ := json.Marshal(map[string]string{"a": action, "u": item.ID})
		line = append(line, button{Text: label, CallbackData: string(callback)})
		if len(line) == 2 {
			keyboard = append(keyboard, line)
			line = make([]button, 0, 2)
		}
	}
	if len(line) > 0 {
		keyboard = append(keyboard, line)
	}
	if len(keyboard) == 0 {
		return fmt.Errorf("没有仍有效的机房按钮")
	}
	message, _ := cb["message"].(map[string]interface{})
	messageID, _ := getNumOrFloat(message["message_id"])
	return telegram.EditMessageReplyMarkup(state, getNested(message, "chat", "id"), int64(messageID), map[string]interface{}{"inline_keyboard": keyboard})
}

// handleTelegramCallback 处理「一键下单」按钮回调。
func handleTelegramCallback(state *app.State, mon *monitor.Monitor, c *gin.Context, cb map[string]interface{}) {
	if legacy, _ := c.Get("tgLegacyMode"); legacy == true {
		// callback 必须应答，否则 Telegram 只会一直显示 loading，用户无法得知
		// 当前 webhook 未启用 secret_token，所有会创建订单的动作都按 fail-closed 拒绝。
		state.Logger.Error("兼容模式(webhook secret 未注册)下拒绝执行 Telegram 下单动作", "telegram")
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "Webhook 安全校验未启用，请在设置页重新注册 Webhook", true)
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "legacy_mode_order_refused"})
		return
	}
	cbData, _ := cb["data"].(string)
	message, _ := cb["message"].(map[string]interface{})
	chatID := getNested(message, "chat", "id")
	messageID, _ := getNumOrFloat(message["message_id"])
	fromUser, _ := cb["from"].(map[string]interface{})
	userID, _ := getNumOrFloat(fromUser["id"])
	state.Logger.Info(fmt.Sprintf("收到Telegram回调: user_id=%v, callback_data=%s...", userID, truncate(cbData, 50)), "telegram")

	// 4) 发送者授权：只有配置的那个 chat 能下单
	if !telegram.IsAuthorizedActor(state, chatID, fromUser["id"]) {
		state.Logger.Warn(fmt.Sprintf("拒绝未授权的 Telegram 回调: chat_id=%v, user_id=%v", chatID, userID), "telegram")
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "无权限", true)
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "unauthorized_actor"})
		return
	}

	// 5) 频率限制
	rateKey := telegram.ChatIDString(chatID)
	if rateKey == "" {
		rateKey = telegram.ChatIDString(fromUser["id"])
	}
	if !telegram.AllowRate(rateKey) {
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "操作过于频繁，请稍后再试", true)
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "rate_limited"})
		return
	}

	callbackObj, ok := decodeCallbackData(state, cbData)
	if !ok {
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "按钮数据异常，请等待新的通知", true)
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Invalid callback data format"})
		return
	}

	action := strOr(callbackObj, "a", "action")
	buttonID := strOr(callbackObj, "u", "uuid")
	if action == "favorite" {
		planCode := strings.TrimSpace(strOr(callbackObj, "p", "planCode"))
		isFavorite, err := state.DB.IsServerFavorite(planCode)
		if err != nil || !isFavorite {
			telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "收藏已不存在，请重新发送 /buy", true)
			c.JSON(http.StatusGone, gin.H{"ok": false, "error": "favorite_not_found"})
			return
		}
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "请选择下单账户", false)
		if !sendTextOrderAccountChoices(state, chatID, int64(messageID), &telegram.OrderInfo{PlanCode: planCode, Quantity: 1}) {
			telegram.SendReply(state, chatID, "❌ 无法生成账户选择按钮，请确认至少配置了一个 OVH 账户。", int64(messageID))
			c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "account_selection_unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	if action == "text" {
		if state.DB == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "database_unavailable"})
			return
		}
		row, claimed, err := state.DB.ClaimTelegramButton(buttonID)
		if err != nil || !claimed || time.Since(time.Unix(int64(row.CreatedAt), 0)) > telegram.ButtonTTL {
			telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "按钮已失效，请重新发送 /buy", true)
			c.JSON(http.StatusGone, gin.H{"ok": false, "error": "text_order_button_unavailable"})
			return
		}
		var meta struct {
			Quantity int `json:"quantity"`
		}
		_ = json.Unmarshal([]byte(row.ConfigInfo), &meta)
		result := telegram.ProcessOrder(state, row.AccountID, row.PlanCode, row.Datacenter, meta.Quantity, db.ParseTelegramButtonOptions(row.Options))
		message, _ := cb["message"].(map[string]interface{})
		chatID := getNested(message, "chat", "id")
		messageID, _ := getNumOrFloat(message["message_id"])
		if !result.Success {
			_ = state.DB.UnclaimTelegramButton(buttonID)
			telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "创建任务失败", true)
			telegram.SendReply(state, chatID, "❌ 下单失败\n\n"+result.Message, int64(messageID))
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "text_order_failed"})
			return
		}
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "已创建抢购任务", false)
		telegram.SendReply(state, chatID, fmt.Sprintf("✅ 已用所选账户创建 %d/%d 个抢购任务。", result.CreatedOrders, result.TotalOrders), int64(messageID))
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	if action == "back" {
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "正在返回机房选择", false)
		if err := restoreTelegramDatacenterChoices(state, mon, cb, buttonID); err != nil {
			state.Logger.Warn("恢复 Telegram 机房选择失败: "+err.Error(), "telegram")
			telegram.SendReply(state, chatID, "❌ 无法恢复机房选择："+err.Error()+"。请等待下一条有货通知后重试。", int64(messageID))
			c.JSON(http.StatusGone, gin.H{"ok": false, "error": "datacenter_selection_unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	if action == "choose" {
		// 先应答停止 Telegram 的 loading；消息编辑失败也会给用户可见提示。
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "正在加载账户", false)
		if err := showTelegramAccountChoices(state, mon, cb, buttonID); err != nil {
			state.Logger.Warn("打开 Telegram 账户选择失败: "+err.Error(), "telegram")
			telegram.SendReply(state, chatID, "❌ 无法打开账户选择："+err.Error()+"。请等待下一条有货通知后重试。", int64(messageID))
			c.JSON(http.StatusGone, gin.H{"ok": false, "error": "account_selection_unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	if action != "add_to_queue" {
		state.Logger.Warn("未知的action: "+action, "telegram")
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Unknown action: " + action})
		return
	}

	// 没有按钮 id 就不许下单。
	//
	// 以前 claim / 过期 / 重放三道判定全在 if buttonID != "" 里面 ——
	// callback_data 里不带 u,planCode/机房/配置就直接取 JSON 的 p/d/o,
	// telegram_order_buttons 一次都不查。于是 README 承诺的
	// 「同一个按钮只能下单一次、超 24h 作废」对任何能构造 body 的人都不成立:
	// 同一组 {"a":"add_to_queue","p":"...","d":"..."} 换个 update_id 就能重放,
	// 想下几单下几单。
	//
	// 正常 TG 客户端改不了 callback_data,所以这条要配合兼容模式或 secret 泄漏;
	// 但既然一次性 nonce 是我们唯一的防重放,就不该留一条绕过它的路。
	if buttonID == "" {
		state.Logger.Warn("拒绝没有按钮 id 的下单回调(无法防重放)", "telegram")
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "按钮已失效,请等下一条通知", true)
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "missing_button_id"})
		return
	}
	planCode := strOr(callbackObj, "p", "planCode")
	dc := strOr(callbackObj, "d", "datacenter")
	// btnAccountID:发通知时记下的「触发订阅所用账户」。planCode 是分区的,
	// 用它下单才不会把欧区机型落到美区账户上。空 = 老按钮/无账户维度 → 退回默认账户。
	btnAccountID := ""
	var options []string
	if optsRaw, ok := callbackObj["o"]; ok {
		options = toStringSlice(optsRaw)
	} else if optsRaw, ok := callbackObj["options"]; ok {
		options = toStringSlice(optsRaw)
	}

	claimed := false // 是否占用了 DB 里的一次性按钮（失败要回滚）
	if buttonID != "" {
		row, ok, err := state.DB.ClaimTelegramButton(buttonID)
		if err != nil {
			state.Logger.Error("认领一键下单按钮失败: "+err.Error(), "telegram")
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "claim_button_failed"})
			return
		}
		if ok {
			// 按钮过期（默认 24h）→ 归还并拒绝，避免用很久以前的库存信息下单
			if time.Since(time.Unix(int64(row.CreatedAt), 0)) > telegram.ButtonTTL {
				_ = state.DB.UnclaimTelegramButton(buttonID)
				state.Logger.Warn("一键下单按钮已过期: "+buttonID, "telegram")
				telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "该按钮已过期，请等待新的上架通知", true)
				c.JSON(http.StatusGone, gin.H{"ok": false, "error": "button_expired"})
				return
			}
			claimed = true
			planCode = row.PlanCode
			dc = row.Datacenter
			options = db.ParseTelegramButtonOptions(row.Options)
			btnAccountID = strings.TrimSpace(row.AccountID)
			state.Logger.Info(fmt.Sprintf("✅ 按钮已认领: id=%s, %s@%s, options=%v, account=%s",
				buttonID, planCode, dc, options, btnAccountID), "telegram")
		} else {
			// 认领失败：要么已经点过（重放），要么这条按钮根本不存在
			if _, exists, _ := state.DB.GetTelegramButton(buttonID); exists {
				state.Logger.Warn("一键下单按钮已被使用过，拒绝重复下单: "+buttonID, "telegram")
				telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "该按钮已使用过", true)
				c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "button_already_used"})
				return
			}
			// 库里没有 → 退回内存缓存（升级前发出、只存在内存里的老按钮）
			if cached := mon.MessageUUIDCacheLookup(buttonID); cached != nil {
				planCode = cached.PlanCode
				dc = cached.Datacenter
				options = cached.Options
				state.Logger.Info("从内存缓存恢复按钮配置（旧按钮）: "+buttonID, "telegram")
			} else {
				// 库里没有、内存缓存也没有 → 拒绝。
				// 以前这里只打一行 Warn 就继续往下走,照样拿 JSON 里的 p/d 下单 ——
				// 按钮行被 DeleteExpiredTelegramButtons 清掉、换库、换机器都会落进
				// 这个分支,等于防重放形同虚设。
				state.Logger.Warn("按钮 UUID 不存在,拒绝下单: "+buttonID, "telegram")
				telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "按钮已失效,请等下一条通知", true)
				c.JSON(http.StatusGone, gin.H{"ok": false, "error": "button_not_found"})
				return
			}
		}
	}

	if planCode == "" || dc == "" {
		if claimed {
			_ = state.DB.UnclaimTelegramButton(buttonID)
		}
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Missing planCode or datacenter"})
		return
	}
	if len(options) == 0 {
		if cachedOpts := mon.OptionsCacheLookup(planCode + "|" + dc); len(cachedOpts) > 0 {
			options = cachedOpts
			state.Logger.Info("✅ 从缓存恢复 options: "+planCode+"|"+dc, "telegram")
		}
	}

	// 入队账户:使用按钮里记下的显式账户；它和查到这批库存的区域一致。
	// 仅老按钮没有账户维度时才回退默认账户；已选账户不存在则拒绝，绝不切换账户。
	// 留空不入队 —— 会让下游 history / 账户 chip 全都对不上号。
	//
	// 一个账户都没有时不能"留空照样入队":这一单永远下不出去，
	// 用户却收到一句"已添加到抢购队列"，等于把失败藏到几十次重试之后。
	acc, hasAcc := state.FindAccount(btnAccountID)
	if !hasAcc && btnAccountID != "" {
		// 账户选择是用户的显式决定；账户已删除时绝不能偷偷换到默认账户，
		// 否则会绕开该账户的专属代理与出口 IP 约束，甚至在另一个账户产生真实订单。
		if claimed {
			_ = state.DB.UnclaimTelegramButton(buttonID)
		}
		state.Logger.Warn("Telegram 一键下单被拒绝：按钮指定的账户已不存在: "+btnAccountID, "telegram")
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "所选账户已不存在", true)
		telegram.SendReply(state, chatID, "❌ 所选下单账户已被删除，未创建抢购任务。请等待下一条有货通知后重新选择账户。", int64(messageID))
		c.JSON(http.StatusGone, gin.H{"ok": false, "error": "selected_account_not_found"})
		return
	}
	if !hasAcc {
		if claimed {
			_ = state.DB.UnclaimTelegramButton(buttonID)
		}
		state.Logger.Warn("Telegram 一键下单被拒绝：系统里没有任何 OVH 账户", "telegram")
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "未配置 OVH 账户", true)
		telegram.SendReply(state, chatID, "❌ 未配置任何 OVH 账户，无法下单。请先在控制台添加账户。", int64(messageID))
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "no_account"})
		return
	}
	accountID := acc.ID
	// planCode 是分区的：欧区 planCode 落到美区账户上，OVH 只会返回空库存而不报错。
	// 按钮带账户时这里就是发通知时那个订阅的账户；退回默认账户的情况仍可能选错，
	// 所以把"这一单会用哪个账户、哪个子公司/大区"明确写进日志和回复，
	// 让用户在真正扣款前就能看出账户选错了。
	accSub := strings.ToUpper(strings.TrimSpace(acc.Zone))
	if accSub == "" {
		accSub = ovh.DefaultSubsidiaryForEndpoint(acc.Endpoint)
	}
	accLabel := fmt.Sprintf("%s（子公司 %s / %s 区）", acc.Name, accSub, ovh.SubsidiaryRegion(accSub))
	if btnAccountID == "" {
		// 明示"这是兜底账户"而不是通知里那个订阅账户，用户才知道要核对
		accLabel += "［默认账户］"
	}
	item := types.QueueItem{
		ID:            uuid.NewString(),
		AccountID:     accountID,
		PlanCode:      planCode,
		Datacenter:    dc,
		Options:       options,
		Status:        "running",
		CreatedAt:     types.NowISO(),
		UpdatedAt:     types.NowISO(),
		RetryInterval: 30,
		RetryCount:    0,
		LastCheckTime: 0,
		FromTelegram:  true,
	}
	state.QueueMu.Lock()
	state.Queue = append(state.Queue, item)
	state.QueueMu.Unlock()
	if err := state.SaveQueue(); err != nil {
		// 落库失败 → 把内存里这条也撤掉,再归还按钮。
		//
		// 以前只归还按钮、不撤内存:任务还在队列里跑着,而按钮又可以再按一次 ——
		// 用户按第二次就是同一台机器的第二条任务,抢到就是两笔真实订单、两次扣款。
		// 而且下面照样回"✅ 已添加到抢购队列",用户完全不知道出过事。
		// 撤销 + 归还按钮 + 明确告知,三件事必须一起做,重试才是安全的。
		state.Logger.Error("Telegram 入队后保存失败: "+err.Error(), "telegram")
		state.QueueMu.Lock()
		for i := range state.Queue {
			if state.Queue[i].ID == item.ID {
				state.Queue = append(state.Queue[:i], state.Queue[i+1:]...)
				break
			}
		}
		state.QueueMu.Unlock()
		if claimed {
			_ = state.DB.UnclaimTelegramButton(buttonID)
		}
		telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "入队失败，请重试", true)
		telegram.SendReply(state, chatID,
			"⚠️ 入队失败：任务没能写进数据库，已撤销，未开始抢购。\n原因: "+err.Error()+"\n\n可以再按一次按钮重试。",
			int64(messageID))
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "save queue: " + err.Error()})
		return
	}

	optsStr := strings.Join(options, ", ")
	if optsStr == "" {
		optsStr = "无（默认配置）"
	}
	state.Logger.Info(fmt.Sprintf("Telegram用户 %v 通过按钮添加到队列: %s@%s, 配置选项: %s, 账户: %s",
		userID, planCode, dc, optsStr, accLabel), "telegram")
	confirmMsg := fmt.Sprintf("✅ 已添加到抢购队列！\n\n型号: %s\n机房: %s\n配置: %s\n账户: %s\n\n系统将自动尝试下单。",
		planCode, strings.ToUpper(dc), optsStr, accLabel)
	telegram.AnswerCallback(state, fmt.Sprintf("%v", cb["id"]), "已添加到队列！", false)
	telegram.SendReply(state, chatID, confirmMsg, int64(messageID))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// sendTextOrderAccountChoices 为文本 /buy 创建明确账户绑定的一次性按钮。
func sendTextOrderAccountChoices(state *app.State, chatID interface{}, messageID int64, order *telegram.OrderInfo) bool {
	if state.DB == nil || order == nil {
		return false
	}
	state.AccountsMu.RLock()
	accounts := append([]types.OVHAccount(nil), state.Accounts...)
	state.AccountsMu.RUnlock()
	if len(accounts) == 0 {
		return false
	}
	type button struct {
		Text         string `json:"text"`
		CallbackData string `json:"callback_data"`
	}
	keyboard := make([][]button, 0, (len(accounts)+1)/2)
	line := make([]button, 0, 2)
	for _, account := range accounts {
		id := uuid.NewString()
		if err := state.DB.UpsertTelegramButtonForAccount(id, account.ID, order.PlanCode, order.Datacenter, order.Options, map[string]interface{}{"quantity": order.Quantity}, float64(time.Now().Unix())); err != nil {
			continue
		}
		callback, _ := json.Marshal(map[string]string{"a": "text", "u": id})
		zone := strings.ToUpper(strings.TrimSpace(account.Zone))
		if zone == "" {
			zone = ovh.DefaultSubsidiaryForEndpoint(account.Endpoint)
		}
		line = append(line, button{Text: account.Name + "（" + ovh.SubsidiaryRegion(zone) + "）", CallbackData: string(callback)})
		if len(line) == 2 {
			keyboard = append(keyboard, line)
			line = make([]button, 0, 2)
		}
	}
	if len(line) > 0 {
		keyboard = append(keyboard, line)
	}
	if len(keyboard) == 0 {
		return false
	}
	return telegram.SendReplyWithMarkup(state, chatID, "请选择用于创建抢购任务的账户：", messageID, map[string]interface{}{"inline_keyboard": keyboard})
}

// sendFavoriteOrderMenu 展示全局关注型号；选择后沿用现有的账户选择与一次性下单按钮。
func sendFavoriteOrderMenu(state *app.State, chatID interface{}, messageID int64) bool {
	if state.DB == nil {
		return false
	}
	favorites, err := state.DB.ListServerFavorites()
	if err != nil || len(favorites) == 0 {
		return false
	}
	type button struct {
		Text         string `json:"text"`
		CallbackData string `json:"callback_data"`
	}
	keyboard := make([][]button, 0, (len(favorites)+1)/2)
	line := make([]button, 0, 2)
	for _, favorite := range favorites {
		label := strings.TrimSpace(favorite.DisplayName)
		if label == "" {
			label = favorite.PlanCode
		}
		callback, _ := json.Marshal(map[string]string{"a": "favorite", "p": favorite.PlanCode})
		line = append(line, button{Text: label, CallbackData: string(callback)})
		if len(line) == 2 {
			keyboard = append(keyboard, line)
			line = make([]button, 0, 2)
		}
	}
	if len(line) > 0 {
		keyboard = append(keyboard, line)
	}
	return telegram.SendReplyWithMarkup(state, chatID, "选择关注型号：\n\n下一步请选择下单账户。未指定配置时会沿用该型号当前可下单的配置与机房；任务过多会被系统拒绝。", messageID, map[string]interface{}{"inline_keyboard": keyboard})
}

// handleTelegramMessage 处理文本下单消息。
func handleTelegramMessage(state *app.State, c *gin.Context, msg map[string]interface{}) {
	if refuseInLegacyMode(state, c) {
		return
	}
	text, _ := msg["text"].(string)
	text = strings.TrimSpace(text)
	chatID := getNested(msg, "chat", "id")
	messageID, _ := getNumOrFloat(msg["message_id"])
	fromUser, _ := msg["from"].(map[string]interface{})
	userID, _ := getNumOrFloat(fromUser["id"])
	username, _ := fromUser["username"].(string)
	if username == "" {
		username = "未知用户"
	}
	state.Logger.Info(fmt.Sprintf("收到Telegram普通消息: user_id=%v, username=%s, text=%s",
		userID, username, truncate(text, 100)), "telegram")

	// 发送者授权
	if !telegram.IsAuthorizedActor(state, chatID, fromUser["id"]) {
		state.Logger.Warn(fmt.Sprintf("拒绝未授权的 Telegram 消息: chat_id=%v, user_id=%v", chatID, userID), "telegram")
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "unauthorized_actor"})
		return
	}

	// 频率限制
	rateKey := telegram.ChatIDString(chatID)
	if rateKey == "" {
		rateKey = telegram.ChatIDString(fromUser["id"])
	}
	if !telegram.AllowRate(rateKey) {
		telegram.SendReply(state, chatID, "⚠️ 操作过于频繁，请稍后再试", int64(messageID))
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "rate_limited"})
		return
	}

	if strings.EqualFold(text, "/buy") || strings.HasPrefix(strings.ToLower(text), "/buy@") {
		if sendFavoriteOrderMenu(state, chatID, int64(messageID)) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
			return
		}
		telegram.SendReply(state, chatID, "暂无关注型号。请先在网页「服务器列表」点击星标关注型号。", int64(messageID))
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}

	orderInfo := telegram.ParseOrderMessage(text)
	if orderInfo == nil {
		state.Logger.Debug("消息不是下单格式，忽略", "telegram")
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	state.Logger.Info(fmt.Sprintf("解析下单消息: planCode=%s, datacenter=%s, quantity=%d, options=%v",
		orderInfo.PlanCode, orderInfo.Datacenter, orderInfo.Quantity, orderInfo.Options), "telegram")
	if sendTextOrderAccountChoices(state, chatID, int64(messageID), orderInfo) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	result := telegram.OrderResult{Success: false, Message: "无法生成账户选择按钮，请确认至少配置了一个 OVH 账户后重试"}
	var reply string
	if result.Success {
		dcText := "所有可用机房"
		if orderInfo.Datacenter != "" {
			dcText = strings.ToUpper(orderInfo.Datacenter)
		}
		optsText := "所有可用配置"
		if len(orderInfo.Options) > 0 {
			optsText = strings.Join(orderInfo.Options, ", ")
		}
		// 这里只是把任务加进抢购队列,还没有真的下单 ——
		// 措辞不能写"下单成功",那会让用户以为已经买到了。
		// 另外不指定机房时任务数 = 配置数 × 有货机房数 × 数量,
		// 可能远超用户直觉,必须把总数醒目地摆出来。
		reply = fmt.Sprintf("📥 已创建 %d/%d 个抢购任务\n\n型号: %s\n机房: %s\n数量: %d\n配置: %s\n\n"+
			"系统将自动尝试下单;每个任务下单成功后会单独通知(注意:下单成功≠已付款)。\n"+
			"不想跑这么多任务就到「抢购队列」里删。",
			result.CreatedOrders, result.TotalOrders, orderInfo.PlanCode, dcText, orderInfo.Quantity, optsText)
	} else {
		reply = "❌ 下单失败\n\n" + result.Message
	}
	telegram.SendReply(state, chatID, reply, int64(messageID))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// parseUpdateID 从 update JSON 里取 update_id（JSON 数字解出来可能是 float64 / json.Number）
func parseUpdateID(v interface{}) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case int64:
		return x
	}
	return 0
}

// decodeCallbackData 解析 callback_data，支持 "b64:" 前缀的 base64 包装和裸 JSON 两种。
func decodeCallbackData(state *app.State, cbData string) (map[string]interface{}, bool) {
	payload := []byte(cbData)
	if strings.HasPrefix(cbData, "b64:") {
		base64Part := cbData[4:]
		if missing := len(base64Part) % 4; missing != 0 {
			base64Part += strings.Repeat("=", 4-missing)
		}
		decoded, err := base64.StdEncoding.DecodeString(base64Part)
		if err != nil {
			state.Logger.Warn(fmt.Sprintf("base64解码失败（可能是数据被截断）: %s, base64_len=%d", err.Error(), len(cbData[4:])), "telegram")
			return nil, false
		}
		payload = decoded
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(payload, &obj); err != nil {
		state.Logger.Error("解析callback_data JSON失败: "+err.Error()+", data="+truncate(cbData, 100), "telegram")
		return nil, false
	}
	return obj, true
}

func getNested(m map[string]interface{}, keys ...string) interface{} {
	var cur interface{} = m
	for _, k := range keys {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

func getNumOrFloat(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func strOr(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func toStringSlice(v interface{}) []string {
	out := []string{}
	switch x := v.(type) {
	case []interface{}:
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, x...)
	}
	return out
}
