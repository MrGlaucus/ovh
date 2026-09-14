package purchase

import "testing"

// availabilityHasSegmentedFQN 的判定边界：FQN 带 addon 段（含 "."）才算"多配置机型"。
// 裸 planCode / 空 fqn / 无记录 都按"没有配置可选"处理 —— 判定只用于拒绝
// "空 options + 多配置"的任务，绝不能拦住裸 planCode 机型的正常下单。
func TestAvailabilityHasSegmentedFQN(t *testing.T) {
	cases := []struct {
		name string
		avs  []map[string]interface{}
		want bool
	}{
		{"裸机型只有 planCode", []map[string]interface{}{{"fqn": "24rise01-v1"}}, false},
		{"分段 FQN 是多配置机型", []map[string]interface{}{{"fqn": "24sk602.ram-128g-ecc-2400.softraid-2x1000nvme"}}, true},
		{"混合集合里任一分段即算", []map[string]interface{}{
			{"fqn": "24rise02-v1"},
			{"fqn": "24sk50.softraid-2x960nvme"},
		}, true},
		{"无记录", nil, false},
		{"fqn 缺失或非字符串", []map[string]interface{}{{}, {"fqn": 42}}, false},
	}
	for _, tc := range cases {
		if got := availabilityHasSegmentedFQN(tc.avs); got != tc.want {
			t.Fatalf("%s: got=%v want=%v", tc.name, got, tc.want)
		}
	}
}
