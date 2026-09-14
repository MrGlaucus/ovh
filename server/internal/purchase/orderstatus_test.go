package purchase

import "testing"

// 付款前解析默认支付方式的选择逻辑。资金操作，绝不擅自替用户在多张卡里挑：
// 默认卡必须"标记 default 且状态 valid"；没有默认标记时仅当唯一有效候选才可用；
// 多个有效候选且无默认 → 返回 0 报错让用户去 OVH 面板设置。
func TestSelectPreferredPaymentMean(t *testing.T) {
	cases := []struct {
		name  string
		items []paymentMeanItem
		want  int64
	}{
		{"默认且有效", []paymentMeanItem{
			{ID: 11, State: "valid"},
			{ID: 22, Default: true, State: "valid"},
		}, 22},
		{"默认卡已过期，唯一有效卡兜底", []paymentMeanItem{
			{ID: 11, Default: true, State: "expired"},
			{ID: 22, State: "valid"},
		}, 22},
		{"唯一有效卡（无默认标记）", []paymentMeanItem{
			{ID: 33, State: "valid"},
			{ID: 44, State: "expired"},
		}, 33},
		{"多张有效但无默认标记", []paymentMeanItem{
			{ID: 11, State: "valid"},
			{ID: 22, State: "valid"},
		}, 0},
		{"全部过期", []paymentMeanItem{
			{ID: 11, Default: true, State: "expired"},
			{ID: 22, State: "expired"},
		}, 0},
		{"只有一张默认有效的卡", []paymentMeanItem{{ID: 77, Default: true, State: "valid"}}, 77},
		{"空列表", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := selectPreferredPaymentMean(c.items); got != c.want {
				t.Fatalf("selectPreferredPaymentMean(%v) = %d, 期望 %d", c.items, got, c.want)
			}
		})
	}
}
