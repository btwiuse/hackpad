# Hackpad — Project Specification

> A complete Go development environment that runs entirely in the browser via WebAssembly (Wasm). Users can write, build, format, and run Go programs without installing anything locally.

Live site: https://hackpad.org  
Reference article: https://johnstarich.medium.com/how-to-compile-code-in-the-browser-with-webassembly-b59ffd452c2b

---

## 1. High-Level Concept

Hackpad ships a patched Go toolchain compiled to WebAssembly, along with a virtual POSIX-like OS layer (filesystem + process model) implemented in Go and exposed to the browser. The browser hosts:

1. **`main.wasm`** — the OS kernel: virtual FS, process manager, install helper, terminal spawner.
2. **`editor.wasm`** — an IDE Wasm binary: tab-paned editor + terminal consoles, invokes the Go toolchain.
3. **`sh.wasm`** — a shell Wasm binary (powered by [hush](https://github.com/hack-pad/hush)).
4. **`go.tar.gz`** — the patched Go toolchain (stdlib + `go`, `gofmt`, `go build`, etc.) as a gzip'd tarball.
5. A **React SPA** that bootstraps the Wasm runtime, mounts filesystems, and renders the UI.

Everything runs client-side; no server-side code execution is needed after the static files are served.

---

## 2. Repository Layout

```
hackpad/
├── main.go                   # Entry point for main.wasm (js build tag only)
├── install.go                # hackpad.install() — fetches *.wasm into /bin
├── http_get.go               # Fetch wrapper (js/wasm)
├── go.mod                    # Go module: github.com/hack-pad/hackpad (Go 1.20)
├── Makefile                  # Build orchestration (see §7)
├── Dockerfile                # Multi-stage Docker build (see §8)
│
├── cmd/
│   ├── editor/               # editor.wasm binary
│   │   ├── main.go           # CLI entry: -editor=<elementID>
│   │   ├── editor.go         # jsEditor: bridges CodeMirror ↔ FS
│   │   ├── css/              # Runtime CSS injection helper
│   │   ├── dom/              # Thin Go wrappers around browser DOM APIs
│   │   ├── ide/              # IDE window, tab panes, editor/console interfaces
│   │   ├── plaineditor/      # Fallback plain-text editor (no CodeMirror)
│   │   ├── taskconsole/      # Task console (shows build/run output)
│   │   └── terminal/         # xterm.js terminal console wrapper
│   └── sh/
│       └── main.go           # sh.wasm binary — runs hush shell
│
├── internal/
│   ├── fs/                   # Virtual filesystem core
│   │   ├── fs.go             # Root mount-FS, OverlayTarGzip, OverlayIndexedDB
│   │   ├── fs_js.go          # IndexedDB persistence backend (js only)
│   │   ├── file_descriptors*.go  # FD table per process
│   │   ├── pipe.go / stdout.go / null_file.go / download.go / …
│   │   └── wasm_cache.go     # Wasm binary caching
│   ├── process/              # Process model
│   │   ├── process.go        # Process struct, PID table, start/wait/done
│   │   ├── process_js.go     # JSValue() — expose process to JS
│   │   ├── wasm.go           # Wasm instantiation and execution
│   │   ├── context.go        # Current process context
│   │   └── lookpath.go       # PATH resolution
│   ├── terminal/
│   │   └── term.go           # SpawnTerminal: bridge xterm.js ↔ process stdin/stdout
│   ├── js/
│   │   ├── fs/               # JS-side syscall shim (overrides window.fs.*)
│   │   │   ├── fs.go         # Init() — register all fs syscalls + overlay APIs
│   │   │   └── overlay.go    # overlayIndexedDB / overlayTarGzip JS functions
│   │   └── process/          # JS-side process shim
│   │       ├── process.go    # Init() — register child_process.spawn/wait/…
│   │       └── spawn.go      # Spawn() — create & start process, return JS value
│   ├── interop/              # JS ↔ Go interop utilities
│   │   ├── load.go           # SetInitialized() — sets window.hackpad.ready
│   │   └── error.go          # WrapAsJSError
│   ├── promise/              # Go Promise primitives for syscall/js
│   ├── global/               # window.hackpad namespace helpers
│   ├── console/              # JS console.log / writer element
│   ├── log/                  # Internal structured logging
│   └── cmd/gozip/            # CLI tool: pack Go installation into go.tar.gz
│
├── server/                   # React frontend (Create React App)
│   ├── public/
│   │   └── index.html        # Loads wasm_exec.js; mounts <div id="root">
│   └── src/
│       ├── index.js          # ReactDOM.render(<App />)
│       ├── App.js            # Bootstraps Hackpad, installs binaries, renders IDE
│       ├── Hackpad.js        # init(), install(), run(), spawn(), spawnTerminal()
│       ├── WebAssembly.js    # WebAssembly.instantiateStreaming polyfill
│       ├── Terminal.js       # xterm.js React component + newTerminal() factory
│       ├── Editor.js         # CodeMirror editor factory (newEditor())
│       ├── ColorScheme.js    # prefers-color-scheme listener
│       ├── Loading.js        # Loading spinner with download %
│       └── Compat.js         # Browser compatibility warning
│
└── patches/                  # Git patches for the forked Go toolchain
    ├── 0001-*.patch          # Core wasm runtime patches
    ├── 0002-*.patch          # fs syscall sane defaults
    └── 0003-*.patch          # Large argv support
```

---

## 3. Architecture

### 3.1 Boot Sequence

```
Browser loads index.html
  └─ loads wasm_exec.js (Go runtime glue, from patched Go toolchain)
  └─ React <App /> mounts

App.useEffect():
  1. WebAssembly.instantiateStreaming("wasm/main.wasm") → go.run(instance)
     • main.wasm registers on window:
         hackpad.{install, spawnTerminal, overlayIndexedDB, overlayTarGzip, …}
         fs.{open, read, write, stat, mkdir, …}            ← Node-style fs API
         child_process.{spawn, wait}
         hackpad.ready = true

  2. fs.mkdir("/bin"); hackpad.overlayIndexedDB("/bin", {cache:true})
     fs.mkdir("/home/me"); hackpad.overlayIndexedDB("/home/me")
     hackpad.overlayTarGzip("/usr/local/go", "wasm/go.tar.gz", {persist:true, …})
       → downloads go.tar.gz, extracts into IndexedDB-backed FS
       → subsequent visits skip re-download (marker file .tarfs-complete)

  3. hackpad.install("editor")  → fetches wasm/editor.wasm → /bin/editor
     hackpad.install("sh")      → fetches wasm/sh.wasm     → /bin/sh

  4. child_process.spawn("editor", ["--editor=editor"])
     • editor.wasm runs as a process (PID 2)
     • editor initialises IDE window on <div id="editor">
     • runs: go version, go mod init playground, go mod tidy
     • opens main.go with a default Hello World program

  setLoading(false) → spinner disappears, IDE is usable
```

### 3.2 Virtual OS

The Go Wasm runtime (via `wasm_exec.js`) expects Node.js-style `window.fs` and `window.process` objects. Hackpad overrides these with its own implementations:

| Browser API | Implementation |
|---|---|
| `window.fs.*` | `internal/js/fs` — delegates to the in-memory/IndexedDB virtual FS |
| `window.child_process.*` | `internal/js/process` — spawns new Wasm instances |
| `window.hackpad.*` | `internal/global` — overlay helpers, install, spawnTerminal |

### 3.3 Virtual Filesystem

The FS is a **mount-point overlay stack** (using [hackpadfs](https://github.com/hack-pad/hackpadfs)):

| Mount | Backend | Notes |
|---|---|---|
| `/` | `mem.FS` (in-memory) | Default root, ephemeral |
| `/bin` | `indexeddb.FS` (relaxed durability) | Installed Wasm binaries, cached across sessions |
| `/home/me` | `indexeddb.FS` | User files, persisted in IndexedDB |
| `/home/me/.cache` | `indexeddb.FS` (relaxed) | Go module cache |
| `/usr/local/go` | `tar.ReaderFS` → `indexeddb.FS` + `cache.ReadOnlyFS` | Go toolchain; streamed from go.tar.gz, persisted after first load |

Each `AddMount` call stacks a new FS at a path, shadowing the one below. File descriptors are per-process tables (`fs.FileDescriptors`).

### 3.4 Process Model

Each spawned command is a **new Wasm instance** running in a goroutine:

```
process.New(command, args, attr)
  → reads binary from FS at command path
  → verifies Wasm magic bytes (\x00asm)
  → go.run(wasmInstance)   [patched Go toolchain]
  → returns PID, can Wait() via context cancellation
```

Process hierarchy:
- PID 1 — `main.wasm` (the kernel process; runs forever via `select{}`)
- PID 2+ — child processes (editor, sh, go build, etc.)

Each process has its own:
- File descriptor table (stdin/stdout/stderr + any pipes)
- Working directory
- Environment variables (inherited + overrides)

Pipes are implemented as goroutine-synchronized channels backed by `idbblob`.

### 3.5 Terminal Integration

`terminal.SpawnTerminal(term, options)` bridges an xterm.js terminal object to a process:

```
stdin  ← term.onData(blob)  → writes to stdinW pipe
stdout → read from stdoutR  → term.write(buf)
stderr → read from stderrR  → term.write(buf)
```

### 3.6 IDE (editor.wasm)

The IDE is entirely Go code targeting `GOOS=js`:

- **`ide.Window`** — top-level DOM manager; creates two `TabPane`s (editors + consoles) and a toolbar
- **`ide.TabPane`** — generic tab container with add/close/focus
- **`ide.Editor`** interface — `OpenFile`, `ReloadFile`, `GetCursor`, `SetCursor`, `Titles`
  - Implemented by `jsEditor` (bridges CodeMirror from JS) or `plaineditor` (fallback)
- **`ide.Console`** interface — terminal tab backed by xterm.js
- **`ide.TaskConsole`** — special console tab for build/run output; `Start(name, args)` spawns a process and pipes its output
- Toolbar buttons: **Build** (`go build -v .`), **Run** (build + execute), **Format** (`go/format.Source`)

---

## 4. Go Toolchain Fork

Hackpad requires a patched Go toolchain (hosted at `github.com/btwiuse/go`, branch `hackpad/release-branch.go<VERSION>`). The patches:

1. **`0001`** — Core wasm runtime patches (executor, memory management, glue code)
2. **`0002`** — Sane defaults for `fs` syscalls in wasm (prevent panics on unimplemented calls)
3. **`0003`** — Remove argv size limit in wasm (required for long command lines)

The patched toolchain is built for the **host** platform but targeting `GOOS=js GOARCH=wasm`, then packaged into `go.tar.gz` by `internal/cmd/gozip`. It includes:

- `bin/js_wasm/go` — the `go` CLI (build, test, run, mod, etc.)
- `bin/js_wasm/gofmt`
- `pkg/tool/js_wasm/` — buildid, pack, cover, vet
- All of the Go standard library

---

## 5. Key Dependencies

| Package | Purpose |
|---|---|
| `github.com/hack-pad/hackpadfs` | Virtual FS abstractions (mem, mount, indexeddb, tar, cache) |
| `github.com/hack-pad/go-indexeddb` | Go bindings for the browser IndexedDB API |
| `github.com/hack-pad/hush` | POSIX-compatible shell (sh.wasm) |
| `github.com/hack-pad/safejs` | Safe wrappers around `syscall/js` |
| `github.com/avct/uasurfer` | User-agent parser (browser compat check) |
| `github.com/johnstarich/go/datasize` | Human-readable byte sizes |
| `github.com/machinebox/progress` | Progress reporting for streaming downloads |
| `go.uber.org/atomic` | Atomic types (PID counter, loading flag) |
| `github.com/pkg/errors` | Error wrapping |
| `mvdan.cc/sh/v3` | Shell execution (used by hush) |
| **React 16** | SPA framework |
| **CodeMirror 5** | Code editor with Go syntax highlighting |
| **xterm.js 4** | Terminal emulator |
| **xterm-addon-fit** | Auto-resize terminal to container |

---

## 6. JavaScript ↔ Go Interop Contract

After `main.wasm` initialises, the following globals are available on `window`:

```js
// Lifecycle
hackpad.ready              // boolean: true once init is complete

// Binary installation
hackpad.install(name)      // Promise — fetches wasm/<name>.wasm → /bin/<name>

// Process management
child_process.spawn(cmd, args, options)  // returns {pid, ppid, error}
child_process.wait(pid, callback)        // callback(err, process)

// Terminal
hackpad.spawnTerminal(term, options)     // options: {args, cwd}

// Filesystem overlays
hackpad.overlayIndexedDB(path, opts)     // Promise — mount IndexedDB FS
hackpad.overlayTarGzip(path, url, opts)  // Promise — mount tar.gz FS
hackpad.destroyMount(path)               // Promise — unmount + clear

// Debug
hackpad.dump(path)         // log FS stats
hackpad.profile()          // CPU profile
hackpad.getMounts()        // list mount points

// Node-style fs (overrides window.fs from wasm_exec.js)
fs.open / fs.read / fs.write / fs.stat / fs.mkdir / fs.readdir / ...
fs.pipe / fs.fstat / fs.flock / fs.rename / fs.unlink / ...

// Editor factory (set by React before editor.wasm runs)
window.editor.newEditor(elem, onEdit)    // → CodeMirror instance wrapper
window.editor.newTerminal(elem)          // → xterm.js instance
```

The `editor.wasm` process reads `window.editor.newEditor` and `window.editor.newTerminal` on startup.

---

## 7. Build System

### Prerequisites

- Go (host, for bootstrapping)
- Node.js 14+, npm
- Docker (for containerised builds)
- Internet access (to clone the patched Go toolchain)

### Key Make Targets

| Target | Description |
|---|---|
| `make go` | Clone + build the patched Go toolchain into `cache/go<VERSION>/` |
| `make commands` | Compile all `cmd/*` to `server/public/wasm/*.wasm` and copy `wasm_exec.js` |
| `make go-static` | `go.tar.gz` + all wasm commands |
| `make node-static` | `npm ci && npm run build` in `server/` |
| `make watch` | Dev server: hot-reload React + rebuild Go on change |
| `make serve` | `go run ./server` (static file server for dev) |
| `make build` | Full Docker build, copy output to `./out/` |
| `make build-docker` | Build Docker image `hackpad:latest` |
| `make run-docker` | Run Docker container on port 8080 |
| `make test-native` | Run Go tests with race detector (native arch) |
| `make lint` | Run `golangci-lint` |

### Build Environment Variables

```makefile
GOOS   = js
GOARCH = wasm
PATH   = ./cache/go/bin:./cache/go/misc/wasm:$PATH
```

All Go compilation (wasm binaries) uses the patched toolchain from `cache/go/`.

---

## 8. Deployment

### Docker (Production)

Three-stage Dockerfile:

```
Stage 1 (go-builder):   golang:1.26
  - make go             → cache/go<VERSION>/
  - make go-static      → server/public/wasm/{*.wasm, go.tar.gz, wasm_exec.js}

Stage 2 (node-builder): node:14
  - make node-static    → server/build/ (React production bundle)

Stage 3 (nginx:1):
  - Adds application/wasm MIME type
  - Serves server/build/ as static files
```

The resulting image serves everything from nginx; no dynamic server is needed.

### Development

```bash
make watch          # starts React dev server + Go watcher (nodemon)
                    # React on :3000, Go files rebuilt automatically
```

---

## 9. Runtime Environment Inside the Browser VM

After boot, the in-browser environment looks like:

```
/
├── bin/
│   ├── editor          (editor.wasm)
│   └── sh              (sh.wasm)
├── usr/local/go/       (Go toolchain, from go.tar.gz via IndexedDB)
│   ├── bin/js_wasm/go
│   ├── bin/js_wasm/gofmt
│   └── pkg/tool/js_wasm/
├── home/me/
│   ├── go/bin/         (user-installed Go binaries)
│   ├── .cache/go-mod/  (Go module cache, IndexedDB-backed)
│   └── playground/     (working directory)
│       ├── go.mod
│       └── main.go
└── tmp/
```

Environment variables passed to child processes:

```
GOMODCACHE = /home/me/.cache/go-mod
GOPROXY    = https://proxy.golang.org/
GOROOT     = /usr/local/go
HOME       = /home/me
PATH       = /bin:/home/me/go/bin:/usr/local/go/bin/js_wasm:/usr/local/go/pkg/tool/js_wasm
```

---

## 10. Known Limitations & Issues

- **Slow compile times** — each `go build` instantiates a new Wasm binary; parallelisation via Web Workers is a planned improvement.
- **Safari crashes** — due to WebKit Wasm memory bugs ([#222097](https://bugs.webkit.org/show_bug.cgi?id=222097), [#227421](https://bugs.webkit.org/show_bug.cgi?id=227421), [#220313](https://bugs.webkit.org/show_bug.cgi?id=220313)). Only Chrome and Firefox (desktop) are fully supported.
- **Single-threaded** — the browser's single JS thread is shared by all goroutines; `runtime.GOMAXPROCS` is effectively 1.
- **No networking from Go** — Go programs cannot open TCP sockets; only HTTP via `fetch` is possible.
- **Memory** — large programs may exhaust browser Wasm memory limits.

---

## 11. Reproducing / Reimplementing

To recreate a system equivalent to Hackpad, you need:

1. **A patched Go toolchain** compiled to `GOOS=js GOARCH=wasm` that:
   - Provides sane defaults for unimplemented fs syscalls (no panic)
   - Allows large argv
   - Exposes `window.fs` and `window.child_process` hooks that can be overridden

2. **A virtual filesystem layer** that:
   - Implements the Node.js-compatible `fs` API expected by `wasm_exec.js`
   - Supports mount-point overlays
   - Persists to IndexedDB for files that should survive page reloads
   - Can stream-extract `.tar.gz` archives into a persistent FS

3. **A process manager** that:
   - Assigns PIDs, tracks parent/child relationships
   - Spawns new Wasm instances per process (each `go build`, `sh`, etc.)
   - Provides pipes (readable/writable FD pairs) for IPC
   - Propagates environment variables and working directory

4. **Terminal integration**:
   - Bridge xterm.js (or equivalent) input/output to process stdin/stdout pipes

5. **An IDE frontend**:
   - Code editor with Go syntax (CodeMirror or Monaco)
   - Tab panes for editors and terminals
   - Toolbar: build, run, format
   - Loading screen with download progress

6. **A static web server** (or CDN):
   - Serve with correct MIME type `application/wasm` for `.wasm` files
   - No server-side code execution required
