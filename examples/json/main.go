package main

import (
	"github.com/tevid/vink"
	"github.com/tevid/vink/proto"
	"log"
)

type TestReq struct {
	A, B int
}

func main() {
	jsonProtocol := proto.Json()
	jsonProtocol.Register(TestReq{})

	server, err := vink.Listen("tcp", "0.0.0.0:0", jsonProtocol, vink.HandlerFunc(sererSideHandle), 0)

	if err != nil {
		vink.GetLogger().Error(err)
	}

	addr := server.Listener().Addr().String()
	go server.Serve()

	client, err := vink.Dial("tcp", addr, jsonProtocol, 0)
	clientSideHandle(client)
}

func sererSideHandle(session *vink.Session) {
	for {
		req, err := session.Receive()
		if err != nil {
			vink.GetLogger().Error(err)
		}
		log.Printf("Server Receive: %v", req)
		err = session.Send(req.(*TestReq).A * req.(*TestReq).B)
	}
}

func clientSideHandle(session *vink.Session) {
	for i := 0; i < 100; i++ {
		err := session.Send(&TestReq{
			i, i * 3,
		})
		if err != nil {
			vink.GetLogger().Error(err)
		}
		rsp, err := session.Receive()
		log.Printf("Client Receive: %v", rsp)
	}
}
