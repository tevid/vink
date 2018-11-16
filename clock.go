package vink

import (
	"sync"
	"time"
)

type SimpleClock struct {
	sync.Mutex
	interval time.Duration
	pos      int
	ticker   *time.Ticker

	exit       chan bool
	events     []chan bool
	maxTimeout time.Duration
}

func NewSimpleClock(interval time.Duration, buckets int) *SimpleClock {

	sc := new(SimpleClock)
	sc.interval = interval

	sc.exit = make(chan bool)
	sc.pos = 0
	sc.maxTimeout = time.Duration(interval * (time.Duration(buckets)))

	sc.events = make([]chan bool, buckets)

	for i := range sc.events {
		sc.events[i] = make(chan bool)
	}

	sc.ticker = time.NewTicker(interval)
	go sc.start()

	return sc
}

func (sc *SimpleClock) start() {

	for {
		select {
		case <-sc.ticker.C:
			sc.onEvent()
		case <-sc.exit:
			sc.ticker.Stop()
			return
		}
	}
}

func (sc *SimpleClock) onEvent() {
	sc.Lock()
	currentEvent := sc.events[sc.pos]
	sc.events[sc.pos] = make(chan bool)
	sc.pos = (sc.pos + 1) % len(sc.events)
	sc.Unlock()
	close(currentEvent)
}

func (sc *SimpleClock) Stop() {
	close(sc.exit)
}

func (sc *SimpleClock) After(timeout time.Duration) <-chan bool {

	if timeout > sc.maxTimeout {
		panic("timeout too much")
	}

	index := int(timeout / sc.interval)

	if index > 0 {
		index--
	}

	sc.Lock()
	index = (sc.pos + index) % len(sc.events)
	b := sc.events[index]
	sc.Unlock()
	return b
}
