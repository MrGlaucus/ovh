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
