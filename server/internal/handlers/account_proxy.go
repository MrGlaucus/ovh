package handlers

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/ovh"
	"github.com/ovh-buy/server/internal/proxy"
	"github.com/ovh-buy/server/internal/types"
)

// AccountProxyStatus returns the persisted account-specific outbound-IP state.
// It is deliberately read-only: rendering the account switcher must not send a
// signed OVH request.
func AccountProxyStatus(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.Param("id"))
		acc, ok := state.FindAccount(id)
		if !ok || acc.ID != id {
			c.JSON(http.StatusNotFound, gin.H{"error": "账户不存在"})
			return
		}
		c.JSON(http.StatusOK, outboundIPResponse(acc, acc.OutboundIPStatus == "verified", 0, ""))
	}
}

// CheckAccountProxyStatus 手动检测账户出口 IP。探测使用独立的账户专属 HTTP client，
// 只有 IP 与预期一致才会继续发送带签名的 OVH /me 请求。
func CheckAccountProxyStatus(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.Param("id"))
		acc, ok := state.FindAccount(id)
		if !ok || acc.ID != id {
			c.JSON(http.StatusNotFound, gin.H{"error": "账户不存在"})
			return
		}
		start := time.Now()
		if err := state.OVH.CheckOutboundIP(id, true); err != nil {
			acc, _ = state.FindAccount(id)
			c.JSON(http.StatusServiceUnavailable, outboundIPResponse(acc, false, time.Since(start).Milliseconds(), err.Error()))
			return
		}
		valid, warning := verifyAccountCreds(state, id)
		acc, _ = state.FindAccount(id)
		response := outboundIPResponse(acc, valid, time.Since(start).Milliseconds(), "")
		if warning != "" {
			response["subsidiaryWarning"] = warning
		}
		if !valid {
			response["error"] = "出口 IP 已验证，但 OVH 凭据验证失败"
			c.JSON(http.StatusServiceUnavailable, response)
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

// CheckProspectiveOutboundIP validates a form's expected IP through its proxy
// without saving anything or sending an OVH credential.
func CheckProspectiveOutboundIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			ProxyURL           string `json:"proxyUrl"`
			ExpectedOutboundIP string `json:"expectedOutboundIp"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		body.ProxyURL = strings.TrimSpace(body.ProxyURL)
		body.ExpectedOutboundIP = strings.TrimSpace(body.ExpectedOutboundIP)
		ip := net.ParseIP(body.ExpectedOutboundIP)
		if ip == nil || ip.To4() == nil || ip.String() != body.ExpectedOutboundIP {
			c.JSON(http.StatusBadRequest, gin.H{"error": "预期出口 IP 必须是单个 IPv4 地址"})
			return
		}
		if body.ProxyURL != "" {
			if _, err := proxy.ParseAccountProxy(body.ProxyURL); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		actual, err := ovh.ProbeOutboundIP(c.Request.Context(), body.ProxyURL)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"expectedOutboundIp": body.ExpectedOutboundIP, "outboundIpStatus": "blocked", "blocked": true, "error": err.Error()})
			return
		}
		if actual != body.ExpectedOutboundIP {
			c.JSON(http.StatusServiceUnavailable, gin.H{"expectedOutboundIp": body.ExpectedOutboundIP, "actualOutboundIp": actual, "outboundIpStatus": "blocked", "blocked": true, "error": "实际出口 IP 与预期不一致"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"expectedOutboundIp": body.ExpectedOutboundIP, "actualOutboundIp": actual, "outboundIpStatus": "verified", "blocked": false})
	}
}

func outboundIPResponse(acc types.OVHAccount, healthy bool, latencyMS int64, errText string) gin.H {
	configured := strings.TrimSpace(acc.ProxyURL) != ""
	mode := "direct"
	if configured {
		mode = "proxy"
	}
	response := gin.H{
		"accountId":           acc.ID,
		"mode":                mode,
		"configured":          configured,
		"healthy":             healthy,
		"expectedOutboundIp":  acc.ExpectedOutboundIP,
		"actualOutboundIp":    acc.ActualOutboundIP,
		"outboundIpStatus":    acc.OutboundIPStatus,
		"outboundIpCheckedAt": acc.OutboundIPCheckedAt,
		"outboundIpError":     acc.OutboundIPError,
		"blocked":             acc.OutboundIPStatus != "verified",
		"latencyMs":           latencyMS,
	}
	if configured {
		response["proxy"] = proxy.Mask(acc.ProxyURL)
	}
	if errText != "" {
		response["error"] = errText
	}
	return response
}
