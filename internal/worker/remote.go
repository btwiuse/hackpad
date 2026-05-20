//go:build js

package worker

import (
	"context"
	"fmt"
	"syscall/js"

	"github.com/hack-pad/hackpad/internal/common"
	"github.com/hack-pad/hackpad/internal/fs"
	"github.com/hack-pad/hackpad/internal/interop"
	jsprocess "github.com/hack-pad/hackpad/internal/js/process"
	"github.com/hack-pad/hackpad/internal/jsworker"
	"github.com/hack-pad/hackpad/internal/log"
	"github.com/hack-pad/hackpad/internal/process"
	"github.com/hack-pad/hackpadfs"
	"github.com/hack-pad/hackpadfs/indexeddb/idbblob"
	"github.com/hack-pad/hackpadfs/keyvalue/blob"
)

type Remote struct {
	pid           common.PID
	parentPID     common.PID
	port          *jsworker.Remote
	closeCtx      context.Context
	closeExitCode *int
	closeErr      error
}

var _ jsprocess.ProcessHandle = (*Remote)(nil)

func NewRemote(parent process.Process, pid process.PID, command string, argv []string, attr *process.ProcAttr) (*Remote, error) {
	if attr == nil {
		attr = new(process.ProcAttr)
	}
	closeCtx, cancel := context.WithCancel(context.Background())

	if attr.Dir == "" {
		attr.Dir = parent.WorkingDirectory()
	}
	if len(attr.Env) == 0 {
		attr.Env = parent.Env()
	}

	openFiles, err := resolveOpenFiles(closeCtx, parent, pid, attr.Files)
	if err != nil {
		return nil, err
	}
	workerName := fmt.Sprintf("pid-%d", pid)
	port, err := jsworker.NewRemoteWasm(workerName, "/wasm/worker.wasm")
	if err != nil {
		return nil, err
	}

	remote := &Remote{
		pid:       pid,
		parentPID: parent.PID(),
		port:      port,
		closeCtx:  closeCtx,
	}

	err = port.Listen(closeCtx, func(me jsworker.MessageEvent, err error) {
		if err != nil {
			remote.closeErr = err
			cancel()
			return
		}
		if me.Data.Type() != js.TypeObject {
			return
		}
		jsExitCode := me.Data.Get("exitCode")
		if jsExitCode.Type() == js.TypeNumber {
			exitCode := jsExitCode.Int()
			remote.closeExitCode = &exitCode
			cancel()
			log.Debug("Remote exited with code:", exitCode)
		}
	})
	if err != nil {
		return nil, err
	}

	go func() {
		if err := awaitMessage(closeCtx, port, "pending_init"); err != nil {
			log.Error("Failed awaiting pending_init:", workerName, err)
			return
		}
		msg, transfers := makeInitMessage(parent.PID(), pid, workerName, command, argv, attr.Dir, attr.Env, openFiles)
		if err := port.PostMessage(msg, transfers); err != nil {
			log.Error("Failed sending init to worker:", workerName, err)
			return
		}
		if err := awaitMessage(closeCtx, remote.port, "ready"); err != nil {
			log.Error("Failed awaiting ready:", workerName, err)
			return
		}
		if err := remote.port.PostMessage(makeStartMessage(), nil); err != nil {
			log.Error("Failed sending start to worker:", workerName, err)
			return
		}
	}()

	return remote, nil
}

func (r *Remote) PID() common.PID {
	return r.pid
}

func (r *Remote) JSValue() js.Value {
	return js.ValueOf(map[string]interface{}{
		"pid":   r.pid.JSValue(),
		"ppid":  r.parentPID.JSValue(),
		"error": js.Null(),
	})
}

func awaitMessage(ctx context.Context, port *jsworker.Remote, messageStr string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	err := port.Listen(ctx, func(me jsworker.MessageEvent, err error) {
		if err != nil {
			result <- err
			return
		}
		if me.Data.Type() == js.TypeString && me.Data.String() == messageStr {
			result <- nil
		}
	})
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-result:
		return err
	}
}

func makeInitMessage(parentPID, pid common.PID, workerName, command string, argv []string, workingDirectory string, env map[string]string, openFiles []openFile) (msg js.Value, transfers []js.Value) {
	var openFileJSValues []interface{}
	var ports []js.Value
	for _, o := range openFiles {
		openFileJSValues = append(openFileJSValues, o)
		if o.pipe != nil {
			ports = append(ports, o.pipe.JSValue())
		}
	}
	return js.ValueOf(map[string]interface{}{
		"init": map[string]interface{}{
			"pid":              pid.JSValue(),
			"ppid":             parentPID.JSValue(),
			"workerName":       workerName,
			"command":          command,
			"argv":             interop.SliceFromStrings(argv),
			"workingDirectory": workingDirectory,
			"env":              interop.StringMap(env),
			"openFiles":        openFileJSValues,
		},
	}), ports
}

func makeStartMessage() js.Value {
	return js.ValueOf(map[string]interface{}{
		"start": true,
	})
}

func (r *Remote) Wait() (exitCode int, err error) {
	<-r.closeCtx.Done()
	if r.closeExitCode == nil {
		switch {
		case r.closeErr != nil:
			return 0, r.closeErr
		default:
			return 0, r.closeCtx.Err()
		}
	}
	return *r.closeExitCode, r.closeErr
}

func bindPortToFile(ctx context.Context, port *jsworker.MessagePort, file hackpadfs.File) error {
	err := port.Listen(ctx, func(me jsworker.MessageEvent, err error) {
		if err != nil {
			log.Error(err)
			return
		}
		bl, err := idbblob.New(me.Data)
		if err != nil {
			log.Error(err)
			return
		}
		if _, err := hackpadfs.WriteFile(file, bl.Bytes()); err != nil {
			log.Error(err)
		}
	})
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = file.Close()
	}()
	go func() {
		const maxReadSize = 1 << 10
		buf := make([]byte, maxReadSize)
		for {
			n, err := file.Read(buf)
			if err != nil {
				if err != interop.ErrNotImplemented {
					log.Error(err)
				}
				return
			}
			if n > 0 {
				bl := idbblob.FromBlob(blob.NewBytes(buf[:n])).JSValue()
				if err := port.PostMessage(bl, []js.Value{bl.Get("buffer")}); err != nil {
					log.Error(err)
					return
				}
			}
		}
	}()
	return nil
}

func resolveOpenFiles(ctx context.Context, parent process.Process, pid common.PID, attrs []fs.Attr) ([]openFile, error) {
	if len(attrs) == 0 {
		attrs = []fs.Attr{{FID: 0}, {FID: 1}, {FID: 2}}
	}
	openFiles := make([]openFile, 0, len(attrs))
	for _, attr := range attrs {
		switch {
		case attr.Ignore:
			openFiles = append(openFiles, openFile{filePath: "/dev/null"})
		case attr.Pipe:
			openFiles = append(openFiles, openFile{filePath: "/dev/null"})
		default:
			file, err := parent.Files().OpenRawFID(pid, attr.FID)
			if err != nil {
				return nil, err
			}
			info, err := file.Stat()
			if err != nil {
				return nil, err
			}
			openF := openFile{filePath: info.Name()}
			if info.Mode()&hackpadfs.ModeNamedPipe != 0 {
				port1, port2, err := jsworker.NewChannel()
				if err != nil {
					return nil, err
				}
				openF.pipe = port1
				if err := bindPortToFile(ctx, port2, file); err != nil {
					return nil, err
				}
			}
			openFiles = append(openFiles, openF)
		}
	}
	return openFiles, nil
}
