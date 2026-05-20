//go:build js

package fs

import (
	"syscall/js"

	"github.com/hack-pad/hackpad/internal/common"
	"github.com/hack-pad/hackpad/internal/fs"
	"github.com/hack-pad/hackpad/internal/process"
	"github.com/pkg/errors"
)

const (
	// These match syscall.LOCK_SH, syscall.LOCK_EX, and syscall.LOCK_UN on
	// platforms where flock is available. The js/wasm target does not expose
	// those constants, so keep local equivalents for compatibility.
	lockShared    = 1
	lockExclusive = 2
	lockUnlock    = 8
)

func flock(args []js.Value) ([]interface{}, error) {
	_, err := flockSync(args)
	return nil, err
}

func flockSync(args []js.Value) (interface{}, error) {
	if len(args) != 2 {
		return nil, errors.Errorf("Invalid number of args, expected 2: %v", args)
	}
	fid := common.FID(args[0].Int())
	flag := args[1].Int()
	var action fs.LockAction
	shouldLock := true
	switch flag {
	case lockExclusive:
		action = fs.LockExclusive
	case lockShared:
		action = fs.LockShared
	case lockUnlock:
		action = fs.Unlock
	}

	return nil, Flock(fid, action, shouldLock)
}

func Flock(fid common.FID, action fs.LockAction, shouldLock bool) error {
	p := process.Current()
	return p.Files().Flock(fid, action)
}
