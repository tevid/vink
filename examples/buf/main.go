package main

import (
	"fmt"
	"github.com/tevid/vink"
	"github.com/tevid/vink/proto"
	"os"
)

func main() {
	simpleProtocol := proto.Simple()
	bufProtocol := proto.Bufio(simpleProtocol, 64*1024, 64*1024)

	server, err := vink.Listen("tcp", "0.0.0.0:0", bufProtocol, vink.HandlerFunc(sererSideHandle), 0)
	if err != nil {
		vink.GetLogger().Error(err)
		os.Exit(1)
	}
	if err != nil {
		vink.GetLogger().Error(err)
		os.Exit(1)
	}

	addr := server.Listener().Addr().String()
	go server.Serve()

	session, err := vink.Dial("tcp", addr, bufProtocol, 0)

	clientSideHandle(session)
}

func clientSideHandle(session *vink.Session) {
	for i := 0; i < 30; i++ {
		msg := []byte("domac")
		err := session.Send(msg)
		if err != nil {
			vink.GetLogger().Error(err)
		}

		rep, err := session.Receive()
		if err != nil {
			vink.GetLogger().Error(err)
		}
		vink.GetLogger().Infof("recv data from server : %s\n", rep)
	}
}

func sererSideHandle(session *vink.Session) {
	for {
		req, err := session.Receive()
		if err != nil {
			vink.GetLogger().Error(err)
		}

		reqStr := fmt.Sprintf("%s", req)
		session.Send([]byte("Hello," + reqStr))
	}
}
