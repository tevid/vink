package vink

import (
	. "github.com/tevid/gohamcrest"
	"math/rand"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

//---------------- proxy test ---------

func TestProxy(t *testing.T) {
	listener1, err := net.Listen("tcp", "127.0.0.1:0")
	Assert(t, err, NilVal())
	defer listener1.Close()

	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	Assert(t, err, NilVal())
	defer listener2.Close()

	proxy := NewProxy(TestMaxPacket)

	go proxy.ClientSideServ(listener1, TestProxyCfg)
	go proxy.ServiceSideServ(listener2, TestProxyCfg)

	time.Sleep(time.Second)

	client, err := DialClient("tcp", listener1.Addr().String(), TestAgentConfig)
	Assert(t, err, NilVal())

	server, err := DialServer("tcp", listener2.Addr().String(), TestAgentConfig)
	Assert(t, err, NilVal())

	OUTCOME := 0
	INCOME := 1

	CLIENT_PEER := 0
	SERVER_PEER := 1

L:
	for {
		n := 0
		for i := 0; i < len(proxy.sessionhubs); i++ {
			n += proxy.sessionhubs[i][CLIENT_PEER].Len()
			n += proxy.sessionhubs[i][SERVER_PEER].Len()
			if n >= 2 {
				break L
			}
		}
		runtime.Gosched()
	}

	var sesses [2][2]*Session
	var acceptChan = [2]chan int{
		make(chan int),
		make(chan int),
	}

	go func() {
		var err error

		//server-input
		sesses[CLIENT_PEER][OUTCOME], err = server.Accept()
		Assert(t, err, NilVal())
		acceptChan[CLIENT_PEER] <- 1

		sesses[SERVER_PEER][OUTCOME], err = client.Accept()
		Assert(t, err, NilVal())
		acceptChan[SERVER_PEER] <- 1

	}()

	connId := uint32(123)

	sesses[CLIENT_PEER][INCOME], err = client.Dial(connId)
	Assert(t, err, NilVal())
	<-acceptChan[CLIENT_PEER]

	sesses[SERVER_PEER][INCOME], err = server.Dial(sesses[CLIENT_PEER][OUTCOME].snapshot.(*ConnInfo).RemoteID())
	Assert(t, err, NilVal())
	<-acceptChan[SERVER_PEER]

	Assert(t, sesses[CLIENT_PEER][OUTCOME].snapshot.(*ConnInfo).ConnID(), Equal(sesses[CLIENT_PEER][INCOME].snapshot.(*ConnInfo).ConnID()))
	Assert(t, sesses[SERVER_PEER][OUTCOME].snapshot.(*ConnInfo).ConnID(), Equal(sesses[SERVER_PEER][INCOME].snapshot.(*ConnInfo).ConnID()))

	for i := 0; i < 1024; i++ {
		buffer1 := make([]byte, 1024)

		for i := 0; i < len(buffer1); i++ {
			buffer1[i] = byte(i)
		}

		x := rand.Intn(2)
		y := rand.Intn(2)

		err := sesses[x][y].Send(buffer1)
		Assert(t, err, NilVal())

		buffer2, err := sesses[x][(y+1)%2].Receive()
		Assert(t, err, NilVal())

		Assert(t, buffer1, Equal(buffer2))
	}

	sesses[CLIENT_PEER][OUTCOME].Close()
	sesses[CLIENT_PEER][INCOME].Close()
	sesses[SERVER_PEER][OUTCOME].Close()
	sesses[SERVER_PEER][INCOME].Close()

	time.Sleep(time.Second)

	Assert(t, 0, Equal(client.sessionhub.Len()))
	Assert(t, 0, Equal(server.sessionhub.Len()))

	for i := 0; i < len(proxy.peerMapList); i++ {
		proxy.peerMapLock[i].Lock()
		Assert(t, 0, Equal(len(proxy.peerMapList[i])))
		proxy.peerMapLock[i].Unlock()
	}

	proxy.Stop()
}

func TestProxyParallel(t *testing.T) {
	listener1, err := net.Listen("tcp", "127.0.0.1:0")
	Assert(t, err, NilVal())
	defer listener1.Close()

	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	Assert(t, err, NilVal())
	defer listener2.Close()

	proxy := NewProxy(TestMaxPacket)

	go proxy.ClientSideServ(listener1, TestProxyCfg)
	go proxy.ServiceSideServ(listener2, TestProxyCfg)

	time.Sleep(time.Second)

	client, err := DialClient("tcp", listener1.Addr().String(), TestAgentConfig)
	Assert(t, err, NilVal())

	server, err := DialServer("tcp", listener2.Addr().String(), TestAgentConfig)
	Assert(t, err, NilVal())

L:
	for {
		n := 0
		for i := 0; i < len(proxy.sessionhubs); i++ {
			n += proxy.sessionhubs[i][0].Len()
			n += proxy.sessionhubs[i][1].Len()
			if n >= 2 {
				break L
			}
		}
		time.Sleep(time.Second)
	}

	go func() {
		for {
			sess, err := server.Accept()
			if err != nil {
				return
			}
			go func() {
				for {
					msg, err := sess.Receive()
					if err != nil {
						return
					}
					if err := sess.Send(msg); err != nil {
						return
					}
				}
			}()
		}
	}()

	var wg sync.WaitGroup
	var errors = make([]error, runtime.GOMAXPROCS(-1))

	for i := 0; i < len(errors); i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			var sess *Session
			sess, errors[n] = client.Dial(123)
			if errors[n] != nil {
				return
			}
			defer sess.Close()

			times := 6666 + rand.Intn(2048)
			for i := 0; i < times; i++ {
				buffer1 := make([]byte, 1024)
				for i := 0; i < len(buffer1); i++ {
					buffer1[i] = byte(rand.Intn(256))
				}

				errors[n] = sess.Send(buffer1)
				if errors[n] != nil {
					return
				}

				var buffer2 interface{}
				buffer2, errors[n] = sess.Receive()
				if errors[n] != nil {
					return
				}
				Assert(t, buffer1, Equal(buffer2.([]byte)))
			}
		}(i)
	}
	wg.Wait()
	time.Sleep(time.Second)

	var failed bool
	for i := 0; i < len(errors); i++ {
		if !failed && errors[i] != nil {
			failed = true
		}
	}

	Assert(t, 0, Equal(client.sessionhub.Len()))
	Assert(t, 0, Equal(server.sessionhub.Len()))

	for i := 0; i < len(proxy.peerMapList); i++ {
		proxy.peerMapLock[i].Lock()
		Assert(t, 0, Equal(len(proxy.peerMapList[i])))
		proxy.peerMapLock[i].Unlock()
	}

	proxy.Stop()
}
