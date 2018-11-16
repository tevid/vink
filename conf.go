package vink

import (
	"encoding/json"
	"io/ioutil"
	"regexp"
)

var gConf VinkConfig

type VinkConfig struct {
	Addr string `json:"ProxyAddr"`
	Port int    `json:"Port"`
}

func loadConfig(file string, o interface{}) {
	//正则过滤
	reg, e := regexp.Compile("//(.*)")

	if e != nil {
		panic(e)
	}

	raw, err := ioutil.ReadFile(file)
	//去除注释
	raw = reg.ReplaceAll(raw, []byte{})
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(raw, o)
	if err != nil {
		panic(err)
	}
}
