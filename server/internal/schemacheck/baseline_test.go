package schemacheck

// OVH 接口漂移基线。
//
// 由来:用户反复问"OVH 的接口是不是改了",而每次都靠人工拉 schema、人工比对,
// 既慢又容易漏。这里把"代码在用的每个端点的签名"固化成基线文件,
// 用一条联网测试跟 OVH 最新 schema 对比 —— 变了就红,并指出变在哪。
//
// 覆盖的是**签名**(是否存在 / apiStatus / 参数名+类型+必填 / 响应类型),
// 不是全量 schema:全量 4.8MB 入库没意义,而且噪声大到没人会去看 diff。
//
// 默认跳过(需要联网、且 OVH 偶尔抽风)。跑法:
//
//	go test ./internal/schemacheck/ -run TestOVHSchemaDrift -v -tags=netcheck
//
// 基线更新(确认变化是良性之后):
//
//	go test ./internal/schemacheck/ -run TestOVHSchemaDrift -tags=netcheck -update

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "把当前 OVH schema 写成新基线")

// regions 三个站点各自的 API 根。三区是独立系统,同一个端点可能只在其中两个存在。
var regions = map[string]string{
	"EU": "https://eu.api.ovh.com/1.0",
	"US": "https://api.us.ovhcloud.com/1.0",
	"CA": "https://ca.api.ovh.com/1.0",
}

// namespaces 项目实际用到的命名空间
var namespaces = []string{
	"dedicated/server",
	"dedicated/installationTemplate",
	"vps",
	"order",
	"me",
	"services",
	"ip",
}

// endpointSig 一个端点在某个区的签名。字段都是"变了就该有人看一眼"的那些。
type endpointSig struct {
	Status       string   `json:"status"`                // PRODUCTION / BETA / DEPRECATED / ALPHA
	Deprecated   string   `json:"deprecated,omitempty"`  // 废弃日期
	Deletion     string   `json:"deletion,omitempty"`    // 删除日期
	Replacement  string   `json:"replacement,omitempty"` // OVH 指定的替代端点
	ResponseType string   `json:"response,omitempty"`    // 响应模型
	Params       []string `json:"params,omitempty"`      // paramType:name:dataType:required
}

// baseline 形状: {"GET /vps/{serviceName}": {"EU": sig, "CA": sig}}
type baseline map[string]map[string]endpointSig

func baselinePath() string { return filepath.Join("testdata", "ovh-endpoints.json") }

