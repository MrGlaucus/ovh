package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ovh-buy/server/internal/proxy"
)

// GetProxyStatus returns the public outbound proxy state. Account-specific OVH
// traffic has separate proxy and outbound-IP controls and is not represented by
// this endpoint.
func GetProxyStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		status, err := proxy.Check(false)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, status)
	}
}

// CheckProxy forces a fresh public proxy probe.
func CheckProxy() gin.HandlerFunc {
	return func(c *gin.Context) {
		status, err := proxy.Check(true)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, status)
	}
}
