# Web Worker Process Architecture

## Background

Hackpad currently runs the main editor process and most subprocess work inside a single browser thread. That keeps the implementation simple, but it makes CPU-heavy work like `go build`, `compile`, and `link` contend with the editor, terminal, and React UI on the same event loop.

The `feature/web-workers-2` branch explored moving non-DOM subprocesses into dedicated browser workers. This document describes the ported design on the current branch, the reasoning behind the current shape of the port, and the follow-up changes still needed to make worker-backed process execution robust.

## Goals

1. Move non-DOM subprocess execution off the main thread.
2. Keep DOM-bound programs like the editor on the main thread.
3. Preserve the existing Hackpad filesystem and process abstractions.
4. Reuse persistent mounts so workers can see `/bin`, `/home/me`, `/tmp`, and `/usr/local/go`.
5. Leave room for a future single-kernel or `SharedWorker` model.

## Current Port

The current branch now includes the core worker transport and bootstrapping pieces:

- `cmd/worker/main.go` adds a dedicated worker wasm binary.
- `server/public/wasmWorker.js` boots a Go wasm binary inside a browser `Worker`.
- `internal/jsworker/` provides the low-level `Worker`, `self`, `MessagePort`, and `MessageChannel` wrappers used by Hackpad.
- `internal/worker/` adds the higher-level worker lifecycle code:
  - DOM-side routing for whether a process stays local or runs in a worker.
  - Worker-side initialization and shutdown.
  - `MessagePort` bridging for named pipes and other raw devices.
- `internal/js/process/` now supports configurable spawn/wait backends so the browser-side shim can route process creation either to the existing local runtime or to worker-backed execution.
- `internal/process/` and `internal/fs/` now support bootstrapping an isolated process from explicit open-file metadata, which is required when a browser worker starts with transferred file handles instead of inherited in-memory descriptors.

## High-Level Architecture

### Main thread

The main thread still loads `wasm/main.wasm` and owns all DOM-capable programs. It now installs a DOM-side worker manager that decides where a child process should run:

- `editor` remains on the main thread because it depends on DOM APIs.
- non-DOM commands are eligible to run in a dedicated worker.

### Dedicated worker

Each dedicated worker runs `wasm/worker.wasm` via `server/public/wasmWorker.js`.

The worker boot flow is:

1. Browser creates a `Worker`.
2. Worker loads `wasm_exec.js` and `worker.wasm`.
3. The Go worker runtime sends `pending_init`.
4. The parent sends an `init` payload containing:
   - pid / ppid
   - command / argv
   - working directory
   - environment
   - serialized open files
5. The worker mounts persistent storage and recreates its process state.
6. The worker sends `ready`.
7. The parent sends `start`.
8. The worker runs the subprocess and later posts `exitCode`.

### File descriptor transfer

Browser workers cannot inherit Go heap objects directly, so file descriptors must be reconstructed.

The current port uses two strategies:

- regular files are reopened by path inside the worker
- named pipes are bridged through `MessagePort` transfers

This matches the intent of the original branch: keep persistent filesystem state in IndexedDB-backed mounts, and use message channels only for descriptors that cannot be reopened from storage.

### Process model

The existing process abstraction is still used. The port adds:

- an isolated-process bootstrap path for worker-started processes
- explicit environment copying
- current-process reassignment so the JS shims in a worker point at the worker-owned process
- a configurable spawn/wait driver so the browser-side `child_process` shim can route to either local or remote execution

## Why `github.com/hack-pad/go-webworkers` Was Not Used

The existing `github.com/hack-pad/go-webworkers` package is close to the low-level dedicated-worker pieces, but it does not currently expose everything this port needs:

- exported `MessagePort` wrapping
- `MessageChannel` construction
- a direct way to transfer arbitrary `MessagePort` objects between contexts

Because the Hackpad worker port relies on `MessagePort` transfer for pipe bridging, the current port keeps the internal `internal/jsworker` implementation. If `go-webworkers` is extended to cover these primitives, Hackpad should switch to it and delete the local wrappers.

## Design Constraints

### DOM-only commands

Some commands cannot safely move into workers because they need browser DOM APIs. Today that is explicitly true for `editor`.

