package vink

import (
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

var PodClock = NewSimpleClock(time.Second, 1800)

type (
	Agent struct {
		proxyProtocol
		msgHandler   MsgHandler
		recvChanSize int
		session      *Session
		lastActive   int64
		newConnMutex sync.Mutex
		newConnChan  chan uint32
		dialMutex    sync.Mutex
		acceptChan   chan *Session
		connectChan  chan *Session
		sessionhub   *sessionHub
		pingChan     chan struct{}
		closeChan    chan struct{}
		closeFlag    int32
	}

	AgentConfig struct {
		MaxPacket       int
		MsgBufferSize   int
		MsgSendChanSize int
		MsgRecvChanSize int
		PingInterval    time.Duration
		PingTimeout     time.Duration
		TimeoutCallBack func() bool
		ServerId        uint32
		AuthKey         string
		msgHandler      MsgHandler
	}

	ConnInfo struct {
		connId   uint32
		remoteId uint32
	}
)

func (c *ConnInfo) ConnID() uint32 {
	return c.connId
}

func (c *ConnInfo) RemoteID() uint32 {
	return c.remoteId
}

func newAgent(maxPacketSize, recvChanSize int, msgHandler MsgHandler) *Agent {
	return &Agent{
		proxyProtocol: proxyProtocol{
			maxPacketSize: maxPacketSize,
		},
		msgHandler:   msgHandler,
		recvChanSize: recvChanSize,
		newConnChan:  make(chan uint32),
		acceptChan:   make(chan *Session, 1),
		connectChan:  make(chan *Session, 1000),
		sessionhub:   NewSessionHub(),
		closeChan:    make(chan struct{}),
	}
}

func DialServer(network, addr string, cfg AgentConfig) (*Agent, error) {
	conn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	return NewServer(conn, cfg)
}

func DialClient(network, addr string, cfg AgentConfig) (*Agent, error) {
	conn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	return NewClient(conn, cfg), nil
}

func NewServer(conn net.Conn, cfg AgentConfig) (*Agent, error) {

	agent := newAgent(cfg.MaxPacket, cfg.MsgRecvChanSize, cfg.msgHandler)

	if err := agent.recvAuth(conn, cfg.ServerId, []byte(cfg.AuthKey)); err != nil {
		GetLogger().Errorf("agent server auth error : %v", err)
		return nil, err
	}

	agent.session = NewSession(agent.newProxyCodec(0, conn, cfg.MsgBufferSize), cfg.MsgSendChanSize)

	go agent.loop()

	if cfg.PingInterval != 0 {
		if cfg.PingInterval > 1800*time.Second {
			panic("over ping interval limit")
		}

		if cfg.PingTimeout == 0 {
			panic("ping timeout is 0")
		}
		go agent.keepalive(cfg.PingInterval, cfg.PingTimeout, cfg.TimeoutCallBack)
	}

	return agent, nil
}

func NewClient(conn net.Conn, cfg AgentConfig) *Agent {

	agent := newAgent(cfg.MaxPacket, cfg.MsgRecvChanSize, cfg.msgHandler)

	agent.session = NewSession(agent.newProxyCodec(0, conn, cfg.MsgBufferSize), cfg.MsgSendChanSize)

	go agent.loop()

	if cfg.PingInterval != 0 {
		if cfg.PingInterval > 1800*time.Second {
			panic("over ping interval limit")
		}

		if cfg.PingTimeout == 0 {
			panic("ping timeout is 0")
		}

		go agent.keepalive(cfg.PingInterval, cfg.PingTimeout, cfg.TimeoutCallBack)
	}
	return agent
}

//读写Run-Loop
func (agent *Agent) loop() {
	defer func() {
		agent.Close()
		if err := recover(); err != nil {
			GetLogger().Errorf("panic info : %v\n%s", err, debug.Stack())
		}
	}()

	for {
		atomic.StoreInt64(&agent.lastActive, time.Now().UnixNano())

		msg, err := agent.session.Receive()
		if err != nil {
			return
		}

		buf := *(msg.(*[]byte))
		connId := agent.decodeCmdFid(buf)

		if connId == 0 {
			agent.processCmd(buf)
			continue
		}

		sess := agent.sessionhub.Get(connId)
		if sess != nil {
			sess.codec.(*agentCodec).pipeline(buf)
		} else {
			//buf = buf[:0]
			agent.send(agent.session, agent.encodeCloseCmd(connId))
		}
	}
}

//命令处理
func (agent *Agent) processCmd(buf []byte) {
	cmd := agent.decodeCmdType(buf)
	switch cmd {
	case CMD_ACCEPT:
		connId, remoteId := agent.decodeAcceptCmd(buf)
		buf = buf[:0]
		agent.addVirtualConn(connId, remoteId, agent.acceptChan)

	case CMD_REFUSE:
		buf = buf[:0]
		select {
		case agent.acceptChan <- nil:
		case <-agent.closeChan:
			return
		}

	case CMD_CONNECT:
		connId, remoteId := agent.decodeConnectCmd(buf)
		buf = buf[:0]
		agent.addVirtualConn(connId, remoteId, agent.connectChan)

	case CMD_CLOSE:
		connId := agent.decodeCloseCmd(buf)
		buf = buf[:0]
		sess := agent.sessionhub.Get(connId)
		if sess != nil {
			sess.Close()
		}

	case CMD_PING:
		agent.pingChan <- struct{}{}
		buf = buf[:0]

	default:
		buf = buf[:0]
		GetLogger().Errorf("unsupported cmd: %v", cmd)
		panic("unsupported cmd")
	}
}

//创建虚连接
func (agent *Agent) addVirtualConn(connId, remoteId uint32, c chan *Session) {
	codec := agent.newAgentCodec(agent.session, connId, agent.recvChanSize, &agent.lastActive, agent.msgHandler)
	session := NewSession(codec, 0)
	session.snapshot = &ConnInfo{connId, remoteId}
	agent.sessionhub.Put(connId, session)
	select {
	case c <- session:
	case <-agent.closeChan:
	default:
		agent.send(agent.session, agent.encodeCloseCmd(connId))
	}
}

func (agent *Agent) Accept() (*Session, error) {
	select {
	case sess := <-agent.connectChan:
		return sess, nil
	case <-agent.closeChan:
		return nil, ErrEOF
	}
}

func (agent *Agent) Dial(remoteId uint32) (*Session, error) {
	agent.dialMutex.Lock()
	defer agent.dialMutex.Unlock()

	if err := agent.send(agent.session, agent.encodeDialCmd(remoteId)); err != nil {
		//GetLogger().Error(err)
		return nil, err
	}
	select {
	case sess := <-agent.acceptChan:
		if sess == nil {
			return nil, ErrRefused
		}
		return sess, nil
	case <-agent.closeChan:
		GetLogger().Error("got eof")
		return nil, ErrEOF
	}
}

//关闭
func (agent *Agent) Close() {
	if atomic.CompareAndSwapInt32(&agent.closeFlag, 0, 1) {
		agent.session.Close()
		close(agent.closeChan)
	}
}

//保持连接
func (agent *Agent) keepalive(pingInterval, pingTimeout time.Duration, timeoutCallback func() bool) {
	agent.pingChan = make(chan struct{})
	for {
		select {
		case <-PodClock.After(pingTimeout):
			if agent.send(agent.session, agent.encodePingCmd()) != nil {
				return
			}
			select {
			case <-agent.pingChan:
			case <-PodClock.After(pingTimeout):
				if timeoutCallback != nil || !timeoutCallback() {
					return
				}
			case <-agent.closeChan:
				return
			}
		case <-agent.closeChan:
			return
		}
	}
}