// usedEndpoints 代码在用的端点。手工维护:自动扫路径拼接不可靠(路径是 "+svc+" 拼出来的),
// 而漏掉一个端点比多写一个危险得多 —— 多写只是多一条噪声,漏掉就是漂移无人发现。
// 加新端点时把它加进来。
var usedEndpoints = []string{
	// —— 独服 ——
	"GET /dedicated/server",
	"GET /dedicated/server/{serviceName}",
	"PUT /dedicated/server/{serviceName}",
	"GET /dedicated/server/{serviceName}/serviceInfos",
	"PUT /dedicated/server/{serviceName}/serviceInfos",
	"GET /dedicated/server/{serviceName}/specifications/hardware",
	"GET /dedicated/server/{serviceName}/specifications/network",
	"POST /dedicated/server/{serviceName}/reboot",
	"POST /dedicated/server/{serviceName}/reinstall",
	"GET /dedicated/server/{serviceName}/install/status",
	"GET /dedicated/server/{serviceName}/boot",
	"GET /dedicated/server/{serviceName}/boot/{bootId}",
	"GET /dedicated/server/{serviceName}/task",
	"GET /dedicated/server/{serviceName}/task/{taskId}",
	"POST /dedicated/server/{serviceName}/task/{taskId}/cancel",
	"GET /dedicated/server/{serviceName}/task/{taskId}/availableTimeslots",
	"POST /dedicated/server/{serviceName}/task/{taskId}/schedule",
	"POST /dedicated/server/{serviceName}/terminate",
	"POST /dedicated/server/{serviceName}/confirmTermination",
	"POST /dedicated/server/{serviceName}/changeContact",
	"GET /dedicated/server/{serviceName}/intervention",
	"POST /dedicated/server/{serviceName}/support/replace/hardDiskDrive",
	"POST /dedicated/server/{serviceName}/support/replace/memory",
	"POST /dedicated/server/{serviceName}/support/replace/cooling",
	"GET /dedicated/server/{serviceName}/ipmi",
	"POST /dedicated/server/{serviceName}/features/ipmi/access",
	"GET /dedicated/server/{serviceName}/features/ipmi/access",
	"GET /dedicated/server/{serviceName}/features/firewall",
	"PUT /dedicated/server/{serviceName}/features/firewall",
	"GET /dedicated/server/{serviceName}/burst",
	"PUT /dedicated/server/{serviceName}/burst",
	"GET /dedicated/server/{serviceName}/mrtg",
	"GET /dedicated/server/{serviceName}/networkInterfaceController",
	"GET /dedicated/server/{serviceName}/biosSettings",
	"GET /dedicated/server/{serviceName}/spla",
	"POST /dedicated/server/{serviceName}/spla",
	"POST /dedicated/server/{serviceName}/virtualMac",
	"GET /dedicated/server/{serviceName}/vrack",
	"GET /dedicated/server/datacenter/availabilities",
	"GET /dedicated/installationTemplate",
	"GET /dedicated/installationTemplate/{templateName}",
	"GET /dedicated/installationTemplate/{templateName}/partitionScheme",

	// —— VPS ——
	"GET /vps",
	"GET /vps/{serviceName}",
	"PUT /vps/{serviceName}",
	"GET /vps/{serviceName}/serviceInfos",
	"PUT /vps/{serviceName}/serviceInfos",
	"POST /vps/{serviceName}/reboot",
	"POST /vps/{serviceName}/start",
	"POST /vps/{serviceName}/stop",
	"POST /vps/{serviceName}/reinstall",
	"POST /vps/{serviceName}/rebuild",
	"GET /vps/{serviceName}/snapshot",
	"PUT /vps/{serviceName}/snapshot",
	"POST /vps/{serviceName}/createSnapshot",
	"POST /vps/{serviceName}/snapshot/revert",
	"GET /vps/{serviceName}/ips",
	"PUT /vps/{serviceName}/ips/{ipAddress}",
	"POST /vps/{serviceName}/terminate",
	"POST /vps/{serviceName}/confirmTermination",
	"GET /vps/order/rule/datacenter",

	// —— 下单 ——
	"POST /order/cart",
	"DELETE /order/cart/{cartId}",
	"POST /order/cart/{cartId}/assign",
	"GET /order/cart/{cartId}/eco",
	"POST /order/cart/{cartId}/eco",
	"GET /order/cart/{cartId}/eco/options",
	"POST /order/cart/{cartId}/eco/options",
	"GET /order/cart/{cartId}/vps",
	"POST /order/cart/{cartId}/vps",
	"GET /order/cart/{cartId}/item/{itemId}/requiredConfiguration",
	"POST /order/cart/{cartId}/item/{itemId}/configuration",
	"GET /order/cart/{cartId}/summary",
	"POST /order/cart/{cartId}/checkout",
	"GET /order/catalog/public/eco",
	"GET /order/catalog/public/vps",

	// —— 账户 / 服务 ——
	"GET /me",
	"GET /me/bill",
	"GET /me/order",
	"GET /me/order/{orderId}",
	"GET /me/order/{orderId}/status",
	"GET /services/{serviceId}",
	"PUT /services/{serviceId}",
	"GET /services/{serviceId}/billing/engagement",
	"GET /services/{serviceId}/billing/engagement/available",
	"POST /services/{serviceId}/billing/engagement/request",
	"PUT /services/{serviceId}/billing/engagement/endRule",

	// —— IP ——
	"GET /ip",
	"GET /ip/{ip}",
	"GET /ip/{ip}/reverse",
	"POST /ip/{ip}/reverse",
	"GET /ip/{ip}/mitigation",
	"POST /ip/{ip}/mitigation",
}

