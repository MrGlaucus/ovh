package monitor

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ovh-buy/server/internal/catalog"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/notify"
	"github.com/ovh-buy/server/internal/ovh"
	"github.com/ovh-buy/server/internal/telegram"
	"github.com/ovh-buy/server/internal/types"
)

// 机房代码 → 中文显示。key 一律是「城市段」,长代码由 availabilityDCCity 归一化后再查。
//
// 取值范围来自 dedicated.AvailabilityDatacenterEnum —— 实测 EU / US / CA 三个站点
// 的这份枚举完全一致(48 个取值),里面既有短代码(gra / bhs / vin)也有带可用区的
// 长代码(eu-west-par-a / ca-east-tor-a / ap-southeast-sgp-a)。
// live 数据两种都会出现:三个站点的 availabilities 里都实际返回过
// eu-west-par-a|b|c 和 ca-east-tor-a,而 vin / hil 只在 US 站点出现。
//
// ⚠️ 国别/城市的唯一事实来源是 catalog 包里的 dcCityMap / dcCountryMap
// (服务器列表、前端 web/src/routes/vps-control.tsx 都按那张表显示)。
// 这里之所以还留一份,只是因为 catalog 的 lookupDCName 目前没导出;
// 两张表分歧过一次,后果是同一个机房在 Telegram 通知里叫「意大利·埃里切」、
// 在服务器列表里叫「英国·埃里斯」,用户没法判断到底下单到了哪个国家。
// 改这里必须同步 catalog,catalog 一旦导出 LookupDCName 就把本表删掉。
//
// 已按 catalog 校正过的三处(OVH 官方机房位置):
//
//	eri = 英国 Erith(伦敦东南),不是意大利埃里切
//	lim = 德国 Limburg(林堡),不是波兰利马诺瓦 —— OVH 在波兰只有华沙 WAW
//	bhs = 加拿大 Beauharnois(博阿尔诺),不是博舍维尔
var dcDisplayMapCN = map[string]string{
	"gra":    "🇫🇷 法国·格拉沃利讷",
	"rbx":    "🇫🇷 法国·鲁贝",
	"rbx-hz": "🇫🇷 法国·鲁贝(HZ)",
	"sbg":    "🇫🇷 法国·斯特拉斯堡",
	"par":    "🇫🇷 法国·巴黎",
	"eri":    "🇬🇧 英国·埃里斯",
	"mil":    "🇮🇹 意大利·米兰",
	"lim":    "🇩🇪 德国·林堡",
	"waw":    "🇵🇱 波兰·华沙",
	"fra":    "🇩🇪 德国·法兰克福",
	"lon":    "🇬🇧 英国·伦敦",
	"bhs":    "🇨🇦 加拿大·博阿尔诺",
	"tor":    "🇨🇦 加拿大·多伦多",
	"yyz":    "🇨🇦 加拿大·多伦多",
	"syd":    "🇦🇺 澳大利亚·悉尼",
	"sgp":    "🇸🇬 新加坡",
	"ynm":    "🇮🇳 印度·孟买",
	"mum":    "🇮🇳 印度·孟买",
	"vin":    "🇺🇸 美国·弗吉尼亚",
	"hil":    "🇺🇸 美国·俄勒冈",
	// 枚举里还有国别粒度的取值(不带城市),照样要能显示
	"fr": "🇫🇷 法国", "de": "🇩🇪 德国", "gb": "🇬🇧 英国", "pl": "🇵🇱 波兰",
	"ca": "🇨🇦 加拿大", "us": "🇺🇸 美国", "au": "🇦🇺 澳大利亚",
	"sg": "🇸🇬 新加坡", "in": "🇮🇳 印度",
	// 枚举里还有 eu / default 两个非国家取值,漏了就会显示成裸 "EU" / "DEFAULT"
	"eu": "🇪🇺 欧洲", "default": "默认机房",
}

var dcDisplayShort = map[string]string{
	"gra":    "🇫🇷 Gra",
	"rbx":    "🇫🇷 Rbx",
	"rbx-hz": "🇫🇷 RbxHZ",
	"sbg":    "🇫🇷 Sbg",
	"par":    "🇫🇷 Par",
	"eri":    "🇬🇧 Eri",
	"mil":    "🇮🇹 Mil",
	"lim":    "🇩🇪 Lim",
	"waw":    "🇵🇱 Waw",
	"fra":    "🇩🇪 Fra",
	"lon":    "🇬🇧 Lon",
	"bhs":    "🇨🇦 Bhs",
	"tor":    "🇨🇦 Tor",
	"yyz":    "🇨🇦 Tor",
	"syd":    "🇦🇺 Syd",
	"sgp":    "🇸🇬 Sgp",
	"ynm":    "🇮🇳 Mum",
	"mum":    "🇮🇳 Mum",
	"vin":    "🇺🇸 Vin",
	"hil":    "🇺🇸 Hil",
	"fr":     "🇫🇷 FR", "de": "🇩🇪 DE", "gb": "🇬🇧 GB", "pl": "🇵🇱 PL",
	"ca": "🇨🇦 CA", "us": "🇺🇸 US", "au": "🇦🇺 AU",
	"sg": "🇸🇬 SG", "in": "🇮🇳 IN",
	"eu": "🇪🇺 EU", "default": "默认",
}

