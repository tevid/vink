package proto

import (
	"bytes"
	"encoding/binary"
	"github.com/tevid/vink"
	"io"
)

type SimpleProtocol struct {
}

func Simple() *SimpleProtocol {
	return &SimpleProtocol{}
}

func (s *SimpleProtocol) NewCodec(conn io.ReadWriter) (vink.Codec, error) {
	codec := &SimpleCodec{
		rw: conn.(io.ReadWriter),
	}
	return codec, nil
}

type SimpleCodec struct {
	preBuf bytes.Buffer //加入发生缓存区
	rw     io.ReadWriter
}

func (sc *SimpleCodec) Receive() (interface{}, error) {
	var head [8]byte
	_, err := io.ReadFull(sc.rw, head[:])
	if err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint64(head[:])
	buf := make([]byte, n)
	io.ReadFull(sc.rw, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (sc *SimpleCodec) Send(msg interface{}) error {

	var head [8]byte

	//在预处理缓冲区写好东西先
	sc.preBuf.Reset()
	sc.preBuf.Write(head[:])
	sc.preBuf.Write(msg.([]byte))

	buff := sc.preBuf.Bytes()

	//通过大端序覆盖前8位的数据
	bodylen := uint64(len(buff) - 8)
	binary.BigEndian.PutUint64(buff, bodylen)

	//数据写入
	_, err := sc.rw.Write(buff)
	if err != nil {
		return err
	}
	return nil
}

func (sc *SimpleCodec) Close() error {
	return sc.rw.(io.ReadWriteCloser).Close()
}
