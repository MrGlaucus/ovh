package monitor

import (
	"fmt"
	"time"

	"github.com/ovh-buy/server/internal/types"
)

// Called before appending the current event. Freeze the reference in the outbox
// so a delayed/retried notification cannot mistake itself for a previous restock.
func previousRestock(history []HistoryEntry, dc, configKey, detected string, now time.Time) string {
	current, err := time.Parse(time.RFC3339Nano, detected)
	if err != nil {
		current = now
	}
	var latest time.Time
	for _, entry := range history {
		key, _ := entry.Config["config_key"].(string)
		if configKey == "" || key != configKey || entry.Datacenter != dc || entry.ChangeType != "available" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err == nil && at.Before(current) && at.After(latest) {
			latest = at
		}
	}
	if latest.IsZero() {
		return "暂无更早补货记录"
	}
	delta := current.Sub(latest)
	var ago string
	switch {
	case delta >= 24*time.Hour:
		ago = fmt.Sprintf("%d天前", int(delta.Hours()/24))
	case delta >= time.Hour:
		ago = fmt.Sprintf("%d小时前", int(delta.Hours()))
	case delta >= time.Minute:
		ago = fmt.Sprintf("%d分钟前", int(delta.Minutes()))
	default:
		ago = "不足1分钟前"
	}
	return "上次补货：" + ago + "（" + latest.In(types.NowChina().Location()).Format("2006-01-02 15:04:05") + "）"
}