// availabilityDCCity 把 dedicated.AvailabilityDatacenterEnum 的取值归一化成城市段 + 可用区。
//
//	"gra"                → ("gra", "")
//	"eu-west-par-b"      → ("par", "B")
//	"ca-east-tor-a"      → ("tor", "A")
//	"ap-southeast-sgp-a" → ("sgp", "A")
//	"rbx-hz"             → ("rbx-hz", "")   两段的先按整体查,查不到再退化
//	"eu-west-1-a"        → ("1", "A")       没有城市名,调用方会退回裸代码
//
// 规则:三段以上且最后一段是单个字母时,那一段是可用区,前一段是城市。
func availabilityDCCity(dc string) (city, az string) {
	d := strings.ToLower(strings.TrimSpace(dc))
	if d == "" {
		return "", ""
	}
	if _, ok := dcDisplayMapCN[d]; ok {
		return d, ""
	}
	parts := strings.Split(d, "-")
	if len(parts) >= 3 {
		last := parts[len(parts)-1]
		if len(last) == 1 && last[0] >= 'a' && last[0] <= 'z' {
			return parts[len(parts)-2], strings.ToUpper(last)
		}
		return last, ""
	}
	return d, ""
}

func dcDisplayCN(dc string) string {
	city, az := availabilityDCCity(dc)
	v, ok := dcDisplayMapCN[city]
	if !ok {
		// 未知代码原样回显:宁可让用户看到裸代码去查,也不要显示一个猜错的城市
		return strings.ToUpper(dc)
	}
	if az != "" {
		return v + " (AZ-" + az + ")"
	}
	return v
}

// DisplayDatacenterShortName 返回 Telegram 按钮用的带国旗短机房名。
// 账户选择页恢复原按钮时也必须调用它，保证交互前后显示一致。
func DisplayDatacenterShortName(dc string) string {
	city, az := availabilityDCCity(dc)
	v, ok := dcDisplayShort[city]
	if !ok {
		return strings.ToUpper(dc)
	}
	if az != "" {
		return v + "-" + az
	}
	return v
}

func (m *Monitor) saveTelegramAvailabilitySession(planCode string, configInfo map[string]interface{}, message string, ref telegram.MessageRef, dcs []map[string]interface{}) {
	if m.state.DB == nil {
		return
	}
	configKey, _ := configInfo["config_key"].(string)
	if configKey == "" {
		m.state.Logger.Warn("未记录 Telegram 通知关联：缺少配置 FQN", "monitor")
		return
	}
	session := db.TelegramNotificationSession{
		ID: uuid.NewString(), PlanCode: planCode, ConfigKey: configKey,
		ChatID: ref.ChatID, MessageID: ref.MessageID, MessageText: message,
		CreatedAt: float64(time.Now().Unix()),
	}
	rows := make([]db.TelegramNotificationDatacenter, 0, len(dcs))
	for _, item := range dcs {
		dc, _ := item["dc"].(string)
		line, _ := item["notification_line"].(string)
		buttonID, _ := item["notification_button_id"].(string)
		buttonText, _ := item["notification_button_text"].(string)
		openedAt := session.CreatedAt
		if raw, _ := item["detected_time"].(string); raw != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				openedAt = float64(parsed.Unix())
			}
		}
		rows = append(rows, db.TelegramNotificationDatacenter{Datacenter: dc, LineText: line, ButtonID: buttonID, ButtonText: buttonText, OpenedAt: openedAt})
	}
	if err := m.state.DB.CreateTelegramNotificationSession(session, rows); err != nil {
		m.state.Logger.Warn("保存 Telegram 通知关联失败: "+err.Error(), "monitor")
	}
}

func (m *Monitor) updateTelegramUnavailable(planCode string, configInfo map[string]interface{}, dcs []map[string]interface{}) []map[string]interface{} {
	if m.state.DB == nil {
		return dcs
	}
	configKey, _ := configInfo["config_key"].(string)
	if configKey == "" {
		return dcs
	}
	m.notificationMu.Lock()
	defer m.notificationMu.Unlock()
	fallback := make([]map[string]interface{}, 0, len(dcs))
	for _, item := range dcs {
		dcName, _ := item["dc"].(string)
		snapshot, found, err := m.state.DB.CloseTelegramNotificationDatacenter(planCode, configKey, dcName, float64(time.Now().Unix()))
		if err != nil || !found {
			fallback = append(fallback, item)
			if err != nil {
				m.state.Logger.Warn("关闭 Telegram 通知机房关联失败: "+err.Error(), "monitor")
			}
			continue
		}
		text, markup := renderTelegramNotificationUnavailable(snapshot)
		if err := telegram.EditMessageText(m.state, snapshot.Session.ChatID, snapshot.Session.MessageID, text, markup); err != nil {
			m.state.Logger.Warn("编辑原 Telegram 上架通知失败，改发独立下架通知: "+err.Error(), "monitor")
			fallback = append(fallback, item)
		}
	}
	return fallback
}

