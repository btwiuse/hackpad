//go:build js

package jsfunc

import "syscall/js"

type Func = func(this js.Value, args []js.Value) interface{}

func NonBlocking(fn Func) js.Func {
	return js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		go fn(this, args)
		return nil
	})
}
