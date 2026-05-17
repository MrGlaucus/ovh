package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/numconv"
)

// GetHardwareInfo GET /api/server-control/:service_name/hardware
func GetHardwareInfo(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var hardware map[string]interface{}
		if err := client.Get("/dedicated/server/"+svc+"/specifications/hardware", &hardware); err != nil {
			state.Logger.Error("获取服务器 "+svc+" 硬件信息失败: "+err.Error(), "server_control")
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		// 1:1 对应 Python app.py:6150-6167：缺字段补 N/A / 0 / {} / []，
		// 否则 JSON 序列化 null 让前端 .toLowerCase / .length 崩溃
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"hardware": gin.H{
				"bootMode":                valueOr(hardware, "bootMode", "N/A"),
				"coresPerProcessor":       defaultZero(hardware["coresPerProcessor"]),
				"threadsPerProcessor":     defaultZero(hardware["threadsPerProcessor"]),
				"numberOfProcessors":      defaultZero(hardware["numberOfProcessors"]),
				"processorName":           valueOr(hardware, "processorName", "N/A"),
				"processorArchitecture":   valueOr(hardware, "processorArchitecture", "N/A"),
				"memorySize":              defaultObj(hardware["memorySize"]),
				"motherboard":             valueOr(hardware, "motherboard", "N/A"),
				"formFactor":              valueOr(hardware, "formFactor", "N/A"),
				"description":             valueOr(hardware, "description", ""),
				"diskGroups":              defaultArr(hardware["diskGroups"]),
				"expansionCards":          defaultArr(hardware["expansionCards"]),
				"usbKeys":                 defaultArr(hardware["usbKeys"]),
				"defaultHardwareRaidSize": defaultObj(hardware["defaultHardwareRaidSize"]),
				"defaultHardwareRaidType": valueOr(hardware, "defaultHardwareRaidType", "N/A"),
			},
		})
	}
}

// GetNetworkSpecs GET /api/server-control/:service_name/network-specs
func GetNetworkSpecs(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var network map[string]interface{}
		if err := client.Get("/dedicated/server/"+svc+"/specifications/network", &network); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"network": gin.H{
				"bandwidth":  network["bandwidth"],
				"connection": network["connection"],
				"ola":        network["ola"],
				"routing":    network["routing"],
				"traffic":    network["traffic"],
				"switching":  network["switching"],
				"vmac":       network["vmac"],
				"vrack":      network["vrack"],
			},
		})
	}
}

// GetServerIPs GET /api/server-control/:service_name/ips
func GetServerIPs(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var list []string
		if err := client.Get("/dedicated/server/"+svc+"/ips", &list); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		// 并发拉每个 IP 的详情
		details := parallelGetStringKeys(client, list, func(ip string) string {
			return "/ip/" + strings.ReplaceAll(ip, "/", "%2F")
		}, 10)
		ips := []gin.H{}
		for i, ip := range list {
			detail := details[i]
			if detail == nil {
				ips = append(ips, gin.H{"ip": ip, "type": "unknown"})
				continue
			}
			routedTo := ""
			if r, ok := detail["routedTo"].(map[string]interface{}); ok {
				if s, ok := r["serviceName"].(string); ok {
					routedTo = s
				}
			}
			ips = append(ips, gin.H{
				"ip":          ip,
				"type":        valueOr(detail, "type", "N/A"),
				"description": valueOr(detail, "description", ""),
				"routedTo":    routedTo,
			})
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "ips": ips, "total": len(ips)})
	}
}

// GetReverseDNS GET /api/server-control/:service_name/reverse
func GetReverseDNS(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var info map[string]interface{}
		if err := client.Get("/dedicated/server/"+svc, &info); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		reverseList := []gin.H{}
		mainIP, _ := info["ip"].(string)
		if mainIP != "" {
			var ips []string
			_ = client.Get("/dedicated/server/"+svc+"/reverse", &ips)
			// 并发拉每个反向 DNS 详情
			details := parallelGetStringKeys(client, ips, func(ip string) string {
				return "/dedicated/server/" + svc + "/reverse/" + ip
			}, 10)
			for i, rip := range ips {
				if details[i] == nil {
					continue
				}
				reverseList = append(reverseList, gin.H{"ipReverse": rip, "reverse": details[i]["reverse"]})
			}
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "reverses": reverseList})
	}
}

