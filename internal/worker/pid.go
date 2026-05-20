//go:build js

package worker

import "github.com/hack-pad/hackpad/internal/common"

func nextChildPID(parent common.PID, counter *uint64) common.PID {
	*counter++
	return common.PID(uint64(parent)<<16 | *counter)
}
