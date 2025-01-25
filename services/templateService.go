package services

import "github.com/Sport-Stride/ss-api-template/utils"

type TemplateService struct {
}

func (service TemplateService) TemplateFunction() {
	utils.Logger.Info("Hello")
}
