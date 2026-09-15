package monitor

import "testing"

// subscriptionWantsConfig 决定"只盯指定配置"的订阅里哪些配置参与检查。
// 判错的后果是双向的:错放会让用户指定的配置被放大成"每套配置各抢一台",
// 错杀则会让订阅静默失效 —— 两种都不能接受,所以每个分支都要有用例钉住。
func TestSubscriptionWantsConfig(t *testing.T) {
	union := map[string]bool{"mem64": true, "mem32": true, "sto2": true, "sto4": true}
	cases := []struct {
		name      string
		want      []string
		cfg       []string
		union     map[string]bool
		segmented bool
		expect    bool
	}{
		{"空选择 = 盯全部配置", nil, []string{"mem64", "sto2"}, union, true, true},
		{"清空后保存的空数组 = 盯全部配置", []string{}, []string{"mem64", "sto2"}, union, true, true},
		{"勾中的配置完全匹配才算命中", []string{"mem64", "sto2"}, []string{"mem64", "sto2"}, union, true, true},
		{"内存同、存储异,不命中", []string{"mem64", "sto2"}, []string{"mem64", "sto4"}, union, true, false},
		{"只勾内存,同内存的每种存储组合都命中", []string{"mem64"}, []string{"mem64", "sto4"}, union, true, true},
		{"勾了带宽等非配置维度,不影响配置匹配", []string{"mem64", "bw500"}, []string{"mem64", "sto2"}, union, true, true},
		{"只勾非配置维度 = 不限配置", []string{"bw500"}, []string{"mem64"}, union, true, true},
		{"目录故障下的多配置机型:宁可不触发也不放大成全盯", []string{"mem64"}, nil, map[string]bool{}, true, false},
		{"裸机型(无带段 FQN)没有配置维度,不受限", []string{"mem64"}, nil, map[string]bool{}, false, true},
		{"选项全部不在目录里,降级为不限配置", []string{"old-code"}, []string{"mem64"}, union, true, true},
	}
	for _, tc := range cases {
		if got := subscriptionWantsConfig(tc.want, tc.cfg, tc.union, tc.segmented); got != tc.expect {
			t.Errorf("%s: subscriptionWantsConfig(%v, %v) = %v, 期望 %v",
				tc.name, tc.want, tc.cfg, got, tc.expect)
		}
	}
}
