package vink

import (
	"github.com/domac/qloger"
)

var defaultLogger qloger.Logger

func init() {
	logger, err := qloger.NewQLogger("stdout", "info")
	if err != nil {
		return
	}
	defaultLogger = logger
}

func GetLogger() qloger.Logger {
	return defaultLogger
}
