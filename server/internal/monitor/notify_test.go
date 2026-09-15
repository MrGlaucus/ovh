package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/db"
)

func TestDisplayDatacenterShortNameKeepsFlag(t *testing.T) {
	if got, want := DisplayDatacenterShortName("gra"), "🇫🇷 Gra"; got != want {
		t.Fatalf("DisplayDatacenterShortName(gra) = %q, want %q", got, want)
	}
	if got, want := DisplayDatacenterShortName("ca-east-tor-a"), "🇨🇦 Tor-A"; got != want {
		t.Fatalf("DisplayDatacenterShortName(ca-east-tor-a) = %q, want %q", got, want)
	}
}

// 通知延迟到分钟级说明通道有问题，那时候用户必须知道自己看到的是旧消息。
func TestAlertWarnsWhenNotificationDelayed(t *testing.T) {
	m := renderTestMonitor(t)
	detected := time.Now().Add(-90 * time.Second).Format(time.RFC3339Nano)
	dcs := []map[string]interface{}{{"dc": "gra", "detected_time": detected, "raw_status": "24H"}}
	msg, _ := m.buildAvailabilityAlert("24ska01", dcs, nil, "KS-5", "", "", "")
	if !contains(msg, "🕐 检测时间:") {
		t.Errorf("通知里必须有检测时间：%s", msg)
	}
	if !contains(msg, "这条通知延迟了") {
		t.Errorf("延迟 90 秒必须提醒：%s", msg)
	}
}

func TestRenderTelegramNotificationUnavailableStrikesOnlyClosedDatacenter(t *testing.T) {
	snapshot := db.TelegramNotificationSnapshot{
		Session: db.TelegramNotificationSession{MessageText: "🎉 服务器上架通知\n\n📦 产品名称: KS-5 | Intel Xeon\n📍 数据中心: 2 个机房有货\n   ✅ GRA (🇫🇷 法国·格拉沃利讷) — 24小时内有货\n   ✅ FRA (🇩🇪 德国·法兰克福) — 72小时内有货\n\n🕐 检测时间: 2026-09-10 10:00:00"},
		Datacenters: []db.TelegramNotificationDatacenter{
			{Datacenter: "gra", LineText: "   ✅ GRA (🇫🇷 法国·格拉沃利讷) — 24小时内有货", ButtonID: "gra-id", ButtonText: "🇫🇷 Gra 一键下单", OpenedAt: 100, ClosedAt: 225},
			{Datacenter: "fra", LineText: "   ✅ FRA (🇩🇪 德国·法兰克福) — 72小时内有货", ButtonID: "fra-id", ButtonText: "🇩🇪 Fra 一键下单", OpenedAt: 100},
		},
	}
	text, markup := renderTelegramNotificationUnavailable(snapshot)
	for _, want := range []string{"🟡 部分机房已下架", "<s>GRA (🇫🇷 法国·格拉沃利讷) — 24小时内有货</s>", "在库时长：2分5秒", "FRA (🇩🇪 德国·法兰克福) — 72小时内有货"} {
		if !strings.Contains(text, want) {
			t.Errorf("原地更新内容缺少 %q：%s", want, text)
		}
	}
	if markup["inline_keyboard"] == nil {
		t.Fatal("仍在库的机房必须保留可用下单按钮")
	}
}

// 单机房是"数据中心 / 可用性"两行一块：下架时整块划掉，不留"可用性"孤行。
func TestRenderTelegramNotificationUnavailableStrikesSingleDatacenterBlock(t *testing.T) {
	snapshot := db.TelegramNotificationSnapshot{
		Session: db.TelegramNotificationSession{MessageText: "🎉 服务器上架通知\n\n📍 数据中心: WAW (🇵🇱 波兰·华沙)\n✅ 可用性: 1小时内有货 - 低库存\n\n🕐 检测时间: 2026-09-10 10:00:00"},
		Datacenters: []db.TelegramNotificationDatacenter{
			{Datacenter: "waw", LineText: "📍 数据中心: WAW (🇵🇱 波兰·华沙)\n✅ 可用性: 1小时内有货 - 低库存", ButtonID: "waw-id", ButtonText: "🇵🇱 Waw 一键下单", OpenedAt: 100, ClosedAt: 200},
		},
	}
	text, markup := renderTelegramNotificationUnavailable(snapshot)
	for _, want := range []string{"⚫ 服务器已下架", "<s>📍 数据中心: WAW (🇵🇱 波兰·华沙)</s>", "<s>✅ 可用性: 1小时内有货 - 低库存</s>", "在库时长：1分40秒"} {
		if !strings.Contains(text, want) {
			t.Errorf("原地更新内容缺少 %q：%s", want, text)
		}
	}
	raw, err := json.Marshal(markup["inline_keyboard"])
	if err != nil {
		t.Fatalf("marshal keyboard: %v", err)
	}
	if string(raw) != "[]" {
		t.Errorf("所有机房都已下架，键盘必须清空，实际：%s", string(raw))
	}
}