func renderTelegramNotificationUnavailable(snapshot db.TelegramNotificationSnapshot) (string, map[string]interface{}) {
	text := html.EscapeString(snapshot.Session.MessageText)
	allClosed := true
	type button struct {
		Text         string `json:"text"`
		CallbackData string `json:"callback_data"`
	}
	// 新格式通知的机房按钮不落库（回调明文含 planCode + 机房名），
	// 编辑时按会话里的机房状态重建：未下架保留、已下架移除。
	// 旧格式通知沿用落库按钮，同样按机房状态重建。
	hasLegacyButtons := false
	for _, dc := range snapshot.Datacenters {
		if strings.TrimSpace(dc.ButtonID) != "" {
			hasLegacyButtons = true
			break
		}
	}
	keyboard := [][]button{}
	line := []button{}
	for _, dc := range snapshot.Datacenters {
		if dc.ClosedAt == 0 {
			allClosed = false
			if !hasLegacyButtons {
				// 未下架机房保留按钮：回调按会话 planCode + 行里机房名重建。
				line = append(line, button{
					Text:         DisplayDatacenterShortName(dc.Datacenter) + " " + telegramBuyMenuButtonText,
					CallbackData: telegramBuyMenuCallback(snapshot.Session.PlanCode, dc.Datacenter),
				})
				if len(line) == 2 {
					keyboard = append(keyboard, line)
					line = nil
				}
				continue
			}
			action := "add_to_queue"
			if strings.Contains(dc.ButtonText, "选择账户下单") {
				action = "choose"
			}
			callback, _ := json.Marshal(map[string]string{"a": action, "u": dc.ButtonID})
			line = append(line, button{Text: dc.ButtonText, CallbackData: string(callback)})
			if len(line) == 2 {
				keyboard = append(keyboard, line)
				line = nil
			}
			continue
		}
		escapedLine := html.EscapeString(dc.LineText)
		duration := humanDuration(time.Duration((dc.ClosedAt - dc.OpenedAt) * float64(time.Second)))
		var replacement string
		if strings.HasPrefix(escapedLine, "   ✅ ") {
			// 多机房列表:整行划掉,行首图标保留
			replacement = "   ✅ <s>" + strings.TrimPrefix(escapedLine, "   ✅ ") + "</s>\n    ⚫ 已下架 · 在库时长：" + duration
		} else {
			// 单机房是"数据中心 / 可用性"两行一块,整块划掉,不留孤行
			lines := strings.Split(escapedLine, "\n")
			for i := range lines {
				lines[i] = "<s>" + lines[i] + "</s>"
			}
			replacement = strings.Join(lines, "\n") + "\n    ⚫ 已下架 · 在库时长：" + duration
		}
		text = strings.Replace(text, escapedLine, replacement, 1)
	}
	if len(line) > 0 {
		keyboard = append(keyboard, line)
	}
	if allClosed {
		text = strings.Replace(text, "🎉 服务器上架通知", "⚫ 服务器已下架", 1)
	} else {
		text = strings.Replace(text, "🎉 服务器上架通知", "🟡 部分机房已下架", 1)
	}
	return text, map[string]interface{}{"inline_keyboard": keyboard}
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d.Seconds())
	switch {
	case seconds >= 3600:
		return fmt.Sprintf("%d小时%d分%d秒", seconds/3600, (seconds%3600)/60, seconds%60)
	case seconds >= 60:
		return fmt.Sprintf("%d分%d秒", seconds/60, seconds%60)
	default:
		return fmt.Sprintf("%d秒", seconds)
	}
}

// CompatibleOrderAccounts 返回与参照账户同一区域的可下单账户。
// 同一区域的 OVH 库存视图相通；跨 EU/US/CA 账户下单只会得到空库存，故不展示。
// 空 id（历史通知按钮没记下账户归属）退回默认账户再取区域，与下单侧兜底一致。
func (m *Monitor) CompatibleOrderAccounts(referenceAccountID string) []types.OVHAccount {
	reference, ok := m.state.FindAccount(referenceAccountID)
	if !ok {
		return nil
	}
	region := ovh.SubsidiaryRegion(catalog.SubsidiaryOfAccount(reference))
	m.state.AccountsMu.RLock()
	accounts := make([]types.OVHAccount, 0, len(m.state.Accounts))
	for _, account := range m.state.Accounts {
		if ovh.SubsidiaryRegion(catalog.SubsidiaryOfAccount(account)) == region {
			accounts = append(accounts, account)
		}
	}
	m.state.AccountsMu.RUnlock()
	return accounts
}

