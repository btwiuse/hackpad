//go:build js

package process

import (
	"syscall/js"

	"github.com/hack-pad/hackpad/internal/common"
	"github.com/hack-pad/hackpad/internal/process"
	"github.com/pkg/errors"
)

type PIDer interface {
	PID() common.PID
}

type ProcessHandle interface {
	PIDer
	JSValue() js.Value
}

type Spawner interface {
	Spawn(command string, argv []string, attr *process.ProcAttr) (ProcessHandle, error)
}

type Waiter interface {
	Wait(pid common.PID) (exitCode int, err error)
}

type localSpawner struct{}
type localWaiter struct{}

var (
	configuredSpawner Spawner = localSpawner{}
	configuredWaiter  Waiter  = localWaiter{}
)

func SetSpawner(spawner Spawner, waiter Waiter) {
	if spawner == nil {
		configuredSpawner = localSpawner{}
	} else {
		configuredSpawner = spawner
	}
	if waiter == nil {
		configuredWaiter = localWaiter{}
	} else {
		configuredWaiter = waiter
	}
}

func (localSpawner) Spawn(command string, argv []string, attr *process.ProcAttr) (ProcessHandle, error) {
	p, err := process.New(command, argv, attr)
	if err != nil {
		return nil, err
	}
	handle, ok := p.(ProcessHandle)
	if !ok {
		return nil, errors.Errorf("invalid process handle type %T", p)
	}
	return handle, p.Start()
}

func (localWaiter) Wait(pid common.PID) (int, error) {
	p, ok := process.Get(pid)
	if !ok {
		return 0, errors.Errorf("Unknown child process: %d", pid)
	}
	return p.Wait()
}
