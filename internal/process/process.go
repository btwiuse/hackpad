package process

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"sync/atomic"

	"github.com/hack-pad/hackpad/internal/common"
	"github.com/hack-pad/hackpad/internal/fs"
	"github.com/hack-pad/hackpad/internal/log"
	"github.com/hack-pad/hackpadfs/keyvalue/blob"
	"github.com/pkg/errors"
)

const (
	minPID = 1
)

type PID = common.PID

type processState string

const (
	statePending   processState = "pending"
	stateCompiling processState = "compiling wasm"
	stateRunning   processState = "running"
	stateDone      processState = "done"
	stateError     processState = "error"
)

var (
	pids    = make(map[PID]*process)
	lastPID atomic.Uint64
)

func init() {
	lastPID.Store(minPID)
}

type Process interface {
	PID() PID
	ParentPID() PID

	Start() error
	Wait() (exitCode int, err error)
	Files() *fs.FileDescriptors
	WorkingDirectory() string
	SetWorkingDirectory(wd string) error
	Env() map[string]string
}

type process struct {
	pid, parentPID  PID
	command         string
	args            []string
	state           processState
	attr            *ProcAttr
	env             map[string]string
	ctx             context.Context
	ctxDone         context.CancelFunc
	exitCode        int
	err             error
	fileDescriptors *fs.FileDescriptors
	setFilesWD      func(wd string) error
}

func New(command string, args []string, attr *ProcAttr) (Process, error) {
	return newWithCurrent(Current(), ReservePID(), command, args, attr)
}

func ReservePID() PID {
	return PID(lastPID.Add(1))
}

func newWithCurrent(current Process, newPID PID, command string, args []string, attr *ProcAttr) (*process, error) {
	if attr == nil {
		attr = new(ProcAttr)
	}
	wd := current.WorkingDirectory()
	if attr.Dir != "" {
		wd = attr.Dir
	}
	env := current.Env()
	if len(attr.Env) != 0 {
		env = copyEnv(attr.Env)
	}
	files, setFilesWD, err := fs.NewFileDescriptors(newPID, wd, current.Files(), attr.Files)
	ctx, cancel := context.WithCancel(context.Background())
	return &process{
		pid:             newPID,
		parentPID:       current.PID(),
		command:         command,
		args:            args,
		state:           statePending,
		attr:            attr,
		env:             env,
		ctx:             ctx,
		ctxDone:         cancel,
		err:             err,
		fileDescriptors: files,
		setFilesWD:      setFilesWD,
	}, err
}

func NewIsolated(parentPID, newPID PID, command string, args []string, workingDirectory string, openFiles []common.OpenFileAttr, env map[string]string) (Process, error) {
	files, setFilesWD, err := fs.NewOpenFileDescriptors(newPID, workingDirectory, openFiles)
	ctx, cancel := context.WithCancel(context.Background())
	return &process{
		pid:             newPID,
		parentPID:       parentPID,
		command:         command,
		args:            args,
		state:           statePending,
		attr:            &ProcAttr{Dir: workingDirectory},
		env:             copyEnv(env),
		ctx:             ctx,
		ctxDone:         cancel,
		err:             err,
		fileDescriptors: files,
		setFilesWD:      setFilesWD,
	}, err
}

func (p *process) PID() PID {
	return p.pid
}

func (p *process) ParentPID() PID {
	return p.parentPID
}

func (p *process) Files() *fs.FileDescriptors {
	return p.fileDescriptors
}

func (p *process) Start() error {
	err := p.start()
	if p.err == nil {
		p.err = err
	}
	return p.err
}

func (p *process) start() error {
	pids[p.pid] = p
	log.Debugf("Spawning process: %v", p)
	go func() {
		command, err := p.prepExecutable()
		if err != nil {
			p.handleErr(err)
			return
		}
		p.run(command)
	}()
	return nil
}

func (p *process) prepExecutable() (command string, err error) {
	fs := p.Files()
	pathEnv := os.Getenv("PATH")
	if p.env != nil && p.env["PATH"] != "" {
		pathEnv = p.env["PATH"]
	}
	command, err = lookPath(fs.Stat, pathEnv, p.command)
	if err != nil {
		return "", err
	}
	fid, err := fs.Open(command, 0, 0)
	if err != nil {
		return "", err
	}
	defer fs.Close(fid)
	buf := blob.NewBytesLength(4)
	_, err = fs.Read(fid, buf, 0, buf.Len(), nil)
	if err != nil {
		return "", err
	}
	magicNumber := string(buf.Bytes())
	if magicNumber != "\x00asm" {
		return "", errors.Errorf("Format error. Expected Wasm file header but found: %q", magicNumber)
	}
	return command, nil
}

func (p *process) Done() {
	log.Debug("PID ", p.pid, " is done.\n", p.fileDescriptors)
	p.fileDescriptors.CloseAll()
	p.ctxDone()
}

func (p *process) handleErr(err error) {
	p.state = stateDone
	if err != nil {
		log.Errorf("Failed to start process: %s", err.Error())
		p.err = err
		p.state = stateError
	}
	p.Done()
}

func (p *process) Wait() (exitCode int, err error) {
	<-p.ctx.Done()
	return p.exitCode, p.err
}

func (p *process) WorkingDirectory() string {
	return p.Files().WorkingDirectory()
}

func (p *process) SetWorkingDirectory(wd string) error {
	return p.setFilesWD(wd)
}

func (p *process) Env() map[string]string {
	return copyEnv(p.env)
}

func (p *process) String() string {
	return fmt.Sprintf("PID=%s, Command=%v, State=%s, WD=%s, Attr=%+v, Err=%+v, Files:\n%v", p.pid, p.args, p.state, p.WorkingDirectory(), p.attr, p.err, p.fileDescriptors)
}

func Dump() interface{} {
	var s strings.Builder
	var pidSlice []PID
	for pid := range pids {
		pidSlice = append(pidSlice, pid)
	}
	sort.Slice(pidSlice, func(a, b int) bool {
		return pidSlice[a] < pidSlice[b]
	})
	for _, pid := range pidSlice {
		s.WriteString(pids[pid].String() + "\n")
	}
	return s.String()
}

func copyEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	envCopy := make(map[string]string, len(env))
	for k, v := range env {
		envCopy[k] = v
	}
	return envCopy
}
