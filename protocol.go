package vink

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type (
	//协议接口
	Protocol interface {
		NewCodec(rw io.ReadWriter) (Codec, error)
	}

	Codec interface {
		Receive() (interface{}, error)
		Send(interface{}) error
		Close() error
	}

	//包体处理器
	MsgHandler interface {
		DecodeMessage([]byte) (interface{}, error)
		EncodeMessage(interface{}) ([]byte, error)
	}

	//会话处理器
	Handler interface {
		HandleSession(session *Session)
	}

	ProtocolFunc func(rw io.ReadWriter) (Codec, error)
	HandlerFunc  func(session *Session)

	proxyProtocol struct {
		maxPacketSize int
	}

	proxyCodec struct {
		*proxyProtocol
		conn      net.Conn
		id        uint32
		br        *bufio.Reader
		headerBuf []byte
		header    [ProtocolHead_LEN_SIZE]byte
	}

	agentCodec struct {
		*proxyProtocol
		netConn     *Session
		connId      uint32
		recvChan    chan []byte
		closeMutex  sync.Mutex
		closed      bool
		lastActived *int64
		msgHandler  MsgHandler
	}
)

func (pf ProtocolFunc) NewCodec(rw io.ReadWriter) (Codec, error) {
	return pf(rw)
}

func (hf HandlerFunc) HandleSession(session *Session) {
	hf(session)
}

//--------------
//自定义protocol
//--------------

func (proto *proxyProtocol) encodeCmd(t byte, cmdsize int) []byte {
	buff := make([]byte, ProtocolHead_LEN_SIZE+cmdsize)
	gByteOrder.PutUint32(buff, uint32(cmdsize))
	gByteOrder.PutUint32(buff[Position_Of_CMD:], 0)
	buff[Position_Of_CMD_TYPE] = t
	return buff
}

func (proto *proxyProtocol) decodeCmdType(data []byte) byte {
	return data[Position_Of_CMD_TYPE]
}

func (proto *proxyProtocol) decodeCmdFid(data []byte) uint32 {
	return gByteOrder.Uint32(data[Position_Of_CMD:])
}

func (p *proxyProtocol) send(session *Session, buf []byte) error {
	err := session.Send(buf)
	if err != nil {
		GetLogger().Errorf("proxy protocol send error: %v", err)
		session.Close()
	}
	return err

}
func (p *proxyProtocol) sendv(session *Session, buf [][]byte) error {
	err := session.Send(buf)
	if err != nil {
		GetLogger().Errorf("proxy protocol send error: %v", err)
		session.Close()
	}
	return err
}

//服务运行初始化
func (p *proxyProtocol) recvAuth(conn net.Conn, serverId uint32, key []byte) error {

	var buf [md5.Size + ProtocolBody_CMD_FID_SIZE]byte

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := io.ReadFull(conn, buf[:8]); err != nil {
		conn.Close()
		return err
	}

	hash := md5.New()
	hash.Write(buf[:8])
	hash.Write(key)
	verify := hash.Sum(nil)

	//buf复用
	copy(buf[:md5.Size], verify)
	gByteOrder.PutUint32(buf[md5.Size:], serverId)

	if _, err := conn.Write(buf[:]); err != nil {
		conn.Close()
		return err
	}
	conn.SetDeadline(time.Time{})
	return nil
}

//服务验证
func (p *proxyProtocol) sendAuth(conn net.Conn, key []byte) (uint32, error) {
	var buf [md5.Size + ProtocolBody_CMD_FID_SIZE]byte
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	rand.Read(buf[:8])
	if _, err := conn.Write(buf[:8]); err != nil {
		conn.Close()
		return 0, err
	}

	hash := md5.New()
	hash.Write(buf[:8])
	hash.Write(key)
	verify := hash.Sum(nil)

	if _, err := io.ReadFull(conn, buf[:]); err != nil {
		conn.Close()
		return 0, err
	}

	if !bytes.Equal(verify, buf[:md5.Size]) {
		conn.Close()
		return 0, ErrServerAuthFail
	}
	conn.SetDeadline(time.Time{})
	return gByteOrder.Uint32(buf[md5.Size:]), nil
}

//--------------
// 自定义 codec
//---------------

func (proto *proxyProtocol) newProxyCodec(id uint32, conn net.Conn, bufferSize int) *proxyCodec {
	pc := &proxyCodec{
		conn:          conn,
		id:            id,
		proxyProtocol: proto,
		br:            bufio.NewReaderSize(conn, bufferSize),
	}
	pc.headerBuf = pc.header[:]
	return pc
}

//数据接收
func (c *proxyCodec) Receive() (interface{}, error) {

	//先把头信息读处理
	//头信息包含 length
	if _, err := io.ReadFull(c.br, c.headerBuf); err != nil {

		return nil, err
	}

	//因为采用大端序作为默认字节序，所以通过大端序获取消息长度

	length := int(gByteOrder.Uint32(c.headerBuf))
	if length > c.maxPacketSize {
		return nil, ErrTooLargePacket
	}

	buff := make([]byte, ProtocolHead_LEN_SIZE+length)
	//把头信息写回去
	copy(buff, c.headerBuf)

	if _, err := io.ReadFull(c.br, buff[ProtocolHead_LEN_SIZE:]); err != nil {
		buff = buff[:0]
		return nil, err
	}
	//返回地址，以防后续跟进这个指针回收
	return &buff, nil

}

//数据发送
func (c *proxyCodec) Send(msg interface{}) error {
	if buff, ok := msg.([][]byte); ok {
		nb := net.Buffers(buff)
		_, err := nb.WriteTo(c.conn)
		return err
	}
	_, err := c.conn.Write(msg.([]byte))
	return err
}

