//go:build js

package worker

import (
	"github.com/hack-pad/hackpad/internal/common"
	jsprocess "github.com/hack-pad/hackpad/internal/js/process"
	"github.com/hack-pad/hackpad/internal/process"
	"github.com/pkg/errors"
)

type DOM struct {
	pids map[common.PID]*Remote
}

var _ jsprocess.Spawner = (*DOM)(nil)
var _ jsprocess.Waiter = (*DOM)(nil)

func NewDOM() *DOM {
	return &DOM{
		pids: make(map[common.PID]*Remote),
	}
}

func (d *DOM) Spawn(command string, argv []string, attr *process.ProcAttr) (jsprocess.ProcessHandle, error) {
	if shouldRunInDOM(command) {
		p, err := process.New(command, argv, attr)
		if err != nil {
			return nil, err
		}
		handle, ok := p.(jsprocess.ProcessHandle)
		if !ok {
			return nil, errors.Errorf("invalid local process handle type %T", p)
		}
		return handle, p.Start()
	}
	parent := process.Current()
	pid := reserveChildPID(parent.PID())
	remote, err := NewRemote(parent, pid, command, argv, attr)
	if err != nil {
		return nil, err
	}
	d.pids[pid] = remote
	return remote, nil
}

func (d *DOM) Wait(pid common.PID) (int, error) {
	if remote, ok := d.pids[pid]; ok {
		return remote.Wait()
	}
	p, ok := process.Get(pid)
	if !ok {
		return 0, errors.Errorf("Unknown child process: %d", pid)
	}
	return p.Wait()
}

func shouldRunInDOM(command string) bool {
	switch command {
	case "editor":
		return true
	default:
		return false
	}
}

var nextChildPIDByParent = map[common.PID]uint64{}

func reserveChildPID(parent common.PID) common.PID {
	nextChildPIDByParent[parent]++
	return common.PID(uint64(parent)<<16 | nextChildPIDByParent[parent])
}
