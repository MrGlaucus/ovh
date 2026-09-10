package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/proxy"
)

// VerifyConfig 检查 Telegram 是否可用:Token / Chat ID 是否填写 + bot 是否能 getMe + chat 是否可访问。
// 用于 AddSubscription 等"必须 TG 有效"的强制校验。
// 返回 (ok, 失败原因)。所有失败原因都是面向终端用户的中文短句。

// tokenRe 匹配 Telegram API URL 里的 bot token 段。
var tokenRe = regexp.MustCompile(`/bot[0-9]+:[A-Za-z0-9_-]+`)

// scrub 抹掉字符串里的 Bot Token。
//
// Go 的 *url.Error 文本长这样:
//
//	Post "https://api.telegram.org/bot<完整TOKEN>/sendMessage": dial tcp ...
//
// 而这类部署连 api.telegram.org 本来就常失败。以前这些 scrub(err.Error()) 被直接
// 拼进日志 —— 明文 Token 落进 logs/ 并通过 GET /api/logs 显示在前端;
// 有几处还把同一串当 error 返回,出现在「添加订阅」的报错和 webhook 信息接口里。
//
// Token 能冒充你发通知、甚至通过 webhook 触发下单,config.go 专门为它做了加密落库,
// 这条路等于把那份保护绕过去了。
func scrub(s string) string {
	return tokenRe.ReplaceAllString(s, "/bot***")
}

