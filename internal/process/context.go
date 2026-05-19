package process

import (
	"strings"
	"syscall"

	"github.com/hack-pad/hackpad/internal/fs"
	"github.com/hack-pad/hackpad/internal/log"
)

const initialDirectory = "/home/me"

var (
	currentPID PID

	switchedContextListener func(newPID, parentPID PID)
)

func Init(switchedContext func(PID, PID)) {
	// create 'init' process
	fileDescriptors, err := fs.NewStdFileDescriptors(minPID, initialDirectory)
	if err != nil {
		panic(err)
	}
	p, err := newWithCurrent(
		&process{fileDescriptors: fileDescriptors},
		minPID,
		"",
		nil,
		&ProcAttr{Env: splitEnvPairs(syscall.Environ())},
	)
	if err != nil {
		panic(err)
	}
	p.state = stateRunning
	InitCurrent(p, switchedContext)
}

func InitCurrent(current Process, switchedContext func(PID, PID)) {
	pids[current.PID()] = current
	if uint64(current.PID()) > lastPID.Load() {
		lastPID.Store(uint64(current.PID()))
	}
	switchedContextListener = switchedContext
	switchContext(current.PID())
}

func switchContext(pid PID) (prev PID) {
	prev = currentPID
	log.Debug("Switching context from PID ", prev, " to ", pid)
	if pid == prev {
		return
	}
	newProcess := pids[pid]
	currentPID = pid
	switchedContextListener(pid, newProcess.ParentPID())
	return
}

func Current() Process {
	process, _ := Get(currentPID)
	return process
}

func Get(pid PID) (process Process, ok bool) {
	p, ok := pids[pid]
	return p, ok
}

func splitEnvPairs(pairs []string) map[string]string {
	env := make(map[string]string)
	for _, pair := range pairs {
		equalIndex := strings.IndexRune(pair, '=')
		if equalIndex == -1 {
			env[pair] = ""
		} else {
			key, value := pair[:equalIndex], pair[equalIndex+1:]
			env[key] = value
		}
	}
	return env
}
