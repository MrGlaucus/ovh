package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ovh-buy/server/internal/app"
)

// GetNetworkInterfaces GET /api/server-control/:service_name/network-interfaces
func GetNetworkInterfaces(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		state.Logger.Info("[网卡] 获取物理网卡列表: "+svc, "server_control")
		var macs []string
		if err := client.Get("/dedicated/server/"+svc+"/networkInterfaceController", &macs); err != nil {
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "does not exist") || strings.Contains(errMsg, "not found") {
				c.JSON(http.StatusOK, gin.H{
					"success":    true,
					"interfaces": []interface{}{},
					"count":      0,
					"message":    "该服务器暂无网卡信息",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		// 并发拉每张网卡详情
		details := parallelGetStringKeys(client, macs, func(m string) string {
			return "/dedicated/server/" + svc + "/networkInterfaceController/" + m
		}, 10)
		interfaces := []gin.H{}
		for i, mac := range macs {
			d := details[i]
			if d == nil {
				interfaces = append(interfaces, gin.H{
					"mac":      mac,
					"linkType": "unknown",
					"error":    "fetch failed",
				})
				continue
			}
			interfaces = append(interfaces, gin.H{
				"mac":                     mac,
				"linkType":                d["linkType"],
				"virtualNetworkInterface": d["virtualNetworkInterface"],
			})
		}
		state.Logger.Info(fmt.Sprintf("[网卡] 找到 %d 个物理网卡", len(interfaces)), "server_control")
		c.JSON(http.StatusOK, gin.H{"success": true, "interfaces": interfaces, "count": len(interfaces)})
	}
}

// GetMRTGData GET /api/server-control/:service_name/mrtg
func GetMRTGData(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		period := c.DefaultQuery("period", "daily")
		trafficType := c.DefaultQuery("type", "traffic:download")
		state.Logger.Info(fmt.Sprintf("[MRTG] 获取流量数据: %s - %s - %s", svc, period, trafficType), "server_control")

		var macs []string
		if err := client.Get("/dedicated/server/"+svc+"/networkInterfaceController", &macs); err != nil {
			state.Logger.Warn("[MRTG] 无法获取网卡列表，使用旧版API: "+err.Error(), "server_control")
			var data []map[string]interface{}
			q := url.Values{}
			q.Set("period", period)
			q.Set("type", trafficType)
			if err := client.Get("/dedicated/server/"+svc+"/mrtg?"+q.Encode(), &data); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "新旧API均失败: " + err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"success":    true,
				"data":       data,
				"period":     period,
				"type":       trafficType,
				"interfaces": []interface{}{},
			})
			return
		}
		// 并发拉每张网卡 MRTG 数据
		type mrtgResult struct {
			data []map[string]interface{}
			err  error
		}
		mrtgResults := make([]mrtgResult, len(macs))
		sem := make(chan struct{}, 10)
		var wg sync.WaitGroup
		for i, mac := range macs {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int, m string) {
				defer wg.Done()
				defer func() { <-sem }()
				var d []map[string]interface{}
				q := url.Values{}
				q.Set("period", period)
				q.Set("type", trafficType)
				path := "/dedicated/server/" + svc + "/networkInterfaceController/" + m + "/mrtg?" + q.Encode()
				if err := client.Get(path, &d); err != nil {
					mrtgResults[idx] = mrtgResult{err: err}
					return
				}
				mrtgResults[idx] = mrtgResult{data: d}
			}(i, mac)
		}
		wg.Wait()

		all := []gin.H{}
		for i, mac := range macs {
			r := mrtgResults[i]
			if r.err != nil {
				all = append(all, gin.H{"mac": mac, "data": []interface{}{}, "error": r.err.Error()})
				continue
			}
			all = append(all, gin.H{"mac": mac, "data": r.data})
			state.Logger.Info(fmt.Sprintf("[MRTG] 获取网卡 %s 数据成功: %d 个数据点", mac, len(r.data)), "server_control")
		}
		state.Logger.Info(fmt.Sprintf("[MRTG] 成功获取 %d 个网卡的流量数据", len(all)), "server_control")
		c.JSON(http.StatusOK, gin.H{
			"success":    true,
			"interfaces": all,
			"period":     period,
			"type":       trafficType,
			"server":     svc,
		})
	}
}

