package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ovh-buy/server/internal/catalog"
	"github.com/ovh-buy/server/internal/db"
)

func TestTelegramMenuCommandMatching(t *testing.T) {
	for _, text := range []string{"/buy", "/buy@ovh_bot", " /BUY@ovh_bot "} {
		if !matchesTelegramCommand(text, "buy") {
			t.Fatalf("%q 应匹配 /buy", text)
		}
	}
	for _, text := range []string{"/buyer", "/buy now", "buy"} {
		if matchesTelegramCommand(text, "buy") {
			t.Fatalf("%q 不应匹配 /buy", text)
		}
	}
}

func TestTelegramListTimesAlwaysUseChinaTime(t *testing.T) {
	if got, want := formatTelegramTime("2026-09-10T10:36:00Z"), "09-10 18:36"; got != want {
		t.Fatalf("带 UTC 时区的检查时间 = %q，期望 %q", got, want)
	}
	unix := float64(time.Date(2026, 9, 10, 10, 36, 0, 0, time.UTC).Unix())
	if got, want := formatTelegramUnixTime(unix), "09-10 18:36"; got != want {
		t.Fatalf("队列 Unix 检查时间 = %q，期望 %q", got, want)
	}
}

func TestResolveTelegramOrderOptionsUsesExactConfigKey(t *testing.T) {
	configs := map[string]*catalog.ConfigAvailability{
		// 用户点按钮时选中的配置（ram-32）：下单时已下架 —— 对应"标准配置下架"。
		"24sk202.ram-32.storage-2x2tb": {
			Options:     []string{"ram-32", "storage-2x2tb"},
			Datacenters: map[string]string{"bhs": "unavailable"},
		},
		// 窗口期里补货的另一套配置（ram-64）：旧逻辑会把订单"反推"到这套。
		"24sk202.ram-64.storage-2x2tb": {
			Options:     []string{"ram-64", "storage-2x2tb"},
			Datacenters: map[string]string{"bhs": "24H"},
		},
	}
	// 精确 FQN 有货：必须返回该 FQN 实时匹配出的 options，而不是按钮里的旧快照 ——
	// 目录变更后同一 FQN 的 addon 可能已变，用旧快照下单就是配置串号。
	options, ok := resolveTelegramOrderOptions(configs, "24sk202.ram-64.storage-2x2tb", "bhs", []string{"stale-option"})
	if !ok || !sameTelegramOptions(options, []string{"ram-64", "storage-2x2tb"}) {
		t.Fatalf("有货的精确 FQN 配置应返回实时 options，实际 ok=%v options=%v", ok, options)
	}
	// 用户场景回归：按钮选的是 ram-32，但下单时该配置已下架、ram-64 补货 ——
	// 必须拒绝，绝不能借用另一配置的库存下单。
	if _, ok := resolveTelegramOrderOptions(configs, "24sk202.ram-32.storage-2x2tb", "bhs", nil); ok {
		t.Fatal("无货 FQN 不得借用其它配置库存")
	}
	// 按钮里的 FQN 在当前目录查不到（配置下架/目录变更）：拒绝。
	if _, ok := resolveTelegramOrderOptions(configs, "24sk202.gone", "bhs", nil); ok {
		t.Fatal("按钮里的 FQN 在当前目录查不到时必须拒绝")
	}
	// 旧按钮（无 config_key）：按完整 options 集合回退匹配，并返回实时 options。
	options, ok = resolveTelegramOrderOptions(configs, "", "bhs", []string{"storage-2x2tb", "ram-64"})
	if !ok || !sameTelegramOptions(options, []string{"ram-64", "storage-2x2tb"}) {
		t.Fatalf("旧按钮应按完整 options 集合回退匹配，实际 ok=%v options=%v", ok, options)
	}
	if _, ok := resolveTelegramOrderOptions(configs, "", "bhs", []string{"ram-32", "storage-2x2tb"}); ok {
		t.Fatal("无货 options 组合不得允许下单")
	}
	// 空身份：旧按钮没有 options 时必须拒绝。以前 sameTelegramOptions([], []) == true，
	// 空 vs 空能匹配上任何"options 空"的配置，旧按钮借此无视配置直接下单。
	if _, ok := resolveTelegramOrderOptions(configs, "", "bhs", nil); ok {
		t.Fatal("旧按钮空 options 必须拒绝，不得匹配空配置")
	}
	configsWithEmpty := map[string]*catalog.ConfigAvailability{
		"24sk202.bare": {Options: nil, Datacenters: map[string]string{"bhs": "24H"}},
	}
	if _, ok := resolveTelegramOrderOptions(configsWithEmpty, "", "bhs", nil); ok {
		t.Fatal("空 options 配置不得与空按钮匹配")
	}
	// 分段 FQN 匹配不出 addon：配置身份无法核定，按钮即使带 config_key 也必须拒绝。
	if _, ok := resolveTelegramOrderOptions(configsWithEmpty, "24sk202.bare", "bhs", nil); ok {
		t.Fatal("分段 FQN 匹配不出 addon 时必须拒绝")
	}
	// 裸 planCode 机型（FQN 无 addon 段）整机唯一配置：options 为空是正常形态，
	// 有货时必须放行 —— 与 purchase/queue 的 availabilityHasSegmentedFQN 同一口径。
	bareConfigs := map[string]*catalog.ConfigAvailability{
		"24rise01-v1": {Options: nil, Datacenters: map[string]string{"bhs": "24H"}},
	}
	options, ok = resolveTelegramOrderOptions(bareConfigs, "24rise01-v1", "bhs", nil)
	if !ok || len(options) != 0 {
		t.Fatalf("裸 planCode 机型有货必须放行且 options 为空，实际 ok=%v options=%v", ok, options)
	}
	if _, ok := resolveTelegramOrderOptions(bareConfigs, "24rise01-v1", "xyz", nil); ok {
		t.Fatal("裸 planCode 机型机房无货时必须拒绝")
	}
}

