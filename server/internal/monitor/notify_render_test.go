package monitor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/db"
	"github.com/ovh-buy/server/internal/logger"
	"github.com/ovh-buy/server/internal/types"
)

func renderTestMonitor(t *testing.T) *Monitor {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	state := &app.State{
		DB: database,
		Logger: logger.New(filepath.Join(dir, "logs.json"),
			slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))),
	}
	return New(state)
}

// 把用户真正会收到的那条上架通知打印出来。
//
// 通知的排版是这个工具的门面 —— 有货那一刻用户只看得到它，
// 而以前想看一眼长什么样，只能等一次真实补货。
// 跑法：go test ./internal/monitor/ -run TestRenderAvailabilityAlert -v
func TestRenderAvailabilityAlert(t *testing.T) {
	m := renderTestMonitor(t)
	detected := time.Now().Add(-3 * time.Second).Format(time.RFC3339Nano)

	dcs := []map[string]interface{}{
		{"dc": "gra", "detected_time": detected, "duration_text": "历时 2小时14分"},
		{"dc": "rbx", "detected_time": detected},
		{"dc": "sbg", "detected_time": detected, "duration_text": "历时 6天3小时"},
	}
	cfg := map[string]interface{}{
		"display":      "64G / 2x480 SSD",
		"memory":       "64GB DDR4 ECC 2133MHz",
		"storage":      "2x480GB SSD SoftRaid",
		"cached_price": "€69.99/月（含税 €83.99）",
		"options":      []string{"ram-64g-noecc-2133", "softraid-2x480ssd"},
	}

	msg, markup := m.buildAvailabilityAlert("24sk602", dcs, cfg, "KS-LE-B",
		"", "trace-9f2a1c", "cfg-77d0")

	fmt.Println("\n┌─────────── Telegram 上架通知 ───────────")
	for _, line := range splitLines(msg) {
		fmt.Println("│ " + line)
	}
	fmt.Println("├─────────── 按钮 ───────────")
	for _, line := range buttonLines(t, markup) {
		fmt.Println("│ " + line)
	}
	fmt.Println("└────────────────────────────")

	// 关键信息必须在,顺带当回归测试
	for _, must := range []string{"24sk602", "64G / 2x480 SSD", "€69.99", "GRA", "RBX", "SBG"} {
		if !contains(msg, must) {
			t.Errorf("通知里缺少关键信息 %q", must)
		}
	}
}

// 已经有任务在抢同一个型号时，通知里必须提醒 ——
// 补货常连着来好几条通知，对同一台机器按两次就是两笔真实订单。
func TestAlertWarnsAboutExistingQueue(t *testing.T) {
	m := renderTestMonitor(t)
	m.state.QueueMu.Lock()
	m.state.Queue = []types.QueueItem{
		{ID: "q1", PlanCode: "24sk602", Datacenter: "gra", Status: "running"},
		{ID: "q2", PlanCode: "24sk602", Datacenter: "rbx", Status: "pending"},
		{ID: "q3", PlanCode: "24sk602", Datacenter: "sbg", Status: "completed"}, // 已完成不算
		{ID: "q4", PlanCode: "24sk603", Datacenter: "gra", Status: "running"},   // 别的型号不算
	}
	m.state.QueueMu.Unlock()

	dcs := []map[string]interface{}{{"dc": "gra"}}
	msg, _ := m.buildAvailabilityAlert("24sk602", dcs, nil, "KS-LE-B", "", "", "")

	if !contains(msg, "已经有 2 个任务在抢") {
		t.Fatalf("应提醒已有 2 个进行中的任务，实际通知：\n%s", msg)
	}

	fmt.Println("\n┌────── 已在抢时的提醒 ──────")
	for _, line := range splitLines(msg) {
		fmt.Println("│ " + line)
	}
	fmt.Println("└───────────────────────────")
}

// 没有任务在抢时不该凭空冒出这句提醒
func TestAlertNoQueueWarningWhenIdle(t *testing.T) {
	m := renderTestMonitor(t)
	msg, _ := m.buildAvailabilityAlert("24sk602",
		[]map[string]interface{}{{"dc": "gra"}}, nil, "KS-LE-B", "", "", "")
	if contains(msg, "个任务在抢") {
		t.Fatalf("没有进行中的任务时不该有这句提醒：\n%s", msg)
	}
}

func splitLines(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

func contains(h, n string) bool {
	return len(n) == 0 || (len(h) >= len(n) && indexOf(h, n) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// buttonLines 把 inline_keyboard 里每一行按钮渲染成一行文本。
// 键盘里的按钮是函数内的局部类型，直接断言不了，走 JSON 最稳。
func buttonLines(t *testing.T, markup map[string]interface{}) []string {
	t.Helper()
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
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		line := ""
		for _, b := range row {
			line += "[ " + b.Text + " ]  "
		}
		out = append(out, line)
	}
	return out
}
