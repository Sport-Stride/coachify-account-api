package core

import (
	"fmt"
	"math/rand"
	"time"
)

type ActivationManager interface {
	GenerateCode() string
}
type SimpleActivationManager struct{}

func NewSimpleActivationManager() *SimpleActivationManager {
	return &SimpleActivationManager{}
}

func (a *SimpleActivationManager) GenerateCode() string {
	rand.Seed(time.Now().UnixNano())
	code := rand.Intn(900000) + 100000
	return fmt.Sprintf("%06d", code)
}
