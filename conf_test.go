package vink

import (
	. "github.com/tevid/gohamcrest"
	"testing"
)

func TestConfigLoad(t *testing.T) {
	var config VinkConfig
	loadConfig("./conf/vink.conf", &config)
	Assert(t, config.Addr, Equal("0.0.0.0"))
}