The long-term design should classify commands more intentionally:

- **DOM commands**: editor UI, anything that touches `window`, `document`, or browser widgets
- **worker-safe commands**: CLI tools, compilers, shells, pure filesystem / process tools
- **hybrid commands**: commands that mostly run in a worker but occasionally need DOM mediation

### Persistent storage

Workers need the same mounted data that the main thread sees. The current port recreates the IndexedDB-backed overlays inside the worker so it can reopen:

- installed binaries in `/bin`
- user files in `/home/me`
- caches in `/home/me/.cache`
- temp files in `/tmp`
- persisted Go toolchain data in `/usr/local/go`

This keeps the current filesystem architecture working, but it means every worker is responsible for local mount setup.

## Known Gaps and Follow-Up Work

### 1. PID allocation is still provisional

The original branch did not fully solve cross-worker pid allocation, and the current port still uses a lightweight parent-derived allocation strategy. That is good enough for an initial port, but not for a long-lived multi-worker kernel.

Recommended follow-up:

- move pid allocation to a single authority on the main thread
- or move to a shared kernel process that owns pid reservation

### 2. Spawn routing is still command-name based

The current port keeps `editor` on the main thread by name. That is practical, but it is not the final design.

Recommended follow-up:

- define command capabilities explicitly
- let installers or manifests declare whether a program is DOM-only or worker-safe
- avoid hard-coded command routing in Go code

### 3. Terminal execution still needs full migration

The worker port now covers the browser-side process shim used by wasm subprocesses. The xterm path still has direct process-launch behavior that should eventually share the same spawn router so shells and terminal commands benefit from the same worker isolation.

Recommended follow-up:

- route terminal process startup through the same spawn driver
- unify pipe behavior between xterm and worker-backed subprocesses

### 4. Pipe and stdio behavior needs deeper coverage

The named-pipe bridge is in place, but stdio semantics still need broader validation across:

- shell pipelines
- `go test`
- `go build`
- nested `exec.Command` trees
- long-running stdout / stderr streams

Recommended follow-up:

- add focused wasm integration tests for pipe-heavy commands
- exercise nested subprocess trees
- verify descriptor close semantics on worker shutdown

### 5. Mount setup is duplicated

Both the main runtime and worker runtime know how to stand up the persistent mounts. That duplication makes drift likely.

Recommended follow-up:

- extract a shared mount/bootstrap helper
- keep mount policy in one place
- make the worker and main-thread startup paths call the same setup code

### 6. No shared kernel yet

The background notes suggest a bigger future design: a single shared kernel with tabs acting as UI workers. The current port does not implement that. It only introduces dedicated workers for subprocess isolation.

Recommended follow-up:

- define a kernel protocol separate from DOM/editor protocol
- move worker lifecycle, pid allocation, and process registry behind that kernel
- evaluate `SharedWorker` once the dedicated-worker transport is stable

## Suggested Next Phases

### Phase 1: stabilize the dedicated-worker port

- validate `go build`, `go test`, and shell workflows in worker-backed subprocesses
- migrate terminal startup to the shared spawn router
- harden descriptor close/error paths

### Phase 2: centralize process orchestration

- central pid allocation
- process registry owned by one authority
- explicit process capability routing

### Phase 3: move toward a kernel model

- one kernel per origin/project
- tabs register themselves as editor/front-end workers
- process management and filesystem coordination leave the UI thread entirely

## Files Added or Changed for This Port

### New worker runtime

- `cmd/worker/main.go`
- `server/public/wasmWorker.js`
- `internal/jsworker/`
- `internal/worker/`

### Updated process / fs plumbing

- `internal/js/process/`
- `internal/process/`
- `internal/fs/`
- `internal/common/fid.go`
- `main.go`

## Summary

This port brings the dedicated-worker experiment from `feature/web-workers-2` onto the current branch without replacing the whole runtime model. The editor still stays on the DOM thread, but non-DOM subprocesses now have the scaffolding needed to move into browser workers. The next major step is not more low-level worker wiring; it is consolidating ownership of pids, process routing, and shared runtime state so the system can grow toward the longer-term shared-kernel design.
