package scrapemateapp

import (
	"sync"

	"github.com/go-playground/validator/v10"
)

const (
	DefaultConcurrency = 1
	DefaultProvider    = "memory"
)

var (
	validate *validator.Validate
	once     sync.Once
)