// buy_menu 状态与 config_key 共用同一个 ConfigInfo JSON：两个读者各自只解自己关心的
// 字段，不能被对方挤掉 —— 菜单链路的每一级按钮都要能同时取回两者。
func TestTelegramButtonConfigKeyCoexistsWithBuyMenu(t *testing.T) {
	raw := `{"buy_menu":{"display":"24sk202","candidates":[{"account_id":"acc-1","config_key":"24sk202.ram-32.storage-2x2tb","options":["ram-32"],"datacenters":["bhs"]}]},"config_key":"24sk202.ram-32.storage-2x2tb"}`
	if got := telegramButtonConfigKey(raw); got != "24sk202.ram-32.storage-2x2tb" {
		t.Fatalf("config_key 解析 = %q, 期望 24sk202.ram-32.storage-2x2tb", got)
	}
	menu, err := readBuyMenuState(db.TelegramButtonRow{ConfigInfo: raw})
	if err != nil {
		t.Fatalf("读取菜单状态失败: %v", err)
	}
	if menu.Display != "24sk202" || len(menu.Candidates) != 1 {
		t.Fatalf("buy_menu 与 config_key 共存解析失败: %+v", menu)
	}
	if got := menu.Candidates[0].ConfigKey; got != "24sk202.ram-32.storage-2x2tb" {
		t.Fatalf("候选 config_key = %q, 期望 24sk202.ram-32.storage-2x2tb", got)
	}
}

func TestTelegramAccountChoiceCallbackDataFitsLimit(t *testing.T) {
	id := uuid.NewString()
	for action := range map[string]struct{}{
		"add_to_queue": {},
		"back":         {},
		"fav":          {},
		"cfg":          {},
		"bm":           {},
		"dc":           {},
		"bc":           {},
		"bd":           {},
	} {
		data, err := json.Marshal(map[string]string{"a": action, "u": id})
		if err != nil {
			t.Fatalf("marshal %q callback: %v", action, err)
		}
		if len(data) > 64 {
			t.Fatalf("%q callback is %d bytes, Telegram accepts at most 64: %s", action, len(data), data)
		}
	}
}
