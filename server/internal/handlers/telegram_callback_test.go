package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ovh-buy/server/internal/catalog"
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

func TestTelegramOrderTargetAvailableUsesExactConfigKey(t *testing.T) {
	configs := map[string]*catalog.ConfigAvailability{
		"24sk202.ram-32.storage-2x2tb": {
			Options:     []string{"ram-32", "storage-2x2tb"},
			Datacenters: map[string]string{"bhs": "24H"},
		},
		"24sk202.ram-64.storage-2x2tb": {
			Options:     []string{"ram-64", "storage-2x2tb"},
			Datacenters: map[string]string{"bhs": "unavailable"},
		},
	}
	if !telegramOrderTargetAvailable(configs, "24sk202.ram-32.storage-2x2tb", "bhs", []string{"ram-32", "storage-2x2tb"}) {
		t.Fatal("有货的精确 FQN 配置应允许下单")
	}
	if telegramOrderTargetAvailable(configs, "24sk202.ram-64.storage-2x2tb", "bhs", []string{"ram-32", "storage-2x2tb"}) {
		t.Fatal("配置 key 指向无货配置时不得借用另一配置的库存")
	}
	if !telegramOrderTargetAvailable(configs, "", "bhs", []string{"storage-2x2tb", "ram-32"}) {
		t.Fatal("旧按钮应按完整 options 集合回退匹配")
	}
	if telegramOrderTargetAvailable(configs, "", "bhs", []string{"ram-64", "storage-2x2tb"}) {
		t.Fatal("无货 options 组合不得允许下单")
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