// SetReverseDNS POST /api/server-control/:service_name/reverse
func SetReverseDNS(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var body struct {
			IP      string `json:"ip"`
			Reverse string `json:"reverse"`
		}
		_ = c.ShouldBindJSON(&body)
		if body.IP == "" || body.Reverse == "" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "IP地址和反向DNS不能为空"})
			return
		}
		if err := client.Post("/dedicated/server/"+svc+"/reverse", map[string]interface{}{
			"ipReverse": body.IP,
			"reverse":   body.Reverse,
		}, nil); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		state.Logger.Info("服务器 "+svc+" IP "+body.IP+" 反向DNS已设置为 "+body.Reverse, "server_control")
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "反向DNS已设置"})
	}
}

// GetServiceInfo GET /api/server-control/:service_name/serviceinfo
func GetServiceInfo(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var info map[string]interface{}
		if err := client.Get("/dedicated/server/"+svc+"/serviceInfos", &info); err != nil {
			state.Logger.Error("获取服务器 "+svc+" 服务信息失败: "+err.Error(), "server_control")
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		renew, _ := info["renew"].(map[string]interface{})
		automatic := false
		period := 0
		if renew != nil {
			if a, ok := renew["automatic"].(bool); ok {
				automatic = a
			}
			if p, ok := numconv.ToInt64(renew["period"]); ok {
				period = int(p)
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"serviceInfo": gin.H{
				"status":         valueOr(info, "status", "unknown"),
				"expiration":     valueOr(info, "expiration", ""),
				"creation":       valueOr(info, "creation", ""),
				"renewalType":    automatic,
				"renewalPeriod":  period,
			},
		})
	}
}

// ChangeContact POST /api/server-control/:service_name/change-contact
func ChangeContact(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var body map[string]interface{}
		_ = c.ShouldBindJSON(&body)
		params := map[string]interface{}{}
		if v, ok := body["contactAdmin"].(string); ok && v != "" {
			params["contactAdmin"] = v
		}
		if v, ok := body["contactTech"].(string); ok && v != "" {
			params["contactTech"] = v
		}
		if v, ok := body["contactBilling"].(string); ok && v != "" {
			params["contactBilling"] = v
		}
		if len(params) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "至少需要指定一个联系人（管理员、技术或计费）"})
			return
		}
		var result map[string]interface{}
		if err := client.Post("/dedicated/server/"+svc+"/changeContact", params, &result); err != nil {
			state.Logger.Error("变更服务器 "+svc+" 联系人失败: "+err.Error(), "server_control")
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		state.Logger.Info(fmt.Sprintf("服务器 %s 联系人变更请求已提交: %v", svc, params), "server_control")
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "联系人变更请求已提交",
			"taskId":  result["id"],
			"details": result,
		})
	}
}

// GetInterventions GET /api/server-control/:service_name/interventions
func GetInterventions(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var ids []interface{}
		if err := client.Get("/dedicated/server/"+svc+"/intervention", &ids); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		// 并发拉详情
		details := parallelGetDetails(client, ids, func(k interface{}) string {
			return "/dedicated/server/" + svc + "/intervention/" + idToString(k)
		}, 10)
		list := []map[string]interface{}{}
		for _, d := range details {
			if d != nil {
				list = append(list, d)
			}
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "interventions": list})
	}
}

// GetInterventionDetail GET /api/server-control/:service_name/interventions/:intervention_id
func GetInterventionDetail(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		id := c.Param("intervention_id")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var d map[string]interface{}
		if err := client.Get("/dedicated/server/"+svc+"/intervention/"+id, &d); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "intervention": d})
	}
}

// GetPlannedInterventions GET /api/server-control/:service_name/planned-interventions
func GetPlannedInterventions(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var ids []interface{}
		if err := client.Get("/dedicated/server/"+svc+"/plannedIntervention", &ids); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		// 并发拉详情
		details := parallelGetDetails(client, ids, func(k interface{}) string {
			return "/dedicated/server/" + svc + "/plannedIntervention/" + idToString(k)
		}, 10)
		list := []map[string]interface{}{}
		for _, d := range details {
			if d != nil {
				list = append(list, d)
			}
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "plannedInterventions": list})
	}
}

// GetPlannedInterventionDetail GET /api/server-control/:service_name/planned-interventions/:intervention_id
func GetPlannedInterventionDetail(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		id := c.Param("intervention_id")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var d map[string]interface{}
		if err := client.Get(fmt.Sprintf("/dedicated/server/%s/plannedIntervention/%s", svc, id), &d); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "plannedIntervention": d})
	}
}

