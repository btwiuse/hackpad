//go:build js

package worker

import (
	"context"
	"io"
	"syscall/js"

	"github.com/hack-pad/hackpad/internal/common"
	"github.com/hack-pad/hackpad/internal/interop"
	jsprocess "github.com/hack-pad/hackpad/internal/js/process"
	"github.com/hack-pad/hackpad/internal/jsworker"
	"github.com/hack-pad/hackpad/internal/log"
	"github.com/hack-pad/hackpad/internal/process"
	"github.com/hack-pad/hackpadfs"
	"github.com/pkg/errors"
)

type Local struct {
	localJS         *jsworker.Local
	process         process.Process
	processStartCtx context.Context
	pids            map[common.PID]*Remote
	nextPID         uint64
}

func NewLocal(ctx context.Context, localJS *jsworker.Local) (_ *Local, err error) {
	local := &Local{
		localJS: localJS,
		pids:    make(map[common.PID]*Remote),
	}
	init, err := local.awaitInit(ctx)
	if err != nil {
		return nil, err
	}

	command := init.Get("command")
	argv := init.Get("argv")
	workingDirectory := init.Get("workingDirectory")
	openFiles := init.Get("openFiles")
	env := init.Get("env")
	parsedOpenFiles, err := parseOpenFiles(openFiles)
	if err != nil {
		return nil, err
	}
	local.process, err = process.NewIsolated(
		common.PID(init.Get("ppid").Int()),
		common.PID(init.Get("pid").Int()),
		command.String(),
		interop.StringsFromJSValue(argv),
		workingDirectory.String(),
		parsedOpenFiles,
		stringMapFromJSObject(env),
	)
	if err != nil {
		return nil, err
	}
	process.SetCurrent(local.process)
	local.nextPID = uint64(local.process.PID()) << 16
	return local, nil
}

func (l *Local) awaitInit(ctx context.Context) (js.Value, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	l.processStartCtx = ctx

	type initMessage struct {
		err  error
		init js.Value
	}
	initChan := make(chan initMessage, 1)
	err := l.localJS.Listen(ctx, func(me jsworker.MessageEvent, err error) {
		if err != nil {
			initChan <- initMessage{err: err}
			return
		}
		if !me.Data.Truthy() || me.Data.Type() != js.TypeObject {
			return
		}
		initData := me.Data.Get("init")
		if !initData.Truthy() {
			return
		}
		initChan <- initMessage{init: initData}
	})
	if err != nil {
		return js.Value{}, err
	}
	if err := l.localJS.PostMessage(js.ValueOf("pending_init"), nil); err != nil {
		return js.Value{}, err
	}
	message := <-initChan
	return message.init, message.err
}

func (l *Local) Start() (err error) {
	startCtx, cancel := context.WithCancel(context.Background())
	err = l.localJS.Listen(startCtx, func(me jsworker.MessageEvent, err error) {
		if err != nil {
			log.Error(err)
			cancel()
			return
		}
		if me.Data.Type() != js.TypeObject || !me.Data.Get("start").Truthy() {
			return
		}
		cancel()
		if err := l.process.Start(); err != nil {
			log.Error(err)
		}
	})
	if err != nil {
		return err
	}
	return l.localJS.PostMessage(js.ValueOf("ready"), nil)
}

func (l *Local) Exit(exitCode int) error {
	if err := l.localJS.PostMessage(makeExitMessage(exitCode), nil); err != nil {
		return err
	}
	return l.localJS.Close()
}

func (l *Local) Spawn(command string, argv []string, attr *process.ProcAttr) (jsprocess.ProcessHandle, error) {
	pid := l.reservePID()
	remote, err := NewRemote(l.process, pid, command, argv, attr)
	if err != nil {
		return nil, err
	}
	l.pids[pid] = remote
	return remote, nil
}

func (l *Local) Wait(pid common.PID) (exitCode int, err error) {
	if pid == l.process.PID() {
		return l.process.Wait()
	}
	remote, ok := l.pids[pid]
	if !ok {
		return 0, errors.Errorf("Unknown child process: %d", pid)
	}
	return remote.Wait()
}

func (l *Local) Started() <-chan struct{} {
	return l.processStartCtx.Done()
}

func makeExitMessage(exitCode int) js.Value {
	return js.ValueOf(map[string]interface{}{
		"exitCode": exitCode,
	})
}

func parseOpenFiles(v js.Value) ([]common.OpenFileAttr, error) {
	openFileJSValues := interop.SliceFromJSValue(v)
	var openFiles []common.OpenFileAttr
	for _, o := range openFileJSValues {
		openFile := readOpenFile(o)
		var pipe io.ReadWriteCloser
		if openFile.pipe != nil {
			var err error
			pipe, err = portToReadWriteCloser(openFile.pipe)
			if err != nil {
				return nil, err
			}
		}
		openFiles = append(openFiles, common.OpenFileAttr{
			FilePath:   openFile.filePath,
			SeekOffset: openFile.seekOffset,
			Mode:       hackpadMode(pipe != nil),
			RawDevice:  pipe,
		})
	}
	return openFiles, nil
}

func (l *Local) reservePID() common.PID {
	return nextChildPID(l.process.PID(), &l.nextPID)
}

func (l *Local) PID() common.PID {
	if l.process == nil {
		return 0
	}
	return l.process.PID()
}

func stringMapFromJSObject(value js.Value) map[string]string {
	if !value.Truthy() {
		return nil
	}
	env := make(map[string]string)
	for name, prop := range interop.Entries(value) {
		env[name] = prop.String()
	}
	return env
}

func hackpadMode(isPipe bool) hackpadfs.FileMode {
	if isPipe {
		return hackpadfs.ModeNamedPipe
	}
	return 0
}