// telegramBuyMenuButtonText 是上架通知机房按钮的文案后缀：
// 点开后进入该机房「配置 → 账户」的实时库存选购链。
const telegramBuyMenuButtonText = "选择配置下单"

// telegramBuyMenuCallback 组装上架通知机房按钮的回调数据。
// planCode 与机房名都很短，可安全放进 64 字节的 callback_data；
// 点击后每一步都会按点击那一刻的实时库存重新校验，通知快照不参与下单决策。
func telegramBuyMenuCallback(planCode, datacenter string) string {
	cb, _ := json.Marshal(map[string]string{"a": "menu", "p": planCode, "d": datacenter})
	return string(cb)
}

// humanMemoryText / humanStorageText 把裸 addon FQN(ram-64g-ecc-2133 /
// softraid-4x2000sa)转成人读文案 —— 监控落库的是 FQN,直接贴进通知里
// 用户只能看到一串代码。已是人读文本的值(含空格,例如旧数据里的
// "32GB ECC DDR4-2400"、目录缺数据时的 "N/A")原样保留,避免被正则
// 翻译一遍反而丢信息。
func humanMemoryText(code string) string {
	if code == "" || strings.Contains(code, " ") {
		return code
	}
	return catalog.FormatMemoryDisplay(code)
}

func humanStorageText(code string) string {
	if code == "" || strings.Contains(code, " ") {
		return code
	}
	return catalog.FormatStorageDisplay(code)
}

// configMemoryStorage 从 configInfo 解出内存 / 存储的可读文案。
func configMemoryStorage(configInfo map[string]interface{}) (memory, storage string) {
	if configInfo == nil {
		return "", ""
	}
	memory, _ = configInfo["memory"].(string)
	storage, _ = configInfo["storage"].(string)
	return humanMemoryText(memory), humanStorageText(storage)
}