func fetchSchema(region, ns string) (map[string]interface{}, error) {
	url := regions[region] + "/" + ns + ".json"
	c := &http.Client{Timeout: 60 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: HTTP %d", region, ns, resp.StatusCode)
	}
	var doc map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// collect 把三区 schema 拍成 {endpoint: {region: sig}}
func collect(t *testing.T) baseline {
	t.Helper()
	want := map[string]bool{}
	for _, e := range usedEndpoints {
		want[e] = true
	}
	out := baseline{}
	for region := range regions {
		for _, ns := range namespaces {
			doc, err := fetchSchema(region, ns)
			if err != nil {
				t.Fatalf("拉取 %s/%s 失败: %v", region, ns, err)
			}
			apis, _ := doc["apis"].([]interface{})
			for _, aRaw := range apis {
				a, _ := aRaw.(map[string]interface{})
				path, _ := a["path"].(string)
				ops, _ := a["operations"].([]interface{})
				for _, oRaw := range ops {
					o, _ := oRaw.(map[string]interface{})
					method, _ := o["httpMethod"].(string)
					key := method + " " + path
					if !want[key] {
						continue
					}
					sig := endpointSig{}
					if st, ok := o["apiStatus"].(map[string]interface{}); ok {
						sig.Status, _ = st["value"].(string)
						sig.Deprecated, _ = st["deprecatedDate"].(string)
						sig.Deletion, _ = st["deletionDate"].(string)
						sig.Replacement, _ = st["replacement"].(string)
					}
					sig.ResponseType, _ = o["responseType"].(string)
					if ps, ok := o["parameters"].([]interface{}); ok {
						for _, pRaw := range ps {
							p, _ := pRaw.(map[string]interface{})
							name, _ := p["name"].(string)
							sig.Params = append(sig.Params, fmt.Sprintf("%v:%s:%v:%v",
								p["paramType"], name, p["dataType"], p["required"]))
						}
						sort.Strings(sig.Params)
					}
					if out[key] == nil {
						out[key] = map[string]endpointSig{}
					}
					out[key][region] = sig
				}
			}
		}
	}
	return out
}

func TestOVHSchemaDrift(t *testing.T) {
	if os.Getenv("OVH_SCHEMA_CHECK") == "" && !*update {
		t.Skip("需要联网:设 OVH_SCHEMA_CHECK=1 运行,或加 -update 刷新基线")
	}
	current := collect(t)

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := json.MarshalIndent(current, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(baselinePath(), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("基线已更新:%d 个端点", len(current))
		return
	}

	raw, err := os.ReadFile(baselinePath())
	if err != nil {
		t.Fatalf("读基线失败(第一次跑请加 -update 生成): %v", err)
	}
	var base baseline
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}

	var problems []string
	// 端点消失 / 状态变化 / 签名变化
	for ep, baseRegions := range base {
		curRegions, ok := current[ep]
		if !ok {
			problems = append(problems, fmt.Sprintf("端点整个消失了: %s", ep))
			continue
		}
		for region, bs := range baseRegions {
			cs, ok := curRegions[region]
			if !ok {
				problems = append(problems, fmt.Sprintf("%s 在 %s 区消失了", ep, region))
				continue
			}
			if cs.Status != bs.Status {
				problems = append(problems, fmt.Sprintf("%s [%s] 状态 %s → %s", ep, region, bs.Status, cs.Status))
			}
			if cs.Deprecated != bs.Deprecated || cs.Deletion != bs.Deletion || cs.Replacement != bs.Replacement {
				problems = append(problems, fmt.Sprintf("%s [%s] 废弃标记变化: deprecated=%q deletion=%q replacement=%q",
					ep, region, cs.Deprecated, cs.Deletion, cs.Replacement))
			}
			if cs.ResponseType != bs.ResponseType {
				problems = append(problems, fmt.Sprintf("%s [%s] 响应类型 %s → %s", ep, region, bs.ResponseType, cs.ResponseType))
			}
			if strings.Join(cs.Params, "|") != strings.Join(bs.Params, "|") {
				problems = append(problems, fmt.Sprintf("%s [%s] 参数变化:\n    基线: %v\n    现在: %v",
					ep, region, bs.Params, cs.Params))
			}
		}
		// 端点新出现在某个区(比如 US 补上了某功能)——是好事,但也要知道
		for region := range curRegions {
			if _, ok := baseRegions[region]; !ok {
				problems = append(problems, fmt.Sprintf("%s 新出现在 %s 区(基线里没有)", ep, region))
			}
		}
	}
	for ep := range current {
		if _, ok := base[ep]; !ok {
			problems = append(problems, fmt.Sprintf("基线里没有这个端点(新加的代码?): %s", ep))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Errorf("OVH 接口相对基线发生了 %d 处变化:\n  %s\n\n确认变化无害后跑 -update 刷新基线",
			len(problems), strings.Join(problems, "\n  "))
	}
}
