package router

import (
	"time"

	"github.com/Sport-Stride/ss-api-template/handlers"
	"github.com/Sport-Stride/ss-api-template/services"
	"github.com/Sport-Stride/ss-api-template/utils"
	"github.com/gin-gonic/gin"
)

func InitializeRouter(services *services.Services) *gin.Engine {

	// Set the default gin router
	r := gin.New()

	r.Use(gin.Recovery())

	// Initialize middlewares
	initializeMiddlewares(r)

	// Initialize routes
	initializeRoutes(r, services)

	return r

}

func initializeRoutes(r *gin.Engine, services *services.Services) {
	untracedGroup := r.Group("/")
	untracedGroup.Use(Ginzap(utils.Logger, time.RFC3339, true, false))

	// health
	untracedGroup.GET("/", handlers.GetHealth)
	untracedGroup.GET("/health", handlers.GetHealth)

	// fallback
	r.NoRoute(handlers.NoRoute)
}
