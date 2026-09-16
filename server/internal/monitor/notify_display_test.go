package monitor

import (
	"testing"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/types"
)

func TestConfigMemoryStorage(t *testing.T) {
	if mem, stor := configMemoryStorage(nil); mem != "" || stor != "" {
		t.Fatalf("nil configInfo 应返回空: %q / %q", mem, stor)
	}
	mem, stor := configMemoryStorage(map[string]interface{}{
		"memory":  "ram-64g-ecc-2133",
		"storage": "softraid-4x2000sa",
	})
	if mem != "64GB ECC RAM-2133" || stor != "4×2TB SA" {
		t.Fatalf("转换结果不符: %q / %q", mem, stor)
	}
	// 值不是 string(历史脏数据)时不能 panic
	if mem, stor := configMemoryStorage(map[string]interface{}{"memory": nil}); mem != "" || stor != "" {
		t.Fatalf("非字符串值应返回空: %q / %q", mem, stor)
	}
}

// 上架通知的内存 / 存储展示:裸 addon FQN 转人读文案,已是人读文本的值原样保留。
func TestHumanMemoryText(t *testing.T) {
	cases := map[string]string{
		"ram-64g-ecc-2133":    "64GB ECC RAM-2133",
		"ram-128g-noecc-2933": "128GB RAM-2933",
		"ram-32g-ecc-2400":    "32GB ECC RAM-2400",
		"32GB ECC DDR4-2400":  "32GB ECC DDR4-2400",
		"N/A":                 "N/A",
		"":                    "",
	}
	for in, want := range cases {
		if got := humanMemoryText(in); got != want {
			t.Errorf("humanMemoryText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanStorageText(t *testing.T) {
	cases := map[string]string{
		"softraid-4x2000sa":  "4×2TB SA",
		"softraid-2x450nvme": "2×450GB NVMe",
		"softraid-4x960ssd":  "4×960GB SSD",
		"2x2TB HDD":          "2x2TB HDD",
		"":                   "",
	}
	for in, want := range cases {
		if got := humanStorageText(in); got != want {
			t.Errorf("humanStorageText(%q) = %q, want %q", in, got, want)
		}
	}
}

// productName:目录 invoiceName 已含 CPU(如 "KS-2 | Intel Xeon-D 1540")时
// 不再重复拼 CPU;纯型号名的旧订阅照常拼。
func TestProductNameCPUDedup(t *testing.T) {
	state := &app.State{
		ServerPlans: []types.ServerPlan{
			{PlanCode: "24sk202", Name: "KS-2 | Intel Xeon-D 1540", CPU: "Intel Xeon-D 1540"},
			{PlanCode: "24rise01", Name: "RISE-1", CPU: "RISE系列专用CPU"},
		},
	}
	m := New(state)

	cases := []struct {
		planCode string
		server   string
		want     string
	}{
		// 订阅存的就是含 CPU 的完整对外名(现网 SK 系列订阅的真实形态)
		{"24sk202", "KS-2 | Intel Xeon-D 1540", "KS-2 | Intel Xeon-D 1540"},
		// 订阅没存名字时回退目录 invoiceName,同样不能重复
		{"24sk202", "", "KS-2 | Intel Xeon-D 1540"},
		// 纯型号名的旧订阅照常拼 CPU
		{"24rise01", "RISE-1", "RISE-1 | RISE系列专用CPU"},
		// 目录里没有该 planCode:退回订阅名
		{"nope", "X-99", "X-99"},
		// 什么都没有:退回 planCode
		{"nope", "", "nope"},
	}
	for _, c := range cases {
		if got := m.productName(c.planCode, c.server); got != c.want {
			t.Errorf("productName(%q, %q) = %q, want %q", c.planCode, c.server, got, c.want)
		}
	}
}
