//go:build js
// +build js

package process

import (
	"os"
	"runtime"
	"syscall/js"

	"github.com/hack-pad/hackpad/internal/interop"
	"github.com/hack-pad/hackpad/internal/log"
)

var (
	jsObject = js.Global().Get("Object")
)

// exitCodeCrash is the sentinel exit code written to exitChan when a child
// WASM process crashes without invoking the exit callback.
const exitCodeCrash = -1

func (p *process) newWasmInstance(path string, importObject js.Value) (js.Value, error) {
	return p.Files().WasmInstance(path, importObject)
}

func (p *process) run(path string) {
	defer func() {
		go runtime.GC()
	}()

	exitChan := make(chan int, 1)
	err := p.startWasmProcess(path, exitChan)
	if err != nil {
		p.handleErr(err)
		return
	}
	p.exitCode = <-exitChan
	// handleErr must be called even when there is no error, because it invokes
	// p.Done() which cancels the process context and unblocks any callers of p.Wait().
	p.handleErr(nil)
}

func (p *process) startWasmProcess(path string, exitChan chan<- int) error {
	p.state = stateCompiling
	goInstance := jsGo.New()
	goInstance.Set("argv", interop.SliceFromStrings(p.args))
	if p.attr.Env == nil {
		p.attr.Env = splitEnvPairs(os.Environ())
	}
	goInstance.Set("env", interop.StringMap(p.attr.Env))
	var resumeFuncPtr *js.Func
	goInstance.Set("exit", interop.SingleUseFunc(func(this js.Value, args []js.Value) interface{} {
		defer func() {
			if resumeFuncPtr != nil {
				resumeFuncPtr.Release()
			}
			// TODO free the whole goInstance to fix garbage issues entirely. Freeing individual properties appears to work for now, but is ultimately a bad long-term solution because memory still accumulates.
			goInstance.Set("mem", js.Null())
			goInstance.Set("importObject", js.Null())
		}()
		if len(args) == 0 {
			exitChan <- exitCodeCrash
			return nil
		}
		code := args[0].Int()
		exitChan <- code
		if code != 0 {
			log.Warnf("Process exited with code %d: %s", code, p)
		}
		return nil
	}))
	importObject := goInstance.Get("importObject")

	instance, err := p.newWasmInstance(path, importObject)
	if err != nil {
		return err
	}

	exports := instance.Get("exports")

	// signalCrash writes a crash sentinel to exitChan so that run() is not
	// blocked indefinitely when the child WASM crashes without calling exit.
	signalCrash := func() {
		select {
		case exitChan <- exitCodeCrash:
		default:
		}
	}

	resumeFunc := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		defer func() {
			if r := recover(); r != nil {
				// Log the panic but do NOT re-panic. Re-panicking here causes
				// Go's handleEvent to recover and return normally to wasm_exec.js,
				// which then never settles the run() promise. That in turn makes
				// run() block forever on runPromise.Await(), leading to a deadlock.
				interop.HandlePanic(r)
				signalCrash()
			}
		}()
		prev := switchContext(p.pid)
		ret := exports.Call("resume", interop.SliceFromJSValues(args)...)
		switchContext(prev)
		return ret
	})
	resumeFuncPtr = &resumeFunc
	wrapperExports := map[string]interface{}{
		"run": interop.SingleUseFunc(func(this js.Value, args []js.Value) interface{} {
			defer func() {
				if r := recover(); r != nil {
					interop.HandlePanic(r)
					signalCrash()
				}
			}()
			prev := switchContext(p.pid)
			ret := exports.Call("run", interop.SliceFromJSValues(args)...)
			switchContext(prev)
			return ret
		}),
		"resume": resumeFunc,
	}
	for export, value := range interop.Entries(exports) {
		_, overridden := wrapperExports[export]
		if !overridden {
			wrapperExports[export] = value
		}
	}
	wrapperInstance := jsObject.Call("defineProperty",
		jsObject.Call("create", instance),
		"exports", map[string]interface{}{ // Instance.exports is read-only, so create a shim
			"value":    wrapperExports,
			"writable": false,
		},
	)

	p.state = stateRunning
	goInstance.Call("run", wrapperInstance)
	return nil
}
