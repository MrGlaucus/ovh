package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/proxy"
)

// AccountProxyStatus 检查一个账户的 OVH 专用代理。配置了代理时使用该账户独立
// Transport 请求 /me；代理错误绝不会走直连。直连账户只回显 direct，不把它伪装成代理可用。
func AccountProxyStatus(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.Param("id"))
		acc, ok := state.FindAccount(id)
		if !ok || acc.ID != id {
			c.JSON(http.StatusNotFound, gin.H{"error": "账户不存在"})
			return
		}
		if strings.TrimSpace(acc.ProxyURL) == "" {
			c.JSON(http.StatusOK, gin.H{"accountId": id, "mode": "direct", "configured": false, "healthy": true})
			return
		}
		start := time.Now()
		client, err := state.OVH.ClientFor(id)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"accountId": id, "mode": "proxy", "configured": true, "healthy": false, "proxy": proxy.Mask(acc.ProxyURL), "error": err.Error()})
			return
		}
		var me map[string]interface{}
		err = client.Get("/me", &me)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"accountId": id, "mode": "proxy", "configured": true, "healthy": false, "proxy": proxy.Mask(acc.ProxyURL), "error": "账户代理不可用，OVH 请求已阻断"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"accountId": id, "mode": "proxy", "configured": true, "healthy": true, "proxy": proxy.Mask(acc.ProxyURL), "latencyMs": time.Since(start).Milliseconds()})
	}
}
