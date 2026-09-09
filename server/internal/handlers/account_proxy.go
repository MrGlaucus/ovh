package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/outboundip"
	"github.com/ovh-buy/server/internal/proxy"
	"github.com/ovh-buy/server/internal/types"
)

// AccountProxyStatus 检查当前账户专属网络路径。先验证出口 IP；只有匹配时才通过
// 该账户独立 Transport 请求带签名的 /me。失败绝不会回退直连。
func AccountProxyStatus(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.Param("id"))
		acc, ok := state.FindAccount(id)
		if !ok || acc.ID != id {
			c.JSON(http.StatusNotFound, gin.H{"error": "账户不存在"})
			return
		}
		status, err := state.OVH.CheckOutboundIP(id, true)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		payload := outboundPayload(id, acc, status)
		if status.State == "direct" {
			payload["healthy"] = true
			payload["ovhAuthStatus"] = "skipped"
			c.JSON(http.StatusOK, payload)
			return
		}
		if !status.Allowed() {
			payload["ovhAuthStatus"] = "blocked"
			c.JSON(http.StatusServiceUnavailable, payload)
			return
		}
		// 只有出口 IP 确认后才允许发出这个带签名的 /me 请求。
		start := time.Now()
		client, err := state.OVH.ClientFor(id)
		if err != nil {
			payload["healthy"] = false
			payload["ovhAuthStatus"] = "failed"
			payload["ovhAuthError"] = err.Error()
			payload["error"] = err.Error()
			c.JSON(http.StatusServiceUnavailable, payload)
			return
		}
		var me map[string]interface{}
		if err := client.Get("/me", &me); err != nil {
			payload["healthy"] = false
			payload["ovhAuthStatus"] = "failed"
			payload["ovhAuthError"] = "出口 IP 已匹配，但 OVH 凭据验证失败"
			payload["error"] = "出口 IP 已匹配，但 OVH 凭据验证失败"
			c.JSON(http.StatusServiceUnavailable, payload)
			return
		}
		payload["healthy"] = true
		payload["ovhAuthStatus"] = "verified"
		payload["latencyMs"] = time.Since(start).Milliseconds()
		c.JSON(http.StatusOK, payload)
	}
}

// CheckAccountProxy 用当前表单的网络配置查询出口 IP，不保存任何输入，也不发送 OVH 鉴权请求。
// 每次都新建 Transport，避免检测请求复用任何账户的连接池或代理配置。
func CheckAccountProxy(state *app.State) gin.HandlerFunc {
	type input struct {
		AccountID          string `json:"accountId"`
		ProxyURL           string `json:"proxyUrl"`
		ExpectedOutboundIP string `json:"expectedOutboundIp"`
	}
	return func(c *gin.Context) {
		var in input
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		in.AccountID = strings.TrimSpace(in.AccountID)
		in.ProxyURL = strings.TrimSpace(in.ProxyURL)
		if in.AccountID != "" {
			acc, ok := state.FindAccount(in.AccountID)
			if !ok || acc.ID != in.AccountID {
				c.JSON(http.StatusNotFound, gin.H{"error": "账户不存在"})
				return
			}
			if in.ProxyURL == "" {
				in.ProxyURL = acc.ProxyURL
			}
			if in.ExpectedOutboundIP == "" {
				in.ExpectedOutboundIP = acc.ExpectedOutboundIP
			}
		}
		if in.ProxyURL != "" {
			if _, err := proxy.ParseAccountProxy(in.ProxyURL); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		candidate := types.OVHAccount{ID: "form-check", ProxyURL: in.ProxyURL, ExpectedOutboundIP: in.ExpectedOutboundIP}
		status := outboundip.NewGate().Ensure(candidate, true)
		payload := gin.H{"healthy": status.Allowed(), "proxy": proxy.Mask(in.ProxyURL), "expectedOutboundIp": status.ExpectedIP, "actualOutboundIp": status.ActualIP, "outboundIpStatus": status.State, "outboundIpError": status.Error, "outboundIpCheckedAt": status.CheckedAt.Format(time.RFC3339Nano)}
		if !status.Allowed() {
			c.JSON(http.StatusServiceUnavailable, payload)
			return
		}
		c.JSON(http.StatusOK, payload)
	}
}

func outboundPayload(id string, acc types.OVHAccount, status outboundip.Status) gin.H {
	return gin.H{"accountId": id, "mode": map[bool]string{true: "proxy", false: "direct"}[strings.TrimSpace(acc.ProxyURL) != ""], "configured": strings.TrimSpace(acc.ProxyURL) != "", "proxy": proxy.Mask(acc.ProxyURL), "expectedOutboundIp": status.ExpectedIP, "actualOutboundIp": status.ActualIP, "outboundIpStatus": status.State, "outboundIpError": status.Error, "outboundIpCheckedAt": status.CheckedAt.Format(time.RFC3339Nano), "blocked": status.State != "direct" && !status.Allowed()}
}
