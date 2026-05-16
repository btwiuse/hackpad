# Design: Terminal ioctl Support for js/wasm

## Problem

Programs using `golang.org/x/term` (e.g., `term.IsTerminal(fd)`, `term.GetSize(fd)`)
fail silently in Hackpad's js/wasm environment:

```go
fd := int(os.Stdout.Fd())
if !term.IsTerminal(fd) {
    fmt.Println("not a terminal")  // always reached on js/wasm
}
w, h, err := term.GetSize(fd)
// err: "terminal: GetSize not implemented on js/wasm"
```

Three independent layers block this:

| Layer | Issue |
|---|---|
| `golang.org/x/term` (module) | `term_unix.go:5` build tag excludes `js` → falls to `term_unsupported.go` (always returns `false` / errors) |
| `golang.org/x/sys/unix` (module) | Same build tag gap — `ioctl_unsigned.go:5` excludes `js`, so `IoctlGetTermios`/`IoctlGetWinsize` don't compile |
| `syscall_js.go` (Go stdlib) | `Syscall()` returns `ENOSYS` for all traps; no `SYS_IOCTL` constant, no dispatch |

## What was added

### 1. Go toolchain patch — `patches/0005-hackpad-add-terminal-ioctl-support.patch`

Adds `SYS_IOCTL` dispatch to the js/wasm runtime:

- `SYS_IOCTL = 54` (Darwin-compatible value)
- `TIOCGETA = 0x40487413`, `TIOCGWINSZ = 0x40087468`
- `Winsize` and `Termios` structs matching Darwin ABI
- `ioctl(fd, req, arg)` handler that calls `hackpad.ioctl(fd, req)` in JS
- `Syscall()` and `Syscall6()` route `SYS_IOCTL` to the handler

Apply this patch to the forked Go toolchain (after patches 0001–0004).

### 2. JS-side ioctl registry — `internal/term/ioctl.go`

Maintains a mapping of file descriptors to terminal dimensions:

- `RegisterTerminal(fid, info)` — called when a terminal spawns
- `UnregisterTerminal(fid)` — called when the terminal process exits
- `Ioctl(fd, req)` — exposed as `hackpad.ioctl(fd, req)`, returns `{rows, cols, baudRate}` or `null`

### 3. Terminal registration — `internal/terminal/term.go`

On terminal spawn:
- Reads `term.rows` / `term.cols` from the xterm.js instance
- Registers stdin, stdout, stderr fds with those dimensions
- Listens for `onResize` and updates dimensions live
- Cleans up registrations on process exit (`proc.Wait()`)

## What still needs to be done

The Go toolchain patch **alone is not sufficient**. Two upstream modules also need
build-tag changes:

| Module | File | Current tag | Needed |
|---|---|---|---|
| `golang.org/x/sys` | `unix/ioctl_unsigned.go:5` | `darwin \|\| freebsd \|\| ...` | Add `\|\| js` |
| `golang.org/x/term` | `term_unix.go:5` | `aix \|\| darwin \|\| ...` | Add `\|\| js` |

These cannot be patched from within Hackpad's Go toolchain fork — they're
external modules resolved at `go build` time via `GOPROXY`.

### Options to close the gap

1. **Fork + replace** — Fork `golang.org/x/term` and `golang.org/x/sys`, add
   `js` to build tags, then use `replace` directives in the user's `go.mod`.
   
2. **Upstream PRs** — Submit patches to `golang.org/x/term` and `golang.org/x/sys`
   to add `js` build tags. These would land for all WASM Go users.

3. **Hackpad-local package** — Use `internal/term` directly instead of
   `golang.org/x/term`. This works today but requires program changes:

   ```go
   import "github.com/hack-pad/hackpad/internal/term"
   
   fd := int(os.Stdout.Fd())
   if !term.IsTerminal(fd) { ... }
   w, h, err := term.GetSize(fd)
   ```

## Correct usage (once fully wired)

After applying the toolchain patch AND the module build-tag fixes:

```go
package main

import (
    "fmt"
    "os"
    "golang.org/x/term"
)

func main() {
    fd := int(os.Stdout.Fd())

    if !term.IsTerminal(fd) {
        fmt.Println("not a terminal")
        return
    }

    w, h, err := term.GetSize(fd)
    if err != nil {
        fmt.Println("get size:", err)
        return
    }

    fmt.Printf("%dx%d\n", w, h)
    // When run in an xterm.js terminal: "80x24"
}
```

## Files changed

```
patches/0005-hackpad-add-terminal-ioctl-support.patch  (new)  Go toolchain patch
internal/term/ioctl.go                                 (new)  fd→terminal dimension registry + JS ioctl handler
internal/terminal/term.go                              (mod)  Register/unregister terminal fds on spawn/exit
main.go                                                (mod)  Expose hackpad.ioctl to JS
docs/terminal-ioctl-design.md                          (new)  This document
```