//链接关闭
func (c *proxyCodec) Close() error {
	return c.conn.Close()
}

func (p *proxyProtocol) newAgentCodec(netConn *Session, connId uint32, recvChanSize int, lastActived *int64, msgHandler MsgHandler) *agentCodec {
	return &agentCodec{
		proxyProtocol: p,
		netConn:       netConn,
		connId:        connId,
		recvChan:      make(chan []byte, recvChanSize),
		lastActived:   lastActived,
		msgHandler:    msgHandler,
	}
}

//消息过滤，去掉消息头
func (v *agentCodec) pipeline(buf []byte) {
	v.closeMutex.Lock()
	if v.closed {
		v.closeMutex.Unlock()
		buf = buf[:0]
		return
	}
	select {
	case v.recvChan <- buf:
		v.closeMutex.Unlock()
		return
	default:
		v.closeMutex.Unlock()
		v.Close()
		buf = buf[:0]
	}
}

//接收传输消息体
func (v *agentCodec) Receive() (interface{}, error) {

	buf, ok := <-v.recvChan
	if !ok {
		return nil, ErrEOF
	}

	defer func() {
		buf = buf[:0]
	}()

	//反序列化消息
	return v.msgHandler.DecodeMessage(
		buf[ProtocolHead_LEN_SIZE+ProtocolBody_CMD_FID_SIZE:])
}

//发送传输消息体
func (v *agentCodec) Send(msg interface{}) error {

	//序列化消息
	data, err := v.msgHandler.EncodeMessage(msg)
	if err != nil {
		return err
	}

	//包体过大
	if len(data) > v.maxPacketSize {
		return ErrTooLargePacket
	}

	headBuf := make([]byte, ProtocolHead_LEN_SIZE+ProtocolBody_CMD_FID_SIZE)
	gByteOrder.PutUint32(headBuf, uint32(ProtocolBody_CMD_FID_SIZE+len(data))) //设置长度
	gByteOrder.PutUint32(headBuf[Position_Of_CMD:], v.connId)                  //设置connid

	buffers := make([][]byte, 2)
	buffers[0] = headBuf
	buffers[1] = data

	err = v.sendv(v.netConn, buffers)
	if err != nil {
		atomic.StoreInt64(v.lastActived, time.Now().Unix())
	}
	return err
}

func (v *agentCodec) Close() error {
	v.closeMutex.Lock()
	if !v.closed {
		v.closed = true
		close(v.recvChan)
		v.send(v.netConn, v.encodeCloseCmd(v.connId))
	}
	v.closeMutex.Unlock()

	for buf := range v.recvChan {
		buf = buf[:0]
	}
	return nil
}

//--------------
// 命令封装
//---------------

func (p *proxyProtocol) decodeDialCmd(data []byte) uint32 {
	return gByteOrder.Uint32(data[Postition_DIAL_REMOTEID:])
}

func (p *proxyProtocol) encodeDialCmd(remoteId uint32) []byte {
	buffer := p.encodeCmd(CMD_DIAL, CMD_SIZE_DIAL)
	gByteOrder.PutUint32(buffer[Postition_DIAL_REMOTEID:], remoteId)
	return buffer
}

func (p *proxyProtocol) decodeAcceptCmd(data []byte) (connId, remoteId uint32) {
	connId = gByteOrder.Uint32(data[Postition_ACCCEPT_CONNID:])
	remoteId = gByteOrder.Uint32(data[Postition_ACCCEPT_REMOTEID:])
	return
}

func (p *proxyProtocol) encodeAcceptCmd(connId, remoteId uint32) []byte {
	buffer := p.encodeCmd(CMD_ACCEPT, CMD_SIZE_ACCEPT)
	gByteOrder.PutUint32(buffer[Postition_ACCCEPT_CONNID:], connId)
	gByteOrder.PutUint32(buffer[Postition_ACCCEPT_REMOTEID:], remoteId)
	return buffer
}

func (p *proxyProtocol) decodeConnectCmd(data []byte) (connId, remoteId uint32) {
	connId = gByteOrder.Uint32(data[Postition_CONNECT_CONNID:])
	remoteId = gByteOrder.Uint32(data[Postition_CONNECT_REMOTEID:])
	return
}

func (p *proxyProtocol) encodeConnectCmd(connId, remoteId uint32) []byte {
	buffer := p.encodeCmd(CMD_CONNECT, CMD_SIZE_CONNECT)
	gByteOrder.PutUint32(buffer[Postition_CONNECT_CONNID:], connId)
	gByteOrder.PutUint32(buffer[Postition_CONNECT_REMOTEID:], remoteId)
	return buffer
}

func (p *proxyProtocol) decodeRefuseCmd(data []byte) (remoteId uint32) {
	remoteId = gByteOrder.Uint32(data[Postition_REFUSE_REMOTEID:])
	return
}

func (p *proxyProtocol) encodeRefuseCmd(remoteId uint32) []byte {
	buffer := p.encodeCmd(CMD_REFUSE, CMD_SIZE_REFUSE)
	gByteOrder.PutUint32(buffer[Postition_REFUSE_REMOTEID:], remoteId)
	return buffer
}

func (p *proxyProtocol) decodeCloseCmd(data []byte) (connId uint32) {
	connId = gByteOrder.Uint32(data[Postition_CLOSE_REMOTEID:])
	return
}

func (p *proxyProtocol) encodeCloseCmd(connId uint32) []byte {
	buffer := p.encodeCmd(CMD_CLOSE, CMD_SIZE_CLOSE)
	gByteOrder.PutUint32(buffer[Postition_CLOSE_REMOTEID:], connId)
	return buffer
}

func (p *proxyProtocol) encodePingCmd() []byte {
	return p.encodeCmd(CMD_PING, CMD_SIZE_PING)
}