// ConfigureOLAAggregation POST /api/server-control/:service_name/ola/aggregation
func ConfigureOLAAggregation(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var body struct {
			Name                    string   `json:"name"`
			VirtualNetworkInterfaces []string `json:"virtualNetworkInterfaces"`
		}
		_ = c.ShouldBindJSON(&body)
		if body.Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "缺少聚合名称(name)参数"})
			return
		}
		if len(body.VirtualNetworkInterfaces) < 2 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "至少需要2个网络接口进行聚合"})
			return
		}
		state.Logger.Info(fmt.Sprintf("[OLA] 配置网络聚合: %s - %s - %d个接口", svc, body.Name, len(body.VirtualNetworkInterfaces)), "server_control")
		var result map[string]interface{}
		if err := client.Post("/dedicated/server/"+svc+"/ola/aggregation", map[string]interface{}{
			"name":                     body.Name,
			"virtualNetworkInterfaces": body.VirtualNetworkInterfaces,
		}, &result); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		state.Logger.Info(fmt.Sprintf("[OLA] 网络聚合配置任务已创建: Task#%v", result["taskId"]), "server_control")
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "网络聚合配置任务已创建", "task": result})
	}
}

// ResetOLAConfiguration POST /api/server-control/:service_name/ola/reset
func ResetOLAConfiguration(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var body struct {
			VirtualNetworkInterface string `json:"virtualNetworkInterface"`
		}
		_ = c.ShouldBindJSON(&body)
		if body.VirtualNetworkInterface == "" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "缺少虚拟网络接口UUID(virtualNetworkInterface)参数"})
			return
		}
		state.Logger.Info("[OLA] 重置网络接口: "+svc+" - "+body.VirtualNetworkInterface, "server_control")
		var result map[string]interface{}
		if err := client.Post("/dedicated/server/"+svc+"/ola/reset", map[string]interface{}{
			"virtualNetworkInterface": body.VirtualNetworkInterface,
		}, &result); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		state.Logger.Info(fmt.Sprintf("[OLA] 网络接口重置任务已创建: Task#%v", result["taskId"]), "server_control")
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "网络接口重置任务已创建", "task": result})
	}
}

// OLAGroup POST /api/server-control/:service_name/ola/group
func OLAGroup(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var result map[string]interface{}
		if err := client.Post("/dedicated/server/"+svc+"/ola/group", map[string]interface{}{}, &result); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		state.Logger.Info("创建OLA组成功: "+svc, "server_control")
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "OLA组已创建", "result": result})
	}
}

// OLAUngroup POST /api/server-control/:service_name/ola/ungroup
func OLAUngroup(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var result map[string]interface{}
		if err := client.Post("/dedicated/server/"+svc+"/ola/ungroup", map[string]interface{}{}, &result); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		state.Logger.Info("解散OLA组成功: "+svc, "server_control")
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "OLA组已解散", "result": result})
	}
}

// IPMI Console
func GetIPMIConsole(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		state.Logger.Info("[IPMI] 获取服务器 "+svc+" IPMI信息", "server_control")
		var ipmi map[string]interface{}
		if err := client.Get("/dedicated/server/"+svc+"/features/ipmi", &ipmi); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		var accessType string
		if sf, ok := ipmi["supportedFeatures"].(map[string]interface{}); ok {
			if _, ok := sf["kvmipHtml5URL"]; ok {
				accessType = "kvmipHtml5URL"
			} else if _, ok := sf["kvmipJnlp"]; ok {
				accessType = "kvmipJnlp"
			} else if _, ok := sf["serialOverLanURL"]; ok {
				accessType = "serialOverLanURL"
			}
		}
		if accessType == "" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "服务器不支持KVM控制台访问"})
			return
		}
		state.Logger.Info("[IPMI] 请求KVM控制台访问，类型: "+accessType, "server_control")
		clientIP := c.GetHeader("X-Forwarded-For")
		if clientIP == "" {
			clientIP = c.ClientIP()
		}
		if idx := strings.Index(clientIP, ","); idx != -1 {
			clientIP = strings.TrimSpace(clientIP[:idx])
		}
		params := map[string]interface{}{
			"type": accessType,
			"ttl":  15,
		}
		if clientIP != "" && !strings.HasPrefix(clientIP, "127.") && !strings.HasPrefix(clientIP, "192.168.") && !strings.HasPrefix(clientIP, "10.") {
			params["ipToAllow"] = clientIP
			state.Logger.Info("[IPMI] 添加IP白名单: "+clientIP, "server_control")
		} else {
			state.Logger.Warn("[IPMI] 跳过IP白名单（本地或内网IP）: "+clientIP, "server_control")
		}
		var task map[string]interface{}
		if err := client.Post("/dedicated/server/"+svc+"/features/ipmi/access", params, &task); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		taskID := task["taskId"]
		state.Logger.Info(fmt.Sprintf("[IPMI] 创建访问任务: taskId=%v, status=%v", taskID, task["status"]), "server_control")
		// 轮询
		maxRetries := 10
		taskCompleted := false
		for i := 0; i < maxRetries; i++ {
			time.Sleep(2 * time.Second)
			var ts map[string]interface{}
			// 1:1 对应 Python app.py:7043 —— OVH 错误直接抛进外层 except 返回 500，
			// 之前 Go 静默 continue 会掩盖 OVH 真错误，最终用 "超时" 假面具吞掉
			if err := client.Get(fmt.Sprintf("/dedicated/server/%s/task/%v", svc, taskID), &ts); err != nil {
				state.Logger.Error(fmt.Sprintf("[IPMI] 查询任务 %v 状态失败: %s", taskID, err.Error()), "server_control")
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
				return
			}
			status, _ := ts["status"].(string)
			state.Logger.Info(fmt.Sprintf("[IPMI] 任务状态检查 (%d/%d): %s", i+1, maxRetries, status), "server_control")
			if status == "done" {
				taskCompleted = true
				break
			}
			if status == "cancelled" || status == "customerError" || status == "ovhError" {
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "IPMI访问任务失败: " + status})
				return
			}
		}
		if !taskCompleted {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "IPMI访问任务超时"})
			return
		}
		var consoleAccess map[string]interface{}
		_ = client.Get("/dedicated/server/"+svc+"/features/ipmi/access?type="+accessType, &consoleAccess)
		c.JSON(http.StatusOK, gin.H{
			"success":    true,
			"ipmi":       ipmi,
			"console":    consoleAccess,
			"accessType": accessType,
		})
	}
}

