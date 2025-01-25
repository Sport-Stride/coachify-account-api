package utils

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type LogWrapperObj struct {
	Logger *zap.SugaredLogger
}

var Logger = LogWrapperObj{
	Logger: initLogger(),
}

func initLogger() *zap.SugaredLogger {
	if gin.IsDebugging() {
		logger, _ := zap.NewDevelopment()
		defer logger.Sync() // flushes buffer, if any
		return logger.Sugar()
	} else {
		//only warn and above
		config := zap.NewProductionConfig()
		config.Level.SetLevel(zap.InfoLevel)
		config.EncoderConfig.TimeKey = "date_time"
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		// safe concurrent call
		config.Sampling = nil
		logger, _ := config.Build()
		defer logger.Sync() // flushes buffer, if any
		return logger.With(zap.Namespace("app")).Sugar()
	}
}

func (logWrapper LogWrapperObj) Info(message string, fields ...zap.Field) {
	logWrapper.Logger.Desugar().Info(message, fields...)
}

func (logWrapper LogWrapperObj) Warn(message string, fields ...zap.Field) {
	logWrapper.Logger.Desugar().Warn(message, fields...)
}

func (logWrapper LogWrapperObj) Error(message string, fields ...zap.Field) {
	logWrapper.Logger.Desugar().Error(message, fields...)
}

func (logWrapper LogWrapperObj) Fatal(message string, fields ...zap.Field) {
	logWrapper.Logger.Desugar().Fatal(message, fields...)
}
