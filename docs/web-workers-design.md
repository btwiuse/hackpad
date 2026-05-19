# Web workers runtime design

## Goals

- Move process execution off the browser main thread.
- Allow compile, link, shell, and editor subprocesses to run in parallel.
- Keep a single DOM-facing runtime responsible for UI work.
- Reuse the shared filesystem and installed toolchain across worker-backed processes.
- Preserve the current frontend, Go toolchain, and CI upgrades already present on the mainline branch.

## High-level architecture

Hackpad now has two runtime roles:

1. **DOM runtime (`main.wasm`)**
   - Runs on the browser main thread.
   - Owns DOM access, editor bootstrapping, terminal spawning, and initial filesystem setup.
   - Starts the editor process as a managed local process instead of spawning it from JavaScript.

2. **Worker runtime (`worker.wasm`)**
   - Runs inside a dedicated browser `Worker`.
   - Hosts one process tree rooted at the process assigned during worker initialization.
   - Can spawn additional worker-backed child processes for parallel work.

The JavaScript `wasmWorker.js` bootstrap script is only responsible for loading `wasm_exec.js`, instantiating `worker.wasm`, and letting the Go runtime take over.

## Process model

### Local worker

Each Go runtime owns a `worker.Local` instance that:

- waits for an initialization message,
- reconstructs the process state,
- initializes the JS `process` and `child_process` shims for that runtime,
- exposes spawn/wait behavior through the runtime-specific process shim,
- starts the assigned process only after an explicit `start` message.

### Remote worker

When a process needs a true parallel child, `worker.Remote`:

- allocates a PID through the kernel PID allocator,
- snapshots the command, argv, cwd, env, and inherited file handles,
- starts a browser worker pointing at `wasmWorker.js?wasm=/wasm/worker.wasm`,
- sends the reconstructed process state over `MessagePort`,
- waits for `ready`,
- sends `start`,
- waits for the worker to publish an exit code.

### Process shims

The JS-facing `process` and `child_process` shims must be runtime-specific. The updated `internal/js/process` package now supports:

- the original single-runtime initialization path used by `init.wasm`,
- a runtime-specific initialization path used by worker-backed runtimes,
- pluggable spawn/wait implementations so worker runtimes can route process lifecycle through `worker.Local`.

## Messaging design

`internal/jsworker` is the transport layer for worker communication.

It provides:

- `MessagePort` wrapping for posting messages and registering listeners,
- local runtime access through `GetLocal`,
- remote worker startup through `NewRemoteWasm`,
- structured message event parsing,
- channel creation for transferring pipe-backed I/O streams.

Initialization messages carry:

- worker name,
- command,
- argv,
- working directory,
- environment,
- inherited open files.

Lifecycle messages carry:

- `pending_init`,
- `ready`,
- `start`,
- `exitCode`.

## Filesystem design

### Shared persistent mounts

The DOM runtime is responsible for creating and mounting:

- `/bin`
- `/home/me`
- `/home/me/.cache`
- `/tmp`
- `/usr/local/go`

Persistence strategy:

- IndexedDB-backed overlays are used for writable mounts.
- The Go toolchain archive is still unpacked through the existing tar+gzip overlay path.
- Worker runtimes remount the same IndexedDB-backed locations so they can observe the shared filesystem state.

### Cross-worker file descriptors

To support process stdio and inherited descriptors across workers:

- `common.OpenFileAttr` describes file descriptors that can be serialized across runtimes.
- `fs.NewFileDescriptorsFromOpenFiles` reconstructs a descriptor table from transferred file metadata.
- `fs.OpenRawFID` increments open counts and exposes the underlying file object for transfer/binding.
- `fs.deviceFile` wraps `io.ReadWriteCloser` instances received from worker pipes.
- The current port still resets inherited regular-file seek offsets to `0` during worker transfer, so non-stdio inherited file positions remain a known limitation that should be fixed in a follow-up.

### Pipe transport

Named pipes are bridged through `MessagePort` pairs:

- the parent side binds a file handle to a transferred message port,
- the child side reconstructs that port as an `io.ReadWriteCloser`,
- pipe reads now block for the first byte and then drain any immediately available bytes, which avoids stalling worker pipe relays waiting for a full buffer.

### File locking

In-memory per-runtime locks are not sufficient once multiple workers can touch the same filesystem.

The updated design uses:

- IndexedDB-backed file lock records on JS/Wasm builds,
- a lightweight in-memory fallback for non-JS builds and native tests.

This keeps `flock` behavior coordinated across workers without regressing native validation.

## Frontend boot flow

The frontend now uses an explicit `boot()` call instead of eagerly starting the Wasm runtime during module evaluation.

Boot sequence:

1. React registers `window.editor` builders.
2. `boot()` instantiates `main.wasm`.
3. `main.wasm` creates the DOM runtime process, mounts filesystems, installs binaries, and starts the editor process.
4. The frontend waits for the worker-ready signal before dismissing the loading UI.

This ordering avoids racing the editor startup against `window.editor` registration.

## Files touched by this port

### New runtime pieces

- `cmd/worker/main.go`
- `internal/jsworker/*`
- `internal/kernel/kernel.go`
- `internal/worker/*`
- `server/public/wasmWorker.js`

### Core integration changes

- `main.go`
- `internal/install/install.go`
- `internal/process/*`
- `internal/js/process/*`
- `internal/fs/file_descriptors.go`
- `internal/fs/pipe.go`
- `internal/fs/device_file.go`
- `internal/common/fid.go`
- `server/src/Hackpad.js`
- `server/src/App.js`

## Constraints preserved from the current branch

- The current Go 1.26 toolchain and wasm patches remain the source of truth.
- The current frontend dependency versions remain the source of truth.
- The React 19 `createRoot` bootstrap remains intact.
- The existing `init.wasm` entrypoint remains available.
- Native test validation continues to run through `make test`.

## Follow-up changes

### Code organization

- Rename and regroup overlapping concepts such as worker, process, port, and filesystem helpers.
- Separate DOM-process concerns from generic worker runtime concerns.
- Consolidate duplicated filesystem mount setup between `main.wasm` and `worker.wasm`.

### Reliability

- Add integration coverage for worker-backed spawn/wait/stdio flows.
- Add browser-level validation for compile workloads and terminal I/O.
- Add stronger startup handshakes so the frontend can distinguish worker initialization from full editor readiness.

### Performance

- Reuse a shared worker pool instead of always creating a brand-new browser worker per process.
- Move toward the longer-term design where a single kernel/shared worker coordinates multiple browser tabs and process scheduling.

### Developer ergonomics

- Add runtime diagnostics for worker lifecycle transitions.
- Document the worker protocol and transferred descriptor formats for future refactors.
- Consider a dedicated package for boot-time filesystem layout and environment defaults.
