package monitor

import "testing"

// 仅配置集合完全一致才匹配。
func TestQueueTaskTargetsConfig(t *testing.T) {
	cases := []struct {
		name         string
		task, config []string
		want         bool
	}{
		{"任务锁定整套配置", []string{"ram-64g-ecc-2133", "softraid-4x2000sa"}, []string{"ram-64g-ecc-2133", "softraid-4x2000sa"}, true},
		{"任务比本套多勾(带宽等不入FQN的项)", []string{"ram-64g-ecc-2133", "softraid-4x2000sa", "bandwidth-1g"}, []string{"ram-64g-ecc-2133", "softraid-4x2000sa"}, false},
		{"任务只勾内存(任意存储都抢)", []string{"ram-64g-ecc-2133"}, []string{"ram-64g-ecc-2133", "softraid-4x2000sa"}, false},
		{"任务盯别的内存", []string{"ram-128g-ecc-2400"}, []string{"ram-64g-ecc-2133", "softraid-4x2000sa"}, false},
		{"任务盯别的存储", []string{"ram-64g-ecc-2133", "softraid-4x6000sa"}, []string{"ram-64g-ecc-2133", "softraid-4x2000sa"}, false},
		{"裸机型任务(options为空)", nil, []string{"ram-64g-ecc-2133"}, false},
		{"目录缺数据(configOptions为空)", []string{"ram-64g-ecc-2133"}, nil, false},
	}
	for _, c := range cases {
		if got := queueTaskTargetsConfig(c.task, c.config); got != c.want {
			t.Errorf("%s: queueTaskTargetsConfig(%v, %v) = %v, want %v", c.name, c.task, c.config, got, c.want)
		}
	}
}