func VerifyConfig(state *app.State) (bool, string) {
	cfg := state.Config.Get()
	token := strings.TrimSpace(cfg.TgToken)
	chatID := strings.TrimSpace(cfg.TgChatID)
	if token == "" {
		return false, "未配置 Telegram Bot Token"
	}
	if chatID == "" {
		return false, "未配置 Telegram Chat ID"
	}
	client := proxy.HTTPClient(10 * time.Second)

	// 1) getMe 验 token
	resp, err := client.Get("https://api.telegram.org/bot" + token + "/getMe")
	if err != nil {
		return false, "无法连接 Telegram API: " + scrub(err.Error())
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var r1 map[string]interface{}
	_ = json.Unmarshal(body, &r1)
	if ok, _ := r1["ok"].(bool); !ok {
		desc, _ := r1["description"].(string)
		if desc == "" {
			desc = "未知错误"
		}
		return false, "Telegram Token 无效: " + desc
	}

	// 2) getChat 验 chat_id (bot 是否能访问这个 chat)
	resp2, err := client.Get("https://api.telegram.org/bot" + token + "/getChat?chat_id=" + chatID)
	if err != nil {
		return false, "无法连接 Telegram API: " + scrub(err.Error())
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	var r2 map[string]interface{}
	_ = json.Unmarshal(body2, &r2)
	if ok, _ := r2["ok"].(bool); !ok {
		desc, _ := r2["description"].(string)
		if desc == "" {
			desc = "未知错误"
		}
		return false, "Telegram Chat ID 不可达: " + desc + " (请先给 bot 发一条消息)"
	}
	return true, ""
}

// MessageRef 是 Telegram 成功发送后返回的消息定位信息。
// 监控用它在下架时准确编辑同一条上架通知。
type MessageRef struct {
	ChatID    string
	MessageID int64
}

func SendMessage(state *app.State, message string, replyMarkup map[string]interface{}) bool {
	_, err := SendMessageWithRef(state, message, replyMarkup)
	return err == nil
}

// SendMessageWithRef 发送 HTML 安全的文本并返回 Telegram 消息 ID。
// 调用方传入纯文本；本函数统一转义，避免服务器名称等动态内容被当成 HTML。
func SendMessageWithRef(state *app.State, message string, replyMarkup map[string]interface{}) (MessageRef, error) {
	cfg := state.Config.Get()
	if cfg.TgToken == "" {
		return MessageRef{}, fmt.Errorf("未配置 Telegram Bot Token")
	}
	if cfg.TgChatID == "" {
		return MessageRef{}, fmt.Errorf("未配置 Telegram Chat ID")
	}

	url := "https://api.telegram.org/bot" + cfg.TgToken + "/sendMessage"
	payload := map[string]interface{}{"chat_id": cfg.TgChatID, "text": message}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return MessageRef{}, fmt.Errorf("构造 Telegram 请求失败: %s", scrub(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := proxy.HTTPClient(10 * time.Second).Do(req)
	if err != nil {
		// url.Error 通常会携带完整请求 URL，其中包含 Bot Token。
		return MessageRef{}, fmt.Errorf("请求 Telegram API 失败: %s", scrub(err.Error()))
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return MessageRef{}, fmt.Errorf("Telegram API 返回 HTTP %d: %s", resp.StatusCode, scrub(string(respBody)))
	}
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil || !result.OK || result.Result.MessageID == 0 {
		return MessageRef{}, fmt.Errorf("Telegram API 未返回有效 message_id")
	}
	return MessageRef{ChatID: cfg.TgChatID, MessageID: result.Result.MessageID}, nil
}

// EditMessageText 原地更新 Bot 自己发送的消息正文和按钮。
func EditMessageText(state *app.State, chatID string, messageID int64, text string, replyMarkup map[string]interface{}) error {
	cfg := state.Config.Get()
	if cfg.TgToken == "" {
		return fmt.Errorf("未配置 Telegram Bot Token")
	}
	payload := map[string]interface{}{"chat_id": chatID, "message_id": messageID, "text": text, "parse_mode": "HTML"}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot"+cfg.TgToken+"/editMessageText", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := proxy.HTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("请求 Telegram API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("Telegram API 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// SetWebhook 调用 Telegram setWebhook
func SetWebhook(state *app.State, webhookURL string) (bool, string, map[string]interface{}) {
	cfg := state.Config.Get()
	if cfg.TgToken == "" {
		return false, "未配置 Telegram Bot Token", nil
	}
	if !strings.HasPrefix(webhookURL, "http://") && !strings.HasPrefix(webhookURL, "https://") {
		return false, "Webhook URL 必须以 http:// 或 https:// 开头", nil
	}
	if !strings.HasSuffix(webhookURL, "/api/telegram/webhook") {
		webhookURL = strings.TrimSuffix(webhookURL, "/") + "/api/telegram/webhook"
	}
	state.Logger.Info("正在设置 Telegram Webhook: "+webhookURL, "telegram")

	// 带上 secret_token：之后 Telegram 每次回调都会带 X-Telegram-Bot-Api-Secret-Token 头，
	// webhook handler 用它区分「真的来自 Telegram」和「别人拿 URL 伪造」。
	secret, secErr := EnsureWebhookSecret(state)
	if secErr != nil {
		state.Logger.Warn("生成 webhook secret 失败，本次将不带 secret_token 注册: "+secErr.Error(), "telegram")
	}

	setURL := "https://api.telegram.org/bot" + cfg.TgToken + "/setWebhook"
	q := url.Values{}
	q.Set("url", webhookURL)
	if secret != "" {
		q.Set("secret_token", secret)
	}
	req, _ := http.NewRequest(http.MethodPost, setURL+"?"+q.Encode(), nil)
	client := proxy.HTTPClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		state.Logger.Error("请求 Telegram API 失败: "+scrub(err.Error()), "telegram")
		return false, scrub(err.Error()), nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	_ = json.Unmarshal(body, &result)
	if ok, _ := result["ok"].(bool); ok {
		state.Logger.Info("✅ Telegram Webhook 设置成功: "+webhookURL, "telegram")
		if secret != "" {
			// secret 已经推给 Telegram，从此刻起 webhook 强制校验
			MarkWebhookSecretRegistered(state)
		}
		// 获取 webhook info
		var info map[string]interface{}
		infoResp, err := client.Get("https://api.telegram.org/bot" + cfg.TgToken + "/getWebhookInfo")
		if err == nil {
			infoBody, _ := io.ReadAll(infoResp.Body)
			infoResp.Body.Close()
			var infoResult map[string]interface{}
			_ = json.Unmarshal(infoBody, &infoResult)
			if r, ok := infoResult["result"].(map[string]interface{}); ok {
				info = r
			}
		}
		return true, webhookURL, info
	}
	desc, _ := result["description"].(string)
	state.Logger.Error("Telegram Webhook 设置失败: "+desc, "telegram")
	return false, desc, nil
}

// GetWebhookInfo
func GetWebhookInfo(state *app.State) (bool, map[string]interface{}, string) {
	cfg := state.Config.Get()
	if cfg.TgToken == "" {
		return false, nil, "未配置 Telegram Bot Token"
	}
	client := proxy.HTTPClient(10 * time.Second)
	resp, err := client.Get("https://api.telegram.org/bot" + cfg.TgToken + "/getWebhookInfo")
	if err != nil {
		state.Logger.Error("请求 Telegram API 失败: "+scrub(err.Error()), "telegram")
		return false, nil, scrub(err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	_ = json.Unmarshal(body, &result)
	if ok, _ := result["ok"].(bool); ok {
		if r, ok := result["result"].(map[string]interface{}); ok {
			return true, r, ""
		}
		return true, nil, ""
	}
	desc, _ := result["description"].(string)
	return false, nil, desc
}

// AnswerCallback 应答 callback_query
func AnswerCallback(state *app.State, callbackQueryID, text string, showAlert bool) {
	cfg := state.Config.Get()
	if cfg.TgToken == "" {
		return
	}
	payload := map[string]interface{}{
		"callback_query_id": callbackQueryID,
		"text":              text,
		"show_alert":        showAlert,
	}
	body, _ := json.Marshal(payload)
	client := proxy.HTTPClient(5 * time.Second)
	req, _ := http.NewRequest(http.MethodPost,
		"https://api.telegram.org/bot"+cfg.TgToken+"/answerCallbackQuery",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// EditMessageReplyMarkup 替换已有 Telegram 消息的内联按钮。
// 账户选择只改按钮，保留原有的有货通知正文，用户全程无需输入文字。
func EditMessageReplyMarkup(state *app.State, chatID interface{}, messageID int64, replyMarkup map[string]interface{}) error {
	cfg := state.Config.Get()
	if cfg.TgToken == "" {
		return fmt.Errorf("未配置 Telegram Bot Token")
	}
	payload := map[string]interface{}{"chat_id": chatID, "message_id": messageID, "reply_markup": replyMarkup}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot"+cfg.TgToken+"/editMessageReplyMarkup", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := proxy.HTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("请求 Telegram API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("Telegram API 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// SendReplyWithMarkup 回复指定消息并附带内联按钮。
func SendReplyWithMarkup(state *app.State, chatID interface{}, text string, replyToMessageID int64, replyMarkup map[string]interface{}) bool {
	cfg := state.Config.Get()
	if cfg.TgToken == "" {
		return false
	}
	payload := map[string]interface{}{"chat_id": chatID, "text": text, "reply_to_message_id": replyToMessageID, "reply_markup": replyMarkup}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot"+cfg.TgToken+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := proxy.HTTPClient(10 * time.Second).Do(req)
	if err != nil {
		state.Logger.Warn("发送 Telegram 账户选择按钮失败: "+scrub(err.Error()), "telegram")
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// SendReply 回复指定消息
func SendReply(state *app.State, chatID interface{}, text string, replyToMessageID int64) {
	cfg := state.Config.Get()
	if cfg.TgToken == "" {
		return
	}
	payload := map[string]interface{}{
		"chat_id":             chatID,
		"text":                text,
		"reply_to_message_id": replyToMessageID,
	}
	body, _ := json.Marshal(payload)
	client := proxy.HTTPClient(10 * time.Second)
	req, _ := http.NewRequest(http.MethodPost,
		"https://api.telegram.org/bot"+cfg.TgToken+"/sendMessage",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

type OrderInfo struct {
	PlanCode   string
	Datacenter string
	// Quantity 每个机房下几台。有上限,见 MaxOrderQuantity。
	Quantity int
	Options  []string
}

// 格式: plancode [datacenter] [quantity] [options(逗号分隔)]
func ParseOrderMessage(text string) *OrderInfo {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return nil
	}
	result := &OrderInfo{
		PlanCode: parts[0],
		Quantity: 1,
	}
	remaining := []string{}
	if len(parts) > 1 {
		remaining = parts[1:]
	}
	if len(remaining) == 0 {
		return result
	}

	// 找包含逗号的部分 = options
	optionsStart := -1
	for i, p := range remaining {
		if strings.Contains(p, ",") {
			optionsStart = i
			break
		}
	}
	if optionsStart >= 0 {
		optsText := strings.Join(remaining[optionsStart:], " ")
		for _, o := range strings.Split(optsText, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				result.Options = append(result.Options, o)
			}
		}
		remaining = remaining[:optionsStart]
	}

	switch len(remaining) {
	case 1:
		p := remaining[0]
		if n, ok := parsePositiveInt(p); ok {
			result.Quantity = clampQuantity(n)
		} else if len(p) >= 3 && len(p) <= 4 && isAllLowerAlpha(p) {
			result.Datacenter = p
		}
	case 2:
		p1, p2 := remaining[0], remaining[1]
		if len(p1) >= 3 && len(p1) <= 4 && isAllLowerAlpha(p1) {
			result.Datacenter = p1
			if n, ok := parsePositiveInt(p2); ok {
				result.Quantity = clampQuantity(n)
			}
		} else if n, ok := parsePositiveInt(p1); ok {
			result.Quantity = clampQuantity(n)
			if len(p2) >= 3 && len(p2) <= 4 && isAllLowerAlpha(p2) {
				result.Datacenter = p2
			}
		}
	}
	return result
}

// parsePositiveInt 只接受纯十进制 ASCII 数字字符串，
// 不接受 "-1" / "+5" / " 3" 等带符号或空白的版本（strconv.Atoi 会通过）。
// MaxOrderQuantity 一条聊天消息能指定的最大数量。
// 没有上限时 "planCode 4000000000" 会让 order_processor 先把 40 亿个
// QueueItem append 进一个切片 —— 进程当场 OOM 被杀。
const MaxOrderQuantity = 20

// MaxOrderFanout 一条消息最多创建多少个抢购任务。
// 不指定机房时任务数 = 配置数 × 有货机房数 × 数量,很容易远超用户直觉。
const MaxOrderFanout = 60

// clampQuantity 把数量夹到 [1, MaxOrderQuantity]
func clampQuantity(n int) int {
	if n < 1 {
		return 1
	}
	if n > MaxOrderQuantity {
		return MaxOrderQuantity
	}
	return n
}

func parsePositiveInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func isAllLowerAlpha(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) || !unicode.IsLower(r) {
			return false
		}
	}
	return len(s) > 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
