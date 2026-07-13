package health

import "sync/atomic"

var (
	live  = atomic.Bool{}
	ready = atomic.Bool{}
)

func SetLiveness(isLive bool) {
	live.Store(isLive)
}

func GetLiveness() bool {
	return live.Load()
}

func SetReadiness(isReady bool) {
	ready.Store(isReady)
}

func GetReadiness() bool {
	return ready.Load()
}
