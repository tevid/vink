package vink

/**
 * 简单的Echo服务
 */

import (
	"io"
	"net"
	"strings"
	"time"
)

type EchoServer struct {
	listener   net.Listener
	protocol   Protocol
	handler    Handler
	sendChSize int
}

func NewEchoServer(listener net.Listener, protocol Protocol, handler Handler, sendChSize int) *EchoServer {
	return &EchoServer{
		listener:   listener,
		protocol:   protocol,
		handler:    handler,
		sendChSize: sendChSize,
	}
}

func (server *EchoServer) Serve() error {
	for {
		conn, err := Accept(server.listener)
		if err != nil {
			return err
		}
		go server.handleConnection(conn)
	}
}

func (server *EchoServer) Listener() net.Listener {
	return server.listener
}

func (server *EchoServer) handleConnection(conn net.Conn) {
	codec, err := server.protocol.NewCodec(conn)
	if err != nil {
		conn.Close()
		return
	}
	session := NewSession(codec, server.sendChSize)
	server.handler.HandleSession(session)
}

func (server *EchoServer) Stop() {
	server.listener.Close()
}

func Listen(network, address string, protocol Protocol, handler Handler, sendChSize int) (*EchoServer, error) {
	listener, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	return NewEchoServer(listener, protocol, handler, sendChSize), nil
}

func Dial(network, address string, protocol Protocol, sendChSize int) (*Session, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	codec, err := protocol.NewCodec(conn)
	if err != nil {
		return nil, err
	}
	return NewSession(codec, sendChSize), nil
}

func Accept(listener net.Listener) (net.Conn, error) {
	var tempDelay time.Duration
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				time.Sleep(tempDelay)
				continue
			}
			if strings.Contains(err.Error(), "use of closed network connection") {
				return nil, io.EOF
			}
			return nil, err
		}
		return conn, nil
	}
}
