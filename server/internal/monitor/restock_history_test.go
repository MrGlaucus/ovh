package monitor

import (
	"strings"
	"testing"
	"time"
)

func TestPreviousRestockMatchesExactConfigAndDC(t *testing.T) {
	now := time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC)
	entry := func(at time.Time, dc, key, change string) HistoryEntry {
		return HistoryEntry{Timestamp: at.Format(time.RFC3339Nano), Datacenter: dc, ChangeType: change, Config: map[string]interface{}{"config_key": key}}
	}
	history := []HistoryEntry{
		entry(now.Add(-5*24*time.Hour), "gra", "plan.ram64", "available"),
		entry(now.Add(-3*24*time.Hour), "gra", "plan.ram64", "available"),
		entry(now.Add(-time.Hour), "gra", "plan.ram32", "available"),
		entry(now.Add(-time.Hour), "bhs", "plan.ram64", "available"),
		entry(now.Add(-time.Hour), "gra", "plan.ram64", "unavailable"),
		entry(now, "gra", "plan.ram64", "available"),
	}
	got := previousRestock(history, "gra", "plan.ram64", now.Format(time.RFC3339Nano), now.Add(time.Hour))
	if got != "上次补货：3天前（2026-10-06 12:00:00）" {
		t.Fatal(got)
	}
	if got := previousRestock(history, "gra", "unknown", "", now); got != "暂无更早补货记录" {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		delta time.Duration
		want  string
	}{{2 * time.Hour, "2小时前"}, {15 * time.Minute, "15分钟前"}, {30 * time.Second, "不足1分钟前"}} {
		got := previousRestock([]HistoryEntry{entry(now.Add(-tc.delta), "gra", "plan.ram64", "available")}, "gra", "plan.ram64", "", now)
		if !strings.Contains(got, tc.want) {
			t.Fatal(got)
		}
	}
}

func TestRestockHistoryRenderedPerDatacenter(t *testing.T) {
	m := renderTestMonitor(t)
	for _, count := range []int{1, 2} {
		dcs := []map[string]interface{}{{"dc": "gra", "raw_status": "1H-high", "previous_restock": "上次补货：3天前（2026-10-06 12:00:00）"}, {"dc": "bhs", "raw_status": "1H-high", "previous_restock": "暂无更早补货记录"}}
		msg, _ := m.buildAvailabilityAlert("plan", dcs[:count], nil, "KS-2", "", "", "")
		if !strings.Contains(msg, "上次补货：3天前") {
			t.Fatal(msg)
		}
		if count == 2 && !strings.Contains(msg, "暂无更早补货记录") {
			t.Fatal(msg)
		}
		for _, dc := range dcs[:count] {
			line, _ := dc["notification_line"].(string)
			if !strings.Contains(line, dc["previous_restock"].(string)) {
				t.Fatal("delisting anchor lost history line")
			}
		}
	}
}