// buildAvailabilityAlert 拼出上架通知的正文和按钮。
//
// 从 SendAvailabilityAlertGrouped 里拆出来，是为了能在测试里直接看到
// 用户真正会收到的那段文字 —— 通知的排版是这个工具的门面，
// 以前只能靠真的触发一次补货才看得见。
// 按钮只有一颗「选择配置下单」入口：点击后走与 /buy 相同的
// 「配置 → 机房 → 账户」链，每一步按点击那一刻的实时库存生成。
func (m *Monitor) buildAvailabilityAlert(planCode string, availableDCs []map[string]interface{},
	configInfo map[string]interface{}, serverName string, priceErrorMessage string, traceID, configTraceID string) (string, map[string]interface{}) {

	var msg strings.Builder
	msg.WriteString("🎉 服务器上架通知\n\n")

	// 产品名称:型号名 + CPU。
	// 光一个 planCode(24sk602)对着手机看不出是什么机器,
	// 而抢购那一刻用户要在几秒内判断"这是不是我要的那台"。
	msg.WriteString("📦 产品名称: " + m.productName(planCode, serverName) + "\n")

	memory, storage := configMemoryStorage(configInfo)
	if memory != "" {
		msg.WriteString("💾 内存: " + memory + "\n")
	}
	if storage != "" {
		msg.WriteString("💿 存储: " + storage + "\n")
	}

	// 机房 + 可用性。
	// 单个机房时按"数据中心 / 可用性"两行摊开;多个机房时列成一张表 ——
	// 每个机房的可用性可能不一样(waw 是 1H-low、gra 是 72H),
	// 合并成一句"N 个机房有货"会把这个差别抹掉,而它直接决定先抢哪个。
	//
	// notification_line 是通知生命周期的锚:下架时按它把对应行划掉,
	// 所以两种布局都要把行文本存进 dcInfo。单机房时把两行一起存,
	// 下架渲染时整块划掉,不会留下"可用性"孤行。
	var detectedTimes []time.Time
	if len(availableDCs) == 1 {
		dc, _ := availableDCs[0]["dc"].(string)
		line := "📍 数据中心: " + dcLine(dc)
		avail := "✅ 可用性: " + availWording(availableDCs[0])
		msg.WriteString(line + "\n")
		msg.WriteString(avail + "\n")
		availableDCs[0]["notification_line"] = line + "\n" + avail
	} else {
		msg.WriteString(fmt.Sprintf("📍 数据中心: %d 个机房有货\n", len(availableDCs)))
		for _, dcInfo := range availableDCs {
			dc, _ := dcInfo["dc"].(string)
			line := "   ✅ " + dcLine(dc) + " — " + availWording(dcInfo)
			msg.WriteString(line + "\n")
			dcInfo["notification_line"] = line
		}
	}
	for _, dcInfo := range availableDCs {
		if dtStr, ok := dcInfo["detected_time"].(string); ok && dtStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, dtStr); err == nil {
				detectedTimes = append(detectedTimes, t)
			}
		}
	}

	// 价格。月费和安装费分开写:安装费是一次性的,混在一起会让人以为月付这么多。
	priceText, _ := configInfo["cached_price"].(string)
	installText, _ := configInfo["install_price"].(string)
	switch {
	case priceText != "":
		msg.WriteString("💰 价格: " + priceText + "\n")
	case priceErrorMessage != "":
		// 价格查不到必须明说。留空会被读成"免费"或"还没加载",
		// 而这两种理解都会让用户按下一个他不知道要花多少钱的按钮。
		msg.WriteString("💰 价格: 未获取到（" + priceErrorMessage + "）\n")
	default:
		msg.WriteString("💰 价格: 未获取到\n")
	}
	if installText != "" {
		msg.WriteString("💵 安装费: " + installText + "\n")
	}

	// 这套配置已经有几个任务在抢。
	// 补货常连着来好几条通知,对同一台机器按两次就是两笔真实订单,
	// 而按钮的一次性 claim 只挡得住同一颗按钮按两次。
	if n := m.activeQueueCount(planCode, optionsFromConfig(configInfo)); n > 0 {
		msg.WriteString(fmt.Sprintf("\n⚠️ 这套配置已经有 %d 个任务在抢了（发 /queue 查看）\n", n))
	}

	pushTime := m.nowBeijing()
	detected := pushTime
	if len(detectedTimes) > 0 {
		detected = detectedTimes[0]
		for _, t := range detectedTimes[1:] {
			if t.Before(detected) {
				detected = t
			}
		}
	}
	msg.WriteString("\n🕐 检测时间: " + detected.Format("2006-01-02 15:04:05") + "\n")
	// 推送延迟只在明显偏大时才提 —— 正常情况下它是噪音,
	// 但延迟到分钟级说明通道有问题,那时候用户必须知道自己看到的是旧消息。
	if lag := pushTime.Sub(detected); lag >= 30*time.Second {
		msg.WriteString(fmt.Sprintf("⚠️ 这条通知延迟了 %.0f 秒才发出\n", lag.Seconds()))
	}
	// Trace ID 仅用于服务端日志排障，不再暴露给通知接收者。
	_ = traceID
	_ = configTraceID

	// 下单按钮按机房逐颗排列（与旧格式一致）：点哪颗就是选哪个机房，
	// 点开后的「配置 → 账户」链在点击那一刻按实时库存重新查询。
	// 通知里的机房列表只用于展示与按钮排布，不参与下单决策，
	// 因此也不再有"机房下架导致按钮失效"的问题。
	type btn struct {
		Text         string `json:"text"`
		CallbackData string `json:"callback_data"`
	}
	keyboard := [][]btn{}
	row := []btn{}
	for idx, dcInfo := range availableDCs {
		dc, _ := dcInfo["dc"].(string)
		callback := telegramBuyMenuCallback(planCode, dc)
		if len(callback) > 64 {
			m.state.Logger.Warn(fmt.Sprintf("上架通知机房按钮回调超长(%d字节)，TG 会拒绝该消息: dc=%s", len(callback), dc), "monitor")
		}
		row = append(row, btn{Text: DisplayDatacenterShortName(dc) + " " + telegramBuyMenuButtonText, CallbackData: callback})
		if len(row) == 2 || idx == len(availableDCs)-1 {
			keyboard = append(keyboard, row)
			row = nil
		}
	}
	return msg.String(), map[string]interface{}{"inline_keyboard": keyboard}
}

// SendAvailabilityAlertGrouped 拼好通知并广播出去。
func (m *Monitor) SendAvailabilityAlertGrouped(planCode string, availableDCs []map[string]interface{},
	configInfo map[string]interface{}, serverName string, priceErrorMessage string, traceID, configTraceID string) {

	msgText, replyMarkup := m.buildAvailabilityAlert(planCode, availableDCs, configInfo,
		serverName, priceErrorMessage, traceID, configTraceID)

	configDesc := ""
	if configInfo != nil {
		if d, ok := configInfo["display"].(string); ok {
			configDesc = " [" + d + "]"
		}
	}
	m.state.Logger.Info(fmt.Sprintf("正在发送汇总Telegram通知: %s%s - %d个机房", planCode, configDesc, len(availableDCs)), "monitor")
	entry, pending := m.enqueueAvailability(planCode, availableDCs, configInfo, serverName, priceErrorMessage)
	result := notify.BroadcastWithResult(m.state, msgText, replyMarkup)
	if result.Telegram != nil {
		m.saveTelegramAvailabilitySession(planCode, configInfo, msgText, *result.Telegram, availableDCs)
	}
	m.finishPending(entry, pending, result.Telegram, result.TelegramError)
	if result.Telegram != nil {
		m.state.Logger.Info(fmt.Sprintf("✅ Telegram汇总通知发送成功: %s%s", planCode, configDesc), "monitor")
	} else if result.TelegramError != nil {
		m.state.Logger.Warn(fmt.Sprintf("⚠️ Telegram汇总通知发送失败: %s%s", planCode, configDesc), "monitor")
	}
}

