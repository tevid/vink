package vink

import (
	"io/ioutil"
	"os"
	"strconv"
	"syscall"
)

func CreatePid(filename string) error {
	pid := syscall.Getpid()
	return ioutil.WriteFile(filename, []byte(strconv.Itoa(pid)), 0644)
}

func RemovePid(filename string) error {
	return os.Remove(filename)
}