// HardwareReplace POST /api/server-control/:service_name/hardware/replace
func HardwareReplace(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var body map[string]interface{}
		_ = c.ShouldBindJSON(&body)
		componentType, _ := body["componentType"].(string)
		comment, _ := body["comment"].(string)
		if componentType == "" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "缺少 componentType 参数"})
			return
		}
		var result map[string]interface{}
		var err2 error
		switch componentType {
		case "hardDiskDrive":
			if comment == "" {
				comment = "Request hard disk drive replacement - faulty disk detected"
			}
			err2 = client.Post("/dedicated/server/"+svc+"/support/replace/hardDiskDrive", map[string]interface{}{
				"comment": comment,
				"disks":   []interface{}{},
				"inverse": true,
			}, &result)
		case "memory":
			details := "Memory module failure"
			if v, ok := body["details"].(string); ok && v != "" {
				details = v
			}
			if comment == "" {
				comment = "Request memory module replacement - hardware failure detected"
			}
			err2 = client.Post("/dedicated/server/"+svc+"/support/replace/memory", map[string]interface{}{
				"comment":          comment,
				"details":          details,
				"slotsDescription": "",
			}, &result)
		case "cooling":
			details := "Cooling system failure"
			if v, ok := body["details"].(string); ok && v != "" {
				details = v
			}
			if comment == "" {
				comment = "Request cooling system replacement - fan failure or overheating"
			}
			err2 = client.Post("/dedicated/server/"+svc+"/support/replace/cooling", map[string]interface{}{
				"comment": comment,
				"details": details,
			}, &result)
		default:
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "不支持的组件类型: " + componentType})
			return
		}
		if err2 != nil {
			errMsg := err2.Error()
			state.Logger.Error("硬件更换失败: "+svc+" - "+componentType+" - "+errMsg, "server_control")
			if strings.Contains(errMsg, "Action pending") {
				ticketID := "未知"
				if m := regexp.MustCompile(`ticketId[:\s]+(\d+)`).FindStringSubmatch(errMsg); m != nil {
					ticketID = m[1]
				}
				c.JSON(http.StatusBadRequest, gin.H{
					"success":   false,
					"error":     "已有待处理的硬件更换工单 (Ticket #" + ticketID + ")，请等待完成后再提交新请求",
					"ticketId":  ticketID,
					"isPending": true,
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": errMsg})
			return
		}
		state.Logger.Info("硬件更换请求已发送: "+svc+" - "+componentType, "server_control")
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "硬件更换请求已发送", "task": result})
	}
}

