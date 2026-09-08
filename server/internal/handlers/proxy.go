package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ovh-buy/server/internal/proxy"
)

// GetProxyStatus GET /api/proxy/status —— 返回缓存的代理配置与各 host 连通性。
// 前端右上角指示器 60s 轮询一次,这里读缓存不发真实探测(探测由 Check(false) 内部按 TTL 触发)。
func GetProxyStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		st, err := proxy.Check(false)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, st)
	}
}

// CheckProxy POST /api/proxy/check —— 强制重新探测全部目标 host。
// 同步等待完成(6 个 host 并发,最坏 ~10s),前端按钮用 loading 态承接。
func CheckProxy() gin.HandlerFunc {
	return func(c *gin.Context) {
		st, err := proxy.Check(true)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, st)
	}
}
