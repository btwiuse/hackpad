//go:build js

package process

import (
	"syscall/js"

	"github.com/hack-pad/hackpad/internal/common"
	"github.com/hack-pad/hackpad/internal/interop"
	"github.com/hack-pad/hackpad/internal/process"
)

var jsProcess = js.Global().Get("process")

type processShim struct {
	process process.Process
	spawner Spawner
	waiter  Waiter
}

type PIDer interface {
	PID() common.PID
}

type Spawner interface {
	Spawn(command string, argv []string, attr *process.ProcAttr) (PIDer, error)
}

type Waiter interface {
	Wait(pid common.PID) (exitCode int, err error)
}

func Init() {
	process.Init(switchedContext)
	initJS(process.Current(), defaultSpawner{}, defaultWaiter{})
}

func InitCurrent(current process.Process, spawner Spawner, waiter Waiter) {
	process.InitCurrent(current, switchedContext)
	initJS(current, spawner, waiter)
}

func initJS(current process.Process, spawner Spawner, waiter Waiter) {
	shim := processShim{
		process: current,
		spawner: spawner,
		waiter:  waiter,
	}

	err := current.Files().MkdirAll(current.WorkingDirectory(), 0750)
	if err != nil {
		panic(err)
	}
	globals := js.Global()

	interop.SetFunc(jsProcess, "getuid", geteuid)
	interop.SetFunc(jsProcess, "geteuid", geteuid)
	interop.SetFunc(jsProcess, "getgid", getegid)
	interop.SetFunc(jsProcess, "getegid", getegid)
	interop.SetFunc(jsProcess, "getgroups", getgroups)
	jsProcess.Set("pid", current.PID().JSValue())
	jsProcess.Set("ppid", current.ParentPID().JSValue())
	interop.SetFunc(jsProcess, "umask", umask)
	interop.SetFunc(jsProcess, "cwd", cwd)
	interop.SetFunc(jsProcess, "chdir", chdir)

	globals.Set("child_process", map[string]interface{}{})
	childProcess := globals.Get("child_process")
	interop.SetFunc(childProcess, "spawn", shim.spawn)
	// interop.SetFunc(childProcess, "spawnSync", spawnSync) // TODO is there any way to run spawnSync so we don't hit deadlock?
	interop.SetFunc(childProcess, "wait", shim.wait)
	interop.SetFunc(childProcess, "waitSync", shim.waitSync)
}

func switchedContext(pid, ppid process.PID) {
	jsProcess.Set("pid", pid.JSValue())
	jsProcess.Set("ppid", ppid.JSValue())
}

func Dump() interface{} {
	return process.Dump()
}

func (s processShim) wait(args []js.Value) ([]interface{}, error) {
	ret, err := s.waitSync(args)
	return []interface{}{ret}, err
}

func (s processShim) waitSync(args []js.Value) (interface{}, error) {
	return waitSyncWith(s.waiter, args)
}

func (s processShim) spawn(args []js.Value) (interface{}, error) {
	return spawnWith(s.spawner, args)
}