// GetHardwareRaidProfiles GET /api/server-control/:service_name/hardware-raid-profiles
func GetHardwareRaidProfiles(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var profiles interface{}
		if err := client.Get("/dedicated/server/"+svc+"/install/hardwareRaidProfile", &profiles); err != nil {
			errMsg := strings.ToLower(err.Error())
			if strings.Contains(errMsg, "not supported") {
				c.JSON(http.StatusOK, gin.H{
					"success":   true,
					"profiles":  []interface{}{},
					"supported": false,
					"message":   "此服务器不支持硬件RAID",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "profiles": profiles, "supported": true})
	}
}

// GetHardwareDiskInfo GET /api/server-control/:service_name/hardware-disk-info
func GetHardwareDiskInfo(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		svc := c.Param("service_name")
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		var hardware map[string]interface{}
		if err := client.Get("/dedicated/server/"+svc+"/specifications/hardware", &hardware); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		diskGroups := map[string]interface{}{}
		if dgs, ok := hardware["diskGroups"].([]interface{}); ok {
			for _, dgRaw := range dgs {
				dg, ok := dgRaw.(map[string]interface{})
				if !ok {
					continue
				}
				id := 0
				if v, ok := numconv.ToInt64(dg["diskGroupId"]); ok {
					id = int(v)
				}
				numberOfDisks := 0
				if v, ok := numconv.ToInt64(dg["numberOfDisks"]); ok {
					numberOfDisks = int(v)
				}
				diskSizeValue := 0
				diskSizeUnit := "GB"
				if ds, ok := dg["diskSize"].(map[string]interface{}); ok {
					if v, ok := numconv.ToInt64(ds["value"]); ok {
						diskSizeValue = int(v)
					}
					if u, ok := ds["unit"].(string); ok {
						diskSizeUnit = u
					}
				}
				disks := []map[string]interface{}{}
				for i := 0; i < numberOfDisks; i++ {
					disks = append(disks, map[string]interface{}{
						"capacity": diskSizeValue,
						"unit":     diskSizeUnit,
						"number":   i + 1,
						"diskType": dg["diskType"],
					})
				}
				diskGroups[fmt.Sprintf("%d", id)] = map[string]interface{}{
					"id":             id,
					"diskType":       dg["diskType"],
					"description":    dg["description"],
					"raidController": dg["raidController"],
					"disks":          disks,
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "diskGroups": diskGroups, "hardware": hardware})
	}
}

// GetPartitionSchemes GET /api/server-control/:service_name/partition-schemes
func GetPartitionSchemes(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		client, err := ovhClientFor(state, c)
		if err != nil {
			noOVHResp(c)
			return
		}
		templateName := c.Query("templateName")
		if templateName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "缺少templateName参数"})
			return
		}
		encodedTpl := url.PathEscape(templateName)
		var schemes []string
		if err := client.Get("/dedicated/installationTemplate/"+encodedTpl+"/partitionScheme", &schemes); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		// 双层嵌套并发：先并发拉每个 scheme 的 info + partition list，
		// 再对每个 scheme 内的 partition 并发拉详情
		type schemeResult struct {
			name       string
			info       map[string]interface{}
			parts      []string
			missingInfo bool
		}
		schemeResults := make([]schemeResult, len(schemes))
		sem := make(chan struct{}, 10)
		var wg sync.WaitGroup
		for i, schemeName := range schemes {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int, sname string) {
				defer wg.Done()
				defer func() { <-sem }()
				es := url.PathEscape(sname)
				var info map[string]interface{}
				if err := client.Get("/dedicated/installationTemplate/"+encodedTpl+"/partitionScheme/"+es, &info); err != nil {
					schemeResults[idx] = schemeResult{name: sname, missingInfo: true}
					return
				}
				var partitions []string
				_ = client.Get("/dedicated/installationTemplate/"+encodedTpl+"/partitionScheme/"+es+"/partition", &partitions)
				schemeResults[idx] = schemeResult{name: sname, info: info, parts: partitions}
			}(i, schemeName)
		}
		wg.Wait()

		details := []gin.H{}
		for _, sr := range schemeResults {
			if sr.missingInfo {
				details = append(details, gin.H{"name": sr.name, "priority": 0, "partitions": []interface{}{}})
				continue
			}
			priority := 0
			if v, ok := numconv.ToInt64(sr.info["priority"]); ok {
				priority = int(v)
			}
			// 并发拉该 scheme 的 partition 详情
			encodedScheme := url.PathEscape(sr.name)
			partDetails := make([]gin.H, len(sr.parts))
			pSem := make(chan struct{}, 10)
			var pWg sync.WaitGroup
			for pi, part := range sr.parts {
				pWg.Add(1)
				pSem <- struct{}{}
				go func(pidx int, p string) {
					defer pWg.Done()
					defer func() { <-pSem }()
					var partInfo map[string]interface{}
					if err := client.Get("/dedicated/installationTemplate/"+encodedTpl+"/partitionScheme/"+encodedScheme+"/partition/"+url.PathEscape(p), &partInfo); err != nil {
						return
					}
					order := 0
					if v, ok := numconv.ToInt64(partInfo["order"]); ok {
						order = int(v)
					}
					partDetails[pidx] = gin.H{
						"mountpoint": p,
						"filesystem": valueOr(partInfo, "filesystem", ""),
						"size":       defaultZero(partInfo["size"]),
						"order":      order,
						"raid":       partInfo["raid"],
						"type":       valueOr(partInfo, "type", "primary"),
					}
				}(pi, part)
			}
			pWg.Wait()
			// 去掉拉取失败的 nil 项
			cleaned := make([]gin.H, 0, len(partDetails))
			for _, pd := range partDetails {
				if pd != nil {
					cleaned = append(cleaned, pd)
				}
			}
			// 冒泡按 order 排序
			for i := 1; i < len(cleaned); i++ {
				for j := i; j > 0; j-- {
					oi, _ := cleaned[j]["order"].(int)
					oj, _ := cleaned[j-1]["order"].(int)
					if oj > oi {
						cleaned[j-1], cleaned[j] = cleaned[j], cleaned[j-1]
					}
				}
			}
			details = append(details, gin.H{
				"name":       sr.name,
				"priority":   priority,
				"partitions": cleaned,
			})
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "schemes": details})
	}
}