func (m *Monitor) SendUnavailableAlertGrouped(planCode string, unavailableDCs []map[string]interface{},
	configInfo map[string]interface{}, serverName, traceID, configTraceID string) {

	// 新通知优先原地更新对应上架消息；旧版本消息或 API 编辑失败时才发送独立下架通知。
	unavailableDCs = m.updateTelegramUnavailable(planCode, configInfo, unavailableDCs)
	if len(unavailableDCs) == 0 {
		return
	}

	var msg strings.Builder
	msg.WriteString("📦 服务器下架通知\n\n")
	if serverName != "" {
		msg.WriteString("服务器: " + serverName + "\n")
	}
	msg.WriteString("型号: " + planCode + "\n")
	if configInfo != nil {
		display, _ := configInfo["display"].(string)
		memory, storage := configMemoryStorage(configInfo)
		msg.WriteString("配置: " + display + "\n")
		msg.WriteString("├─ 内存: " + memory + "\n")
		msg.WriteString("└─ 存储: " + storage + "\n")
	}
	msg.WriteString(fmt.Sprintf("\n已下架机房 (%d 个):\n", len(unavailableDCs)))
	for _, dcInfo := range unavailableDCs {
		dc, _ := dcInfo["dc"].(string)
		msg.WriteString("  • " + dcDisplayCN(dc) + " (" + strings.ToUpper(dc) + ")")
		if dt, ok := dcInfo["duration_text"].(string); ok && dt != "" {
			msg.WriteString(" - ⏱️ 本次上架持续: " + strings.TrimPrefix(dt, "历时 "))
		}
		msg.WriteString("\n")
	}
	// Trace ID 仅用于服务端日志排障，不再暴露给通知接收者。
	_ = traceID
	_ = configTraceID
	msg.WriteString("\n⏰ 时间: " + m.nowBeijing().Format("2006-01-02 15:04:05"))

	configDesc := ""
	if configInfo != nil {
		if d, ok := configInfo["display"].(string); ok {
			configDesc = " [" + d + "]"
		}
	}
	m.state.Logger.Info(fmt.Sprintf("正在发送聚合下架Telegram通知: %s%s - %d个机房", planCode, configDesc, len(unavailableDCs)), "monitor")
	if notify.Broadcast(m.state, msg.String(), nil) > 0 {
		m.state.Logger.Info(fmt.Sprintf("✅ Telegram聚合下架通知发送成功: %s%s", planCode, configDesc), "monitor")
	} else {
		m.state.Logger.Warn(fmt.Sprintf("⚠️ Telegram聚合下架通知发送失败: %s%s", planCode, configDesc), "monitor")
	}
}

