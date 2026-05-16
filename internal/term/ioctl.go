//go:build js
// +build js

package term

import (
	"sync"
	"syscall/js"

	"github.com/hack-pad/hackpad/internal/common"
)

// TerminalInfo holds the dimensions of a terminal.
type TerminalInfo struct {
	Rows uint16
	Cols uint16
	Baud int
}

var (
	termMu    sync.RWMutex
	termInfos = map[common.FID]*TerminalInfo{}
)

// RegisterTerminal associates an fd with terminal info.
// Called when a terminal is spawned via hackpad's SpawnTerminal.
func RegisterTerminal(fd common.FID, info *TerminalInfo) {
	termMu.Lock()
	termInfos[fd] = info
	termMu.Unlock()
}

// UnregisterTerminal removes the terminal association for the given fd.
func UnregisterTerminal(fd common.FID) {
	termMu.Lock()
	delete(termInfos, fd)
	termMu.Unlock()
}

// Ioctl is the JS-callable ioctl handler.
// It is registered as hackpad.ioctl in main.go.
// Returns an object with {rows, cols, baudRate} for terminal fds, or null.
func Ioctl(this js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return nil
	}
	fd := common.FID(args[0].Int())

	termMu.RLock()
	info, ok := termInfos[fd]
	termMu.RUnlock()

	if !ok {
		return nil
	}

	return map[string]interface{}{
		"rows":     info.Rows,
		"cols":     info.Cols,
		"baudRate": info.Baud,
	}
}
