package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/GunarsK-portfolio/portfolio-common/health"
)

// Setup configures all routes for the worker service
func Setup(router *gin.Engine, healthAgg *health.Aggregator) {
	// Health check (required for App Runner)
	router.GET("/health", healthAgg.Handler())
}
