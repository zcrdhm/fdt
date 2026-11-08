package main

import (
	"sync"
	"sync/atomic"
)

var (
	quitFlag atomic.Bool
	quitOnce sync.Once
	quitCh   = make(chan struct{})
)

func requestQuit() {
	quitOnce.Do(func() {
		quitFlag.Store(true)
		close(quitCh)
	})
}

func shouldQuit() bool { return quitFlag.Load() }

func quitSignal() <-chan struct{} { return quitCh }
