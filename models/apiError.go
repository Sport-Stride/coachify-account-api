package models

import (
	"github.com/gin-gonic/gin"
)

type ApiError struct {
	Code  int
	Error error
}

func FormatError(c *gin.Context, apiError *ApiError) {
	c.JSON(apiError.Code, gin.H{
		"error":  apiError.Error.Error(),
		"status": apiError.Code,
	})
}
