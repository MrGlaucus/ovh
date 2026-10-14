package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/ovh-buy/server/internal/app"
	"net/http"
	"unicode/utf8"
)

func UpdateHistoryNote(state *app.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Note *string `json:"note"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
		if err := c.ShouldBindJSON(&body); err != nil || body.Note == nil || utf8.RuneCountInString(*body.Note) > 1000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请填写备注，最多1000字，可保存空内容清除备注"})
			return
		}
		found, err := state.UpdateHistoryNote(c.Param("id"), *body.Note)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "备注保存失败，请重试"})
			return
		}
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "抢购历史记录不存在"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"note": *body.Note})
	}
}
