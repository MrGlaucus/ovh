package telegram

import (
	"strings"
	"testing"
)

// 中文输入法与"写坏的数量"这两类小白输入的静默失效(ccf8f94)。
//
// 这些写法以前全都**静默**失效:任务照样建、照样下单,只是配置悄悄没了 ——
// 用户唯一的反馈是"TG 上下单总是选不了配置"。没有报错、日志里也看不出异常。
//
// 覆盖范围:全角标点(半角/全角逗号、顿号、分号)与 -1 / +2 / 3.5 这类
// 意图是数量、只是写得不合法的串。位置无关解析(空格分隔配置、大写机房等)
// 不在此文件 —— 那是另一条上游 TG 提交的功能,本 fork 未合并。

// 中文输入法打出来的是全角标点。用户在手机上发 `24ska01 gra 2 ram-64g，softraid-2x960ssd`
// 时,以前会把整串配置当成**一个** addon planCode ——
// 匹配不上任何东西,而且不报错:单照下,只是配置悄悄没了。
func TestParseOrderMessageAcceptsFullWidthPunctuation(t *testing.T) {
	cases := []string{
		"24ska01 gra 2 ram-64g，softraid-2x960ssd", // 全角逗号
		"24ska01 gra 2 ram-64g、softraid-2x960ssd", // 顿号
		"24ska01　gra　2　ram-64g，softraid-2x960ssd", // 全角空格 + 全角逗号
	}
	for _, in := range cases {
		got := ParseOrderMessage(in)
		if got == nil {
			t.Errorf("%q 解析失败", in)
			continue
		}
		if got.Datacenter != "gra" || got.Quantity != 2 {
			t.Errorf("%q → dc=%q qty=%d,期望 gra/2", in, got.Datacenter, got.Quantity)
		}
		if strings.Join(got.Options, ",") != "ram-64g,softraid-2x960ssd" {
			t.Errorf("%q → opts=%v,期望两项拆开", in, got.Options)
		}
	}
}

// 数量写成 -1 / 3.5 这种:意图是数量、只是写得不合法,不能当成配置项发给 OVH
// (换回来的是一句用户看不懂的英文报错)。
func TestParseOrderMessageMalformedQuantityIsNotAnOption(t *testing.T) {
	for _, in := range []string{"24ska01 gra -1", "24ska01 gra +2", "24ska01 gra 3.5"} {
		got := ParseOrderMessage(in)
		if len(got.Options) != 0 {
			t.Errorf("%q → 写坏的数量落进了 options: %v", in, got.Options)
		}
		if got.Datacenter != "gra" {
			t.Errorf("%q → 机房被带偏: %q", in, got.Datacenter)
		}
	}
}

// 坏数量混在逗号分隔的配置串里。这条是本 fork 解析结构下 isMalformedQuantity
// 真正会拦到的路径:`ram-64g，-1` 要拆成两项,-1 被忽略,ram-64g 留下。
func TestParseOrderMessageMalformedQuantityInOptionsIgnored(t *testing.T) {
	got := ParseOrderMessage("24ska01 gra ram-64g，-1")
	if got.Datacenter != "gra" {
		t.Errorf("机房被带偏: %q", got.Datacenter)
	}
	if strings.Join(got.Options, ",") != "ram-64g" {
		t.Errorf("期望只留 ram-64g,实际 %v", got.Options)
	}
}
