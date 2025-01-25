package services

import (
	"github.com/Sport-Stride/ss-api-template/utils"
)

type Services struct {
	TemplateService *TemplateService
}

func InitServices(config utils.AppConfig) *Services {

	template := &TemplateService{}

	return &Services{
		TemplateService: template,
	}

}
