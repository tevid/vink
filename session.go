package vink

import (
	"sync"
	"sync/atomic"
)

var gSessionId uint64 = 0

//会话信息
type Session struct {
	id       uint64
	codec    Codec
	snapshot interface{}

	sendCh   chan interface{}
	sendLock sync.RWMutex

	recvLock sync.Mutex

	closeFlag     int32
	closeCh       chan int
	closeLock     sync.Mutex
	headCloseHook *closeHook
	tailCloseHook *closeHook
}

//新建会话
func NewSession(codec Codec, sendChSize int) *Session {
	session := &Session{
		id:      atomic.AddUint64(&gSessionId, 1),
		codec:   codec,
		closeCh: make(chan int),
	}

	if sendChSize > 0 {
		session.sendCh = make(chan interface{}, sendChSize)
		go session.asyncSend()
	}
	return session
}

func (session *Session) Receive() (interface{}, error) {
	session.recvLock.Lock()
	defer session.recvLock.Unlock()

	msg, err := session.codec.Receive()
	if err != nil {
		session.Close()
	}
	return msg, err
}

//同步发送
func (session *Session) Send(msg interface{}) error {
	if session.sendCh == nil {
		if session.IsSessionClosed() {
			return ErrSessionClosed
		}

		session.sendLock.Lock()
		defer session.sendLock.Unlock()

		err := session.codec.Send(msg)
		if err != nil {
			session.Close()
		}
		return err
	}

	session.sendLock.RLock()
	if session.IsSessionClosed() {
		session.sendLock.RUnlock()
		return ErrSessionClosed
	}

	select {
	case session.sendCh <- msg:
		session.sendLock.RUnlock()
		return nil
	default:
		session.sendLock.RUnlock()
		session.Close()
		return ErrSessionClosed
	}
}

//异步发送
func (session *Session) asyncSend() {
	defer session.Close()
	for {
		select {
		case data, ok := <-session.sendCh:
			if !ok || session.codec.Send(data) != nil { //发送失败
				return
			}
		case <-session.closeCh: //主动关闭
			return
		}
	}
}

func (session *Session) IsSessionClosed() bool {
	return atomic.LoadInt32(&session.closeFlag) == 1
}

func (session *Session) Close() error {
	if atomic.CompareAndSwapInt32(&session.closeFlag, 0, 1) {
		close(session.closeCh)

		if session.sendCh != nil {
			session.sendLock.Lock()
			close(session.sendCh)
			session.sendLock.Unlock()
		}

		//关闭协议传输
		err := session.codec.Close()

		go func() {
			session.triggerCloseHooks()
		}()

		return err
	}
	return ErrSessionClosed
}

//触发回调
func (session *Session) triggerCloseHooks() {
	session.closeLock.Lock()
	defer session.closeLock.Unlock()
	for hook := session.headCloseHook; hook != nil; hook = hook.Next {
		hook.hookFunc()
	}
}

//添加回调
func (session *Session) AddCloseHook(key interface{}, hookFunc func()) {

	//关闭状态下，禁止添加
	if session.IsSessionClosed() {
		return
	}

	session.closeLock.Lock()
	defer session.closeLock.Unlock()

	hook := new(closeHook)
	hook.Name = key
	hook.Next = nil
	hook.hookFunc = hookFunc

	if session.headCloseHook == nil {
		session.headCloseHook = hook
	} else {
		session.tailCloseHook.Next = hook
	}
	session.tailCloseHook = hook
}

//移除回调
func (session *Session) RemoveCloseHook(key interface{}) {
	//关闭状态下，禁止添加
	if session.IsSessionClosed() {
		return
	}

	session.closeLock.Lock()
	defer session.closeLock.Unlock()

	var prevHook *closeHook

	for hook := session.headCloseHook; hook != nil; prevHook, hook = hook, hook.Next {
		if hook.Name == key {

			//如果 node 是链头
			if session.headCloseHook == hook {
				session.headCloseHook = hook.Next
			} else {
				//移除 node
				prevHook.Next = hook.Next
			}

			//如果 node 是链尾
			if session.tailCloseHook == hook {
				session.tailCloseHook = prevHook
			}

			return
		}
	}
}

//关闭钩子
type closeHook struct {
	Next     *closeHook
	Name     interface{}
	hookFunc func()
}

//会话集合
type sessionHub struct {
	mutex    sync.RWMutex
	sessions map[uint32]*Session
	State    interface{}
}

func NewSessionHub() *sessionHub {
	return &sessionHub{
		sessions: make(map[uint32]*Session),
	}
}

func (sh *sessionHub) Len() int {
	sh.mutex.RLock()
	defer sh.mutex.RUnlock()
	return len(sh.sessions)
}

func (sh *sessionHub) Fetch(callback func(*Session)) {
	sh.mutex.RLock()
	defer sh.mutex.RUnlock()
	for _, session := range sh.sessions {
		callback(session)
	}
}

func (sh *sessionHub) Get(key uint32) *Session {
	sh.mutex.RLock()
	defer sh.mutex.RUnlock()
	session, _ := sh.sessions[key]
	return session
}

func (sh *sessionHub) Put(key uint32, session *Session) {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()
	if session, exists := sh.sessions[key]; exists {
		sh.remove(key, session)
	}
	session.AddCloseHook(key, func() {
		sh.Remove(key)
	})
	sh.sessions[key] = session
}

func (sh *sessionHub) remove(key uint32, session *Session) {
	session.RemoveCloseHook(key)
	delete(sh.sessions, key)
}

func (sh *sessionHub) Remove(key uint32) bool {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()
	session, exists := sh.sessions[key]
	if exists {
		sh.remove(key, session)
	}
	return exists
}

func (sh *sessionHub) FetchAndRemove(callback func(*Session)) {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()
	for key, session := range sh.sessions {
		sh.remove(key, session)
		callback(session)
	}
}

func (sh *sessionHub) Close() {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()
	for key, session := range sh.sessions {
		sh.remove(key, session)
	}
}