// 上架通知按机房逐颗排列下单按钮（与旧格式一致）：
// 点哪颗就是选哪个机房，点开后走该机房下「配置 → 账户」的实时库存链。
func TestBuildAvailabilityAlertUsesPerDatacenterBuyMenuButtons(t *testing.T) {
	m := renderTestMonitor(t)
	dcs := []map[string]interface{}{
		{"dc": "gra", "raw_status": "24H"},
		{"dc": "fra", "raw_status": "72H"},
		{"dc": "sbg", "raw_status": "24H"},
	}
	_, markup := m.buildAvailabilityAlert("24ska01", dcs, nil, "KS-5", "", "", "")

	raw, err := json.Marshal(markup["inline_keyboard"])
	if err != nil {
		t.Fatalf("marshal keyboard: %v", err)
	}
	var rows [][]struct {
		Text         string `json:"text"`
		CallbackData string `json:"callback_data"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("unmarshal keyboard: %v", err)
	}
	// 每行最多两颗：3 个机房 → 2+1。
	if len(rows) != 2 || len(rows[0]) != 2 || len(rows[1]) != 1 {
		t.Fatalf("3 个机房应排成 2+1 两颗一行，实际：%s", string(raw))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		for _, btn := range row {
			if !strings.Contains(btn.CallbackData, `"a":"menu"`) || !strings.Contains(btn.CallbackData, `"p":"24ska01"`) {
				t.Errorf("按钮回调应携带 menu 动作和 planCode，实际：%s", btn.CallbackData)
			}
			var cb map[string]string
			if err := json.Unmarshal([]byte(btn.CallbackData), &cb); err != nil {
				t.Fatalf("unmarshal callback: %v", err)
			}
			if want := DisplayDatacenterShortName(cb["d"]) + " " + telegramBuyMenuButtonText; btn.Text != want {
				t.Errorf("按钮文案不对：%q，want %q", btn.Text, want)
			}
			seen[cb["d"]] = true
		}
	}
	for _, dc := range []string{"gra", "fra", "sbg"} {
		if !seen[dc] {
			t.Errorf("缺少机房 %s 的下单按钮", dc)
		}
	}
	for _, dc := range dcs {
		if _, ok := dc["notification_button_id"]; ok {
			t.Errorf("新格式通知不落机房按钮：%v", dc)
		}
	}
}

// 新格式通知下架编辑：按机房状态重建明文回调按钮（未下架保留、已下架移除），
// 不依赖落库按钮。
func TestRenderTelegramNotificationUnavailableRebuildsDatacenterButtons(t *testing.T) {
	snapshot := db.TelegramNotificationSnapshot{
		Session: db.TelegramNotificationSession{PlanCode: "24ska01", MessageText: "🎉 服务器上架通知\n\n📍 数据中心: 2 个机房有货\n   ✅ GRA (🇫🇷 法国·格拉沃利讷) — 24小时内有货\n   ✅ FRA (🇩🇪 德国·法兰克福) — 72小时内有货"},
		Datacenters: []db.TelegramNotificationDatacenter{
			{Datacenter: "gra", LineText: "   ✅ GRA (🇫🇷 法国·格拉沃利讷) — 24小时内有货", OpenedAt: 100, ClosedAt: 225},
			{Datacenter: "fra", LineText: "   ✅ FRA (🇩🇪 德国·法兰克福) — 72小时内有货", OpenedAt: 100},
		},
	}
	text, markup := renderTelegramNotificationUnavailable(snapshot)
	if !strings.Contains(text, "🟡 部分机房已下架") {
		t.Errorf("部分下架标题不对：%s", text)
	}
	raw, err := json.Marshal(markup["inline_keyboard"])
	if err != nil {
		t.Fatalf("marshal keyboard: %v", err)
	}
	var rows [][]struct {
		Text         string `json:"text"`
		CallbackData string `json:"callback_data"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("unmarshal keyboard: %v", err)
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatalf("部分下架只保留未下架机房的按钮，实际：%s", string(raw))
	}
	if got := rows[0][0].Text; got != "🇩🇪 Fra 选择配置下单" {
		t.Errorf("未下架机房按钮文案不对：%q", got)
	}
	if cb := rows[0][0].CallbackData; !strings.Contains(cb, `"a":"menu"`) || !strings.Contains(cb, `"p":"24ska01"`) || !strings.Contains(cb, `"d":"fra"`) {
		t.Errorf("未下架机房按钮回调不对：%s", cb)
	}

	// 全部下架后没有可点机房，键盘清空（无货的事实由下一次新通知反映）。
	snapshot.Datacenters[1].ClosedAt = 240
	text, markup = renderTelegramNotificationUnavailable(snapshot)
	if !strings.Contains(text, "⚫ 服务器已下架") {
		t.Errorf("全下架标题不对：%s", text)
	}
	raw, err = json.Marshal(markup["inline_keyboard"])
	if err != nil {
		t.Fatalf("marshal keyboard: %v", err)
	}
	if string(raw) != "[]" {
		t.Errorf("所有机房都已下架，键盘必须清空，实际：%s", string(raw))
	}
}