func (m *Monitor) SendAvailabilityAlert(planCode, datacenter, status, changeType string,
	configInfo map[string]interface{}, serverName, durationText, priceCheckError, traceID, configTraceID, detectedTime string) {

	var msg strings.Builder
	pushTime := m.nowBeijing()
	// Trace ID 只在服务端日志中使用，不再发送给通知接收者。
	_ = traceID
	_ = configTraceID

	switch changeType {
	case "available":
		msg.WriteString("🎉 服务器上架通知！\n\n")
		if serverName != "" {
			msg.WriteString("服务器: " + serverName + "\n")
		}
		msg.WriteString("型号: " + planCode + "\n")
		msg.WriteString("数据中心: " + datacenter + "\n")
		if configInfo != nil {
			display, _ := configInfo["display"].(string)
			memory, storage := configMemoryStorage(configInfo)
			msg.WriteString("配置: " + display + "\n")
			msg.WriteString("├─ 内存: " + memory + "\n")
			msg.WriteString("└─ 存储: " + storage + "\n")
		}
		priceText, _ := configInfo["cached_price"].(string)
		if priceText == "" {
			// 兜底也走公开目录的月付条目:购物车询价返回的是首期账单总额
			// (月费 + 一次性安装费),贴成"月付"会多算一份安装费。
			priceText = m.monthlyPriceText(planCode, accountIDFromConfig(configInfo), optionsFromConfig(configInfo))
		}
		if priceText != "" {
			msg.WriteString("\n💰 价格: " + priceText + "\n")
		}
		msg.WriteString("状态: " + status + "\n")
		if durationText != "" {
			msg.WriteString("⏱️ 上次无货→本次有货: " + strings.TrimPrefix(durationText, "历时 ") + "\n")
		}
		if detectedTime != "" {
			if t, err := time.Parse(time.RFC3339Nano, detectedTime); err == nil {
				delay := pushTime.Sub(t)
				secs := int(delay.Seconds())
				minutes := secs / 60
				rem := secs % 60
				msg.WriteString("⏰ 检测时间: " + t.Format("2006-01-02 15:04:05") + "\n")
				msg.WriteString("📤 推送时间: " + pushTime.Format("2006-01-02 15:04:05") + "\n")
				switch {
				case secs > 0 && minutes > 0:
					msg.WriteString(fmt.Sprintf("⏱️ 推送延迟: %d分%d秒\n", minutes, rem))
				case secs > 0:
					msg.WriteString(fmt.Sprintf("⏱️ 推送延迟: %d秒\n", rem))
				default:
					msg.WriteString("⏱️ 推送延迟: <1秒\n")
				}
			}
		} else {
			msg.WriteString("⏰ 推送时间: " + pushTime.Format("2006-01-02 15:04:05") + "\n")
		}
		msg.WriteString("\n\n💡 快去抢购吧！")
	case "price_check_failed":
		msg.WriteString("📦 服务器可用性通知\n\n")
		if serverName != "" {
			msg.WriteString("服务器: " + serverName + "\n")
		}
		msg.WriteString("型号: " + planCode + "\n")
		msg.WriteString("数据中心: " + datacenter + "\n")
		if configInfo != nil {
			display, _ := configInfo["display"].(string)
			memory, storage := configMemoryStorage(configInfo)
			msg.WriteString("配置: " + display + "\n")
			msg.WriteString("├─ 内存: " + memory + "\n")
			msg.WriteString("└─ 存储: " + storage + "\n")
		}
		if priceText, ok := configInfo["cached_price"].(string); ok && priceText != "" {
			msg.WriteString("\n💰 价格: " + priceText + "\n")
		}
		msg.WriteString("\n状态: 可用性显示有货\n")
		msg.WriteString("时间: " + pushTime.Format("2006-01-02 15:04:05") + "\n")
		msg.WriteString("\n")
		msg.WriteString("⚠️ 特别说明：\n")
		if priceCheckError != "" {
			msg.WriteString(fmt.Sprintf("（价格校验未通过: %s，已跳过自动下单）", priceCheckError))
		} else {
			msg.WriteString("（价格校验未通过，已跳过自动下单）")
		}
	default:
		msg.WriteString("📦 服务器下架通知\n\n")
		if serverName != "" {
			msg.WriteString("服务器: " + serverName + "\n")
		}
		msg.WriteString("型号: " + planCode + "\n")
		if configInfo != nil {
			display, _ := configInfo["display"].(string)
			memory, storage := configMemoryStorage(configInfo)
			msg.WriteString("配置: " + display + "\n")
			msg.WriteString("├─ 内存: " + memory + "\n")
			msg.WriteString("└─ 存储: " + storage + "\n")
		}
		msg.WriteString("\n数据中心: " + datacenter + "\n")
		msg.WriteString("状态: 已无货\n")
		msg.WriteString("⏰ 时间: " + pushTime.Format("2006-01-02 15:04:05"))
		if durationText != "" {
			msg.WriteString("\n⏱️ 本次上架持续: " + strings.TrimPrefix(durationText, "历时 "))
		}
	}

	configDesc := ""
	if configInfo != nil {
		if d, ok := configInfo["display"].(string); ok {
			configDesc = " [" + d + "]"
		}
	}
	m.state.Logger.Info(fmt.Sprintf("正在发送Telegram通知: %s@%s%s", planCode, datacenter, configDesc), "monitor")
	if notify.Broadcast(m.state, msg.String(), nil) > 0 {
		m.state.Logger.Info(fmt.Sprintf("✅ Telegram通知发送成功: %s@%s%s - %s", planCode, datacenter, configDesc, changeType), "monitor")
	} else {
		m.state.Logger.Warn(fmt.Sprintf("⚠️ Telegram通知发送失败: %s@%s%s", planCode, datacenter, configDesc), "monitor")
	}
}

func (m *Monitor) SendNewServerAlert(server map[string]interface{}) {
	memRaw, _ := server["memory"].(string)
	storRaw, _ := server["storage"].(string)
	msg := fmt.Sprintf("🆕 新服务器上架通知！\n\n型号: %v\n名称: %v\nCPU: %v\n内存: %v\n存储: %v\n带宽: %v\n时间: %s\n\n💡 快去查看详情！",
		server["planCode"], server["name"], server["cpu"], humanMemoryText(memRaw), humanStorageText(storRaw), server["bandwidth"],
		m.nowBeijing().Format("2006-01-02 15:04:05"))
	notify.Broadcast(m.state, msg, nil)
	m.state.Logger.Info(fmt.Sprintf("发送新服务器提醒: %v", server["planCode"]), "monitor")
}

