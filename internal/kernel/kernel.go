package kernel

import (
	"github.com/hack-pad/hackpad/internal/common"
	"sync/atomic"
)

const (
	minPID = 1
)

var (
	lastPID atomic.Uint64
)

func ReservePID() common.PID {
	if lastPID.Load() == 0 {
		lastPID.Store(minPID)
	}
	return common.PID(lastPID.Add(1))
}
