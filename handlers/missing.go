package handlers

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

func NoRoute(c *gin.Context) {
	c.JSON(
		http.StatusNotFound,
		gin.H{"status": http.StatusNotFound, "error": "Not Found"})
}