// activeQueueCount 当前有几个进行中的任务会抢"这套配置"。
//
// 用来在上架通知里提醒"你已经在抢这台了"。补货往往连着来好几条通知,
// 用户在手机上对同一台机器按两次按钮是很自然的动作,而那就是两笔真实订单。
// 按钮自己的一次性 claim 只挡得住同一颗按钮按两次,挡不住两条通知各按一次。
//
// configOptions 是这套配置(FQN 内存/存储段)匹配出的 addon。以前这里只
// 匹配 planCode —— 同型号下别的配置的任务也被算进来,用户明明没抢这套
// 配置也看到"有 N 个任务在抢"。现在按 queueTaskTargetsConfig 收窄。
func (m *Monitor) activeQueueCount(planCode string, configOptions []string) int {
	if m.state == nil {
		return 0
	}
	m.state.QueueMu.Lock()
	defer m.state.QueueMu.Unlock()
	n := 0
	for _, it := range m.state.Queue {
		if it.PlanCode != planCode {
			continue
		}
		if it.Status != "running" && it.Status != "pending" && it.Status != "paused" {
			continue
		}
		if !queueTaskTargetsConfig(it.Options, configOptions) {
			continue
		}
		n++
	}
	return n
}

// queueTaskTargetsConfig 判断一个抢购任务会不会抢到"这套配置"。
//
// 任一方向子集成立都算"覆盖到这套":
//   - 任务⊆本套:任务只勾了部分维度(如"ram-64g 的任意存储"),会抢到本套;
//   - 本套⊆任务:任务比本套多勾 —— 网页下单是逐组选择,带宽等不入 FQN 的
//     项也会进任务 options,它一样会抢本套。
//
// 两边的配置维度有一处不一致(如内存选得不同)时两个方向都不成立 → 不算。
// 缺可比信息(任务 options 空 = 裸 planCode 机型,configOptions 空 = 目录缺
// 数据)时退回"算":提醒的目的是防用户手滑重复下单,漏提醒的代价更大。
func queueTaskTargetsConfig(taskOptions, configOptions []string) bool {
	if len(taskOptions) == 0 || len(configOptions) == 0 {
		return true
	}
	return subsetOf(taskOptions, configOptions) || subsetOf(configOptions, taskOptions)
}

// subsetOf sub 里的每一项都在 super 里。
func subsetOf(sub, super []string) bool {
	for _, x := range sub {
		if !containsString(super, x) {
			return false
		}
	}
	return true
}

// productName 通知抬头那一行。
//
// 光一个 planCode(24sk602)在手机上看不出是什么机器,而抢购那一刻用户
// 要在几秒内判断"这是不是我要的那台"。所以把型号名和 CPU 拼出来。
// 目录没加载到时退回 planCode —— 不编,也不留空。
func (m *Monitor) productName(planCode, serverName string) string {
	name := strings.TrimSpace(serverName)
	cpu := ""
	if m.state != nil {
		m.state.ServerPlansMu.RLock()
		for _, p := range m.state.ServerPlans {
			if p.PlanCode == planCode {
				if name == "" {
					name = strings.TrimSpace(p.Name)
				}
				cpu = strings.TrimSpace(p.CPU)
				break
			}
		}
		m.state.ServerPlansMu.RUnlock()
	}
	switch {
	case name != "" && cpu != "":
		// 目录的 invoiceName 本身就可能是完整对外名(如 "KS-2 | Intel Xeon-D 1540"),
		// 而 CPU 字段正是从它 | 后半段提取的 —— 名字里已含 CPU 时不重复拼,
		// 否则会显示成 "... | Intel Xeon-D 1540 | Intel Xeon-D 1540"。
		if strings.Contains(strings.ToLower(name), strings.ToLower(cpu)) {
			return name
		}
		return name + " | " + cpu
	case name != "":
		return name
	default:
		return planCode
	}
}

// dcLine "waw (波兰华沙)"。拿不到中文名就只写代码。
func dcLine(dc string) string {
	code := strings.ToUpper(dc)
	cn := strings.TrimSpace(dcDisplayCN(dc))
	// dcDisplayCN 拿不到时会退回大写代码,那种情况别写成 "WAW (WAW)"
	if cn == "" || strings.EqualFold(cn, dc) {
		return code
	}
	return code + " (" + cn + ")"
}

// availWording 这个机房的可用性该怎么说。
//
// 取的是 OVH 原样返回的值(1H-low / 72H …),它才带着"多久能交付、库存高低"
// 这两个决定要不要立刻下单的信息。没有原值时退回"有货"——
// 那是归一化之后仅剩的事实,不要编一个更具体的说法出来。
func availWording(dcInfo map[string]interface{}) string {
	if raw, ok := dcInfo["raw_status"].(string); ok && raw != "" {
		if w := AvailabilityCN(raw); w != "" {
			return w
		}
	}
	return "有货"
}
