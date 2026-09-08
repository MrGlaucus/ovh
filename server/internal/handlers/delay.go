package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ovh-buy/server/internal/app"
)

// GetDelayConfig GET /api/delay-config —— 返回自动下单延迟秒数。
// 只读:AUTO_ORDER_DELAY_SECONDS 在启动时解析进 state,重启才生效。
// 前端右上角指示器与代理状态并排展示,让用户随时知道
// "自动下单会不会先等 N 秒" —— 延迟不是健康状态,是特意设的等待,
// 忘了关会导致补货时干等,所以必须常驻视线。
func GetDelayConfig(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"delay_seconds": state.AutoOrderDelaySeconds,
		})
	}
}
