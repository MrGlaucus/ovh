package purchase

import (
	"encoding/json"
	"testing"
)

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

// 订单级可用列表的选择：paymentMean 必须用列表原值传回 OVH（大小写敏感），
// 归一化匹配只用于"认得出"和查账户级默认 ID；余额类不自动使用。
func TestPickPayableMean(t *testing.T) {
	cases := []struct {
		name        string
		avail       []string
		wantMember  string
		wantAccount string
		wantOK      bool
	}{
		{"标准驼峰", []string{"creditCard"}, "creditCard", "creditCard", true},
		{"大写枚举保留原值", []string{"CREDIT_CARD"}, "CREDIT_CARD", "creditCard", true},
		{"优先级：信用卡先于PayPal", []string{"paypal", "creditCard"}, "creditCard", "creditCard", true},
		{"仅PayPal大写", []string{"PAYPAL"}, "PAYPAL", "paypal", true},
		{"银行账户可自动付", []string{"bankAccount", "ovhAccount"}, "bankAccount", "bankAccount", true},
		{"仅余额类不自动用", []string{"fidelityAccount", "ovhAccount"}, "", "", false},
		{"空列表", nil, "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			member, account, ok := pickPayableMean(c.avail)
			if member != c.wantMember || account != c.wantAccount || ok != c.wantOK {
				t.Fatalf("pickPayableMean(%v) = (%q, %q, %v), 期望 (%q, %q, %v)",
					c.avail, member, account, ok, c.wantMember, c.wantAccount, c.wantOK)
			}
		})
	}
}

// 订单可用列表的宽容解析：官方对象数组为主，null 条目跳过（旧文档：null 表示
// 该方式不可用），字符串数组形态兼容。
func TestParseAvailableMeans(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"官方对象数组", `[{"paymentMean":"bankAccount"}]`, []string{"bankAccount"}},
		{"多方式保持顺序", `[{"paymentMean":"creditCard"},{"paymentMean":"paypal"}]`, []string{"creditCard", "paypal"}},
		{"null 条目与空值跳过", `[{"paymentMean":"creditCard"},null,{"paymentMean":null}]`, []string{"creditCard"}},
		{"字符串数组兼容", `["creditCard","paypal"]`, []string{"creditCard", "paypal"}},
		{"空响应 null", `null`, nil},
		{"空数组", `[]`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var raw []json.RawMessage
			if err := json.Unmarshal([]byte(c.raw), &raw); err != nil {
				t.Fatalf("测试数据 JSON 非法: %v", err)
			}
			got := parseAvailableMeans(raw)
			if len(got) != len(c.want) {
				t.Fatalf("parseAvailableMeans(%s) = %v, 期望 %v", c.raw, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("parseAvailableMeans(%s) = %v, 期望 %v", c.raw, got, c.want)
				}
			}
		})
	}
}
