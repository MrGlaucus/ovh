package monitor

import "testing"

// isQuietTransition:有货 ↔ 价格校验失败 属于同一波有货的抖动,通知侧静默
// (状态推进与下单不受影响);其余跳变照常通知。
//
// 背景:OVH 抖动时状态 available → price_check_failed → available 来回跳,
// 每次恢复以前都发「🎉 上架」,用户收到一串"反复上下架"而库存从未变过。
func TestIsQuietTransition(t *testing.T) {
	cases := []struct {
		name        string
		old, actual string
		quiet       bool
	}{
		{"从有货掉进校验失败:抖动,静默", "available", "price_check_failed", true},
		{"校验恢复:不是新上架,静默", "price_check_failed", "available", true},
		{"从无货变有货:真上架,通知", "unavailable", "available", false},
		{"从无货进入有货但校验失败:真实变化,通知", "unavailable", "price_check_failed", false},
		{"从校验失败变无货:真下架,通知", "price_check_failed", "unavailable", false},
		{"从有货变无货:真下架,通知", "available", "unavailable", false},
		{"首次检查有货:通知", "", "available", false},
		{"首次检查校验失败:通知", "", "price_check_failed", false},
		{"没跳变,不涉静默", "available", "available", false},
	}
	for _, c := range cases {
		if got := isQuietTransition(c.old, c.actual); got != c.quiet {
			t.Errorf("%s: isQuietTransition(%q, %q) = %v, want %v", c.name, c.old, c.actual, got, c.quiet)
		}
	}
}