// GetTrafficStatistics GET /api/server-control/:service_name/statistics
func GetTrafficStatistics(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		_, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		state.Logger.Info("[Stats] 获取服务器 "+svc+" 流量统计", "server_control")
		period := c.DefaultQuery("period", "lastday")
		typeParam := c.DefaultQuery("type", "traffic:download")
		// 多账户:用请求里指定账户的 endpoint + 凭据
		acc, ok := ovhAccountFor(state, c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "未配置 OVH 账户"})
			return
		}
		baseURL := ovhAPIBaseURL(acc.Endpoint)
		apiURL := baseURL + "/1.0/dedicated/server/" + svc + "/statistics?period=" + url.QueryEscape(period) + "&type=" + url.QueryEscape(typeParam)
		state.Logger.Info("[Stats] 请求API: "+apiURL, "server_control")
		req, _ := http.NewRequest(http.MethodGet, apiURL, nil)
		req.Header.Set("X-Ovh-Application", acc.AppKey)
		req.Header.Set("X-Ovh-Consumer", acc.ConsumerKey)
		httpClient := &http.Client{Timeout: 10 * time.Second}
		resp, err := httpClient.Do(req)
		if err != nil {
			state.Logger.Error("[Stats] 流量统计API调用失败: "+err.Error(), "server_control")
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error":   "该服务器可能不支持流量统计功能",
				"details": err.Error(),
			})
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			state.Logger.Error(fmt.Sprintf("[Stats] API返回错误: %d - %s", resp.StatusCode, string(body)), "server_control")
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error":   fmt.Sprintf("流量统计API不可用 (HTTP %d)", resp.StatusCode),
			})
			return
		}
		var stats interface{}
		_ = jsonUnmarshal(body, &stats)
		state.Logger.Info("[Stats] 流量统计获取成功", "server_control")
		c.JSON(http.StatusOK, gin.H{
			"success":    true,
			"statistics": stats,
			"period":     period,
			"type":       typeParam,
		})
	}
}

// GetNetworkInterfaceStats GET /api/server-control/:service_name/network-stats
func GetNetworkInterfaceStats(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		state.Logger.Info("[Network] 获取服务器 "+svc+" 网络接口信息", "server_control")
		var macs []string
		if err := client.Get("/dedicated/server/"+svc+"/networkInterfaceController", &macs); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		// 并发拉每张网卡详情
		details := parallelGetStringKeys(client, macs, func(m string) string {
			return "/dedicated/server/" + svc + "/networkInterfaceController/" + m
		}, 10)
		interfaces := []map[string]interface{}{}
		for _, d := range details {
			if d != nil {
				interfaces = append(interfaces, d)
			}
		}
		state.Logger.Info(fmt.Sprintf("[Network] 找到 %d 个网络接口", len(interfaces)), "server_control")
		c.JSON(http.StatusOK, gin.H{"success": true, "interfaces": interfaces})
	}
}
