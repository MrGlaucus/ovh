package telegram

import (
	"encoding/json"
	"testing"
)

// update_id 从 JSON 解出来是 float64。解错就等于 offset 不推进 ——
// 那会让同一批 update 被无限重放,每一轮都重新下一次单。
func TestParseUpdateIDAny(t *testing.T) {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(`{"update_id": 123456789}`), &m); err != nil {
		t.Fatal(err)
	}
	if got := parseUpdateIDAny(m["update_id"]); got != 123456789 {
		t.Fatalf("JSON 里的 update_id 应解成 123456789，实际 %d", got)
	}
	if got := parseUpdateIDAny(int64(42)); got != 42 {
		t.Fatalf("int64 应原样返回，实际 %d", got)
	}
	if got := parseUpdateIDAny("nope"); got != 0 {
		t.Fatalf("非数字应返回 0，实际 %d", got)
	}
	if got := parseUpdateIDAny(nil); got != 0 {
		t.Fatalf("nil 应返回 0，实际 %d", got)
	}
}
