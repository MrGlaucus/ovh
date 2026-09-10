package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ovh-buy/server/internal/app"
	"github.com/ovh-buy/server/internal/types"
)

// GetServerFavorites GET /api/server-favorites
func GetServerFavorites(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := state.DB.ListServerFavorites()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取收藏失败: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, items)
	}
}

// AddServerFavorite POST /api/server-favorites
func AddServerFavorite(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			PlanCode    string `json:"planCode"`
			DisplayName string `json:"displayName"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效"})
			return
		}
		body.PlanCode = strings.TrimSpace(body.PlanCode)
		if body.PlanCode == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 planCode"})
			return
		}
		if err := state.DB.AddServerFavorite(body.PlanCode, body.DisplayName, types.NowISO()); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "添加收藏失败: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success"})
	}
}

// RemoveServerFavorite DELETE /api/server-favorites/:planCode
func RemoveServerFavorite(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		planCode := strings.TrimSpace(c.Param("planCode"))
		if planCode == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 planCode"})
			return
		}
		removed, err := state.DB.DeleteServerFavorite(planCode)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "取消收藏失败: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "removed": removed})
	}
}
