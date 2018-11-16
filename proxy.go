package vink

import (
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type (
	// 代理层
	Proxy struct {
		proxyProtocol
		sideServers [SIDE_NUM]*EchoServer

		proxySessionId uint32
		sessionhubs    [connSlots][PEER_NUM]*sessionHub

		gVConnId    uint32
		peerMapList [connSlots]map[uint32]*Peers
		peerMapLock [connSlots]sync.RWMutex
	}

	Peers struct {
		cs [PEER_NUM]*Session
	}

	ProxyConfig struct {
		MaxConn          int
		MsgBuffSize      int
		MsgSendChanSize  int
		ProxyIdleTimeout time.Duration
		AuthKey          string
	}
)

func (ps *Peers) GetPeers() [2]*Session {
	return ps.cs
}

func NewProxy(maxPacketSize int) *Proxy {

	proxy := new(Proxy)
	proxy.maxPacketSize = maxPacketSize

	//初始化分配
	for i := 0; i < connSlots; i++ {
		proxy.peerMapList[i] = make(map[uint32]*Peers)
		proxy.sessionhubs[i][CLIENT_SIDE] = NewSessionHub()
		proxy.sessionhubs[i][SERVER_SIDE] = NewSessionHub()
	}

	return proxy
}

//针对服务端的服务
func (p *Proxy) ServiceSideServ(ln net.Listener, cfg ProxyConfig) {
	p.sideServers[SERVER_SIDE] = NewEchoServer(ln,
		ProtocolFunc(func(rw io.ReadWriter) (Codec, error) {
			serverId, err := p.sendAuth(rw.(net.Conn), []byte(cfg.AuthKey))
			if err != nil {
				GetLogger().Errorf("error accept server from %s: %s", rw.(net.Conn).RemoteAddr(), err)
				return nil, err
			}
			GetLogger().Infof("accept server %d from %s", serverId, rw.(net.Conn).RemoteAddr())
			return p.newProxyCodec(serverId, rw.(net.Conn), cfg.MsgBuffSize), nil
		}),
		HandlerFunc(func(session *Session) {
			p.handleSession(session, SERVER_SIDE, 0, cfg.ProxyIdleTimeout)
		}),
		cfg.MsgSendChanSize)

	p.sideServers[SERVER_SIDE].Serve()
}

//针对客户端的服务
func (p *Proxy) ClientSideServ(ln net.Listener, cfg ProxyConfig) {
	p.sideServers[CLIENT_SIDE] = NewEchoServer(ln,
		ProtocolFunc(func(rw io.ReadWriter) (Codec, error) {
			fid := atomic.AddUint32(&p.proxySessionId, 1)
			return p.newProxyCodec(fid, rw.(net.Conn), cfg.MsgBuffSize), nil
		}),
		HandlerFunc(func(session *Session) {
			p.handleSession(session, CLIENT_SIDE, cfg.MaxConn, cfg.ProxyIdleTimeout)
		}),
		cfg.MsgSendChanSize)

	p.sideServers[CLIENT_SIDE].Serve()
}

//处理会话信息
func (p *Proxy) handleSession(session *Session, side, maxConn int, idleTimeout time.Duration) {

	fid := session.codec.(*proxyCodec).id
	record := p.newSessionRecord(fid, session)
	session.snapshot = record

	p.addconnSession(fid, side, session)

	defer func() {
		record.Delete()
		if err := recover(); err != nil {
			GetLogger().Error("Proxy Panic:", err)
		}
	}()

	otherside := 1 &^ side

	GetLogger().Infof("side = %d , othersize = %d ", side, otherside)

	for {

		//设置最大空闲处理
		if idleTimeout > 0 {

			err := session.codec.(*proxyCodec).conn.SetDeadline(time.Now().Add(idleTimeout))
			if err != nil {
				GetLogger().Error(err)
				return
			}
		}

		buf, err := session.Receive()

		if err != nil {
			//GetLogger().Error(err)
			return
		}

		msg := *(buf.(*[]byte))
		connId := p.decodeCmdFid(msg)

		if connId == 0 {
			p.processCmd(msg, session, side, maxConn)
			continue
		}

		//获取对端信息
		peers := p.getPeers(connId)

		if peers.GetPeers()[side] == nil || peers.GetPeers()[otherside] == nil {
			msg = msg[:0]
			p.send(session, p.encodeCloseCmd(connId))
			continue
		}

		if peers.GetPeers()[side] != session {
			msg = msg[:0]
			panic("peer session info not match")
		}
		p.send(peers.GetPeers()[otherside], msg)
	}
}

func (p *Proxy) processCmd(msg []byte, session *Session, side, maxConn int) {
	otherside := 1 &^ side
	cmd := p.decodeCmdType(msg)
	GetLogger().Debugf("%v | proxy processCmd = %d", msg, cmd)

	switch cmd {
	case CMD_DIAL:
		remoteId := p.decodeDialCmd(msg)
		msg = msg[:0]

		var peers [2]*Session
		peers[side] = session
		peers[otherside] = p.getConnSession(remoteId, otherside)

		info := &Peers{peers}

		if peers[otherside] == nil || !p.acceptPeers(info, session, maxConn) {
			p.send(session, p.encodeRefuseCmd(remoteId))
		}
	case CMD_CLOSE:
		connId := p.decodeCloseCmd(msg)
		msg = msg[:0]
		p.closePeers(connId)

	case CMD_PING:
		msg = msg[:0]
		p.send(session, p.encodePingCmd())

	default:
		msg = msg[:0]
		panic(fmt.Sprintf("unsupported proxy command : %d", cmd))
	}
}

func (p *Proxy) Stop() {
	p.sideServers[SERVER_SIDE].Stop()
	p.sideServers[CLIENT_SIDE].Stop()
}

//会话快照信息
type SessionRecord struct {
	sync.Mutex
	fid        uint32
	proxy      *Proxy
	session    *Session
	lastActive int64
	hbChan     chan struct{}
	connsMap   map[uint32]struct{}
	deleteOnce sync.Once
	deleted    bool
}

func (p *Proxy) newSessionRecord(fid uint32, session *Session) *SessionRecord {
	return &SessionRecord{
		fid:      fid,
		session:  session,
		proxy:    p,
		hbChan:   make(chan struct{}),
		connsMap: make(map[uint32]struct{}),
	}
}

func (record *SessionRecord) Delete() {
	record.deleteOnce.Do(func() {
		record.session.Close()
		record.Lock()
		record.deleted = true
		record.Unlock()

		//关闭与之关联的peer虚连接
		for fid := range record.connsMap {
			record.proxy.closePeers(fid)
		}
	})
}

//put到channel后会在session中注册一个closedcallback
//session关闭后，会自动清除这个关系表
func (p *Proxy) addconnSession(connId uint32, side int, session *Session) {
	p.sessionhubs[connId%connSlots][side].Put(connId, session)
}

func (p *Proxy) getConnSession(connId uint32, side int) *Session {
	return p.sessionhubs[connId%connSlots][side].Get(connId)
}

//添加虚链接
func (p *Proxy) addPeers(connId uint32, peers *Peers) {
	slotIndex := connId % connSlots
	p.peerMapLock[slotIndex].Lock()
	defer p.peerMapLock[slotIndex].Unlock()
	if _, exists := p.peerMapList[slotIndex][connId]; exists {
		panic("virtual connection already exists")
	}
	p.peerMapList[slotIndex][connId] = peers
}

//获取虚链接
func (p *Proxy) getPeers(connId uint32) *Peers {
	slotIndex := connId % connSlots
	p.peerMapLock[slotIndex].Lock()
	defer p.peerMapLock[slotIndex].Unlock()
	return p.peerMapList[slotIndex][connId]
}

//删除虚链接
func (p *Proxy) delPeers(connId uint32) (*Peers, bool) {
	slotIndex := connId % connSlots
	p.peerMapLock[slotIndex].Lock()
	pi, ok := p.peerMapList[slotIndex][connId]
	//如果虚链接的结构全部已经清理了，是 非ok, 这样返回 false
	if ok {
		delete(p.peerMapList[slotIndex], connId)
	}
	p.peerMapLock[slotIndex].Unlock()
	return pi, ok
}

//接受虚链接请求
func (p *Proxy) acceptPeers(peers *Peers, session *Session, maxConn int) bool {
	var connId uint32
	for connId == 0 {
		connId = atomic.AddUint32(&p.gVConnId, 1)
	}

	for i := 0; i < PEER_NUM; i++ {
		record := peers.GetPeers()[i].snapshot.(*SessionRecord)
		record.Lock()
		defer record.Unlock()

		if record.deleted {
			GetLogger().Error("proxy was deleted")
			return false
		}

		if peers.GetPeers()[i] == session && maxConn != 0 && len(record.connsMap) >= maxConn {
			GetLogger().Error("session connections size over maxconn")
			return false
		}

		if _, exists := record.connsMap[connId]; exists {
			panic("peers connection already exists")
		}
		record.connsMap[connId] = struct{}{}
	}

	//更新虚链接信息
	p.addPeers(connId, peers)

	pcount := len(peers.GetPeers())

	for i := 0; i < pcount; i++ {
		//other size cid
		remoteId := peers.GetPeers()[(i+1)%pcount].snapshot.(*SessionRecord).fid
		if peers.GetPeers()[i] == session {
			p.send(peers.GetPeers()[i], p.encodeAcceptCmd(connId, remoteId))
		} else {
			p.send(peers.GetPeers()[i], p.encodeConnectCmd(connId, remoteId))
		}
	}
	return true
}

//关闭虚链接
func (p *Proxy) closePeers(connId uint32) {
	peers, ok := p.delPeers(connId)
	if !ok {
		return
	}
	for i := 0; i < len(peers.GetPeers()); i++ {
		record := peers.GetPeers()[i].snapshot.(*SessionRecord)
		record.Lock()
		defer record.Unlock()
		if record.deleted {
			continue
		}
		//只清集合中的信息，和发送清理信息，不直接对session有任何关闭操作
		delete(record.connsMap, connId)
		p.send(peers.GetPeers()[i], p.encodeCloseCmd(connId))
	}
}
