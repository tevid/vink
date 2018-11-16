package vink

import (
	"encoding/binary"
	"errors"
	"io"
	"runtime"
	"time"
)

// ---------------------
// 定义信息
//----------------------

var gByteOrder binary.ByteOrder = binary.BigEndian

var (
	ErrSessionClosed  = errors.New("session closed fail")
	ErrTooLargePacket = errors.New("too large packet size")
	ErrRefused        = errors.New("virtual connection refused error")
	ErrServerAuthFail = errors.New("server auth fail")
	ErrEOF            = io.EOF
)

const (
	//命令字信息
	CMD_DIAL = iota
	CMD_ACCEPT
	CMD_CONNECT
	CMD_REFUSE
	CMD_CLOSE
	CMD_PING
)

const (
	CLIENT_SIDE = 0
	SERVER_SIDE = 1

	PEER_NUM  = 2
	SIDE_NUM  = 2
	connSlots = 32
)

const (

	//长度信息
	ProtocolHead_LEN_SIZE      = 4 //协议头的长度数值大小
	ProtocolBody_CMD_FID_SIZE  = 4 //协议体的流水号大小
	ProtocolBody_CMD_TYPE_SIZE = 1 //协议体的命令字大小
	ProtocolBody_CMD_SIZE      = ProtocolBody_CMD_FID_SIZE + ProtocolBody_CMD_TYPE_SIZE

	//位置信息
	Position_Of_CMD      = ProtocolHead_LEN_SIZE
	Position_Of_CMD_TYPE = Position_Of_CMD + ProtocolBody_CMD_FID_SIZE
	Position_Of_MSG      = Position_Of_CMD_TYPE + ProtocolBody_CMD_TYPE_SIZE
)

const (
	//dial cmd = |fid|type|fid(remoteid)|
	CMD_SIZE_DIAL           = ProtocolBody_CMD_SIZE + ProtocolBody_CMD_FID_SIZE
	Postition_DIAL_REMOTEID = Position_Of_MSG

	//accept cmd = |fid|type|fid(connid)|fid(remoteid)|
	CMD_SIZE_ACCEPT            = ProtocolBody_CMD_SIZE + ProtocolBody_CMD_FID_SIZE + ProtocolBody_CMD_FID_SIZE
	Postition_ACCCEPT_CONNID   = Position_Of_MSG
	Postition_ACCCEPT_REMOTEID = Postition_ACCCEPT_CONNID + ProtocolBody_CMD_FID_SIZE

	//connect cmd = |fid|type|fid(connid)|fid(remoteid)|
	CMD_SIZE_CONNECT           = ProtocolBody_CMD_SIZE + ProtocolBody_CMD_FID_SIZE + ProtocolBody_CMD_FID_SIZE
	Postition_CONNECT_CONNID   = Position_Of_MSG
	Postition_CONNECT_REMOTEID = Postition_ACCCEPT_CONNID + ProtocolBody_CMD_FID_SIZE

	//refuse cmd = |fid|type|fid(remoteid)|
	CMD_SIZE_REFUSE           = ProtocolBody_CMD_SIZE + ProtocolBody_CMD_FID_SIZE
	Postition_REFUSE_REMOTEID = Position_Of_MSG

	//close cmd = |fid|type|fid(remoteid)|
	CMD_SIZE_CLOSE           = ProtocolBody_CMD_SIZE + ProtocolBody_CMD_FID_SIZE
	Postition_CLOSE_REMOTEID = Position_Of_MSG

	//ping cmd = |fid|type|
	CMD_SIZE_PING = ProtocolBody_CMD_SIZE
)

var (
	TestMaxConn      = 10000
	TestMaxPacket    = 2048
	TestBuffsize     = 1024
	TestSendChanSize = int(runtime.GOMAXPROCS(-1) * 6000)
	TestRecvChanSize = 6000
	TestIdleTimeout  = time.Second * 2
	TestPingInterval = time.Second
	TestPingTimeout  = time.Second
	TestAuthKey      = "123"
	TestServerId     = uint32(123)
)

var TestProxyCfg = ProxyConfig{
	MaxConn:          TestMaxConn,
	MsgBuffSize:      TestBuffsize,
	MsgSendChanSize:  TestSendChanSize,
	ProxyIdleTimeout: TestIdleTimeout,
	AuthKey:          TestAuthKey,
}

var TestAgentConfig = AgentConfig{
	MaxPacket:       TestMaxPacket,
	MsgBufferSize:   TestBuffsize,
	MsgSendChanSize: TestSendChanSize,
	MsgRecvChanSize: TestRecvChanSize,
	PingDuration:    TestPingInterval,
	PingTimeout:     TestPingTimeout,
	ServerId:        TestServerId,
	AuthKey:         TestAuthKey,
	msgHandler:      &TestMsgHandler{},
}

type TestMsgHandler struct {
}

func (f *TestMsgHandler) EncodeMessage(msg interface{}) ([]byte, error) {
	buf := make([]byte, len(msg.([]byte)))
	copy(buf, msg.([]byte))
	return buf, nil
}

func (f *TestMsgHandler) DecodeMessage(msg []byte) (interface{}, error) {
	buf := make([]byte, len(msg))
	copy(buf, msg)
	return buf, nil
}
