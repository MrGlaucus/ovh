package monitor

import (
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

func TestWriteNotificationTimingKeepsUsefulTimingFields(t *testing.T) {
	var msg strings.Builder
	m := &Monitor{}
	m.writeNotificationTiming(&msg, "2026-09-10T10:00:00Z", time.Date(2026, 9, 10, 10, 1, 5, 0, time.UTC))
	text := msg.String()
	for _, want := range []string{"检测时间：", "推送时间：", "推送延迟：1分5秒"} {
		if !strings.Contains(text, want) {
			t.Errorf("通知时间区块缺少 %q：%s", want, text)
		}
	}
}

func TestRenderTelegramNotificationUnavailableStrikesOnlyClosedDatacenter(t *testing.T) {
	snapshot := db.TelegramNotificationSnapshot{
		Session: db.TelegramNotificationSession{MessageText: "🟢 服务器现货\n\n📍 可下单机房（2 个）\n  • 🇫🇷 法国·格拉沃利讷 (GRA)\n  • 🇩🇪 德国·法兰克福 (FRA)"},
		Datacenters: []db.TelegramNotificationDatacenter{
			{Datacenter: "gra", LineText: "  • 🇫🇷 法国·格拉沃利讷 (GRA)", ButtonID: "gra-id", ButtonText: "🇫🇷 Gra 一键下单", OpenedAt: 100, ClosedAt: 225},
			{Datacenter: "fra", LineText: "  • 🇩🇪 德国·法兰克福 (FRA)", ButtonID: "fra-id", ButtonText: "🇩🇪 Fra 一键下单", OpenedAt: 100},
		},
	}
	text, markup := renderTelegramNotificationUnavailable(snapshot)
	for _, want := range []string{"🟡 部分机房已下架", "<s>🇫🇷 法国·格拉沃利讷 (GRA)</s>", "在库时长：2分5秒", "🇩🇪 德国·法兰克福 (FRA)"} {
		if !strings.Contains(text, want) {
			t.Errorf("原地更新内容缺少 %q：%s", want, text)
		}
	}
	if markup["inline_keyboard"] == nil {
		t.Fatal("仍在库的机房必须保留可用下单按钮")
	}
}
