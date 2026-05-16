# Hackpad 内部架构设计文档

## 概述

Hackpad 是一个在浏览器中运行完整 Go 工具链的 WebAssembly 应用。它的核心挑战是：**浏览器沙箱没有操作系统提供的真实文件系统、进程管理和 Unix 系统调用**，而 Go 的标准库和工具链却严重依赖这些能力。

为了解决这个矛盾，Hackpad 采用两条相互配合的技术路线：

1. **`./internal` 包**：在 Go/wasm 运行时内部，用 Go 代码重新实现了文件系统、进程调度、管道等 OS 原语，并通过 `window.fs` 和 `window.child_process` 这两个 JS 全局对象暴露给 Go 的 `syscall/js` 层。

2. **`./patches` 补丁**：修改 Go 标准工具链，让原本在 `js/wasm` 目标上返回 `ENOSYS` 的系统调用（`StartProcess`、`Wait4`、`Pipe`、`Flock` 等）改为调用上面 `internal` 暴露的 JS 接口。

两者缺一不可：**补丁打通了调用链路，internal 提供了真实实现**。

---

## 整体架构

```
┌──────────────────────────────────────────────────────────────┐
│                       浏览器 / Node.js                        │
│                                                              │
│  window.fs.*          window.child_process.*                 │
│  (open/read/write/    (spawn/wait)                           │
│   pipe/flock/...)      │                                     │
│         │              │                                     │
│         ▼              ▼                                     │
│ ┌─────────────────────────────────────────────────────────┐  │
│ │              hackpad.wasm (Go/wasm 主进程)               │  │
│ │                                                         │  │
│ │  main.go                                                │  │
│ │   ├─ internal/js/fs.Init()    → 注册 window.fs.*        │  │
│ │   ├─ internal/js/process.Init() → 注册 window.child_process.* │
│ │   └─ global.Set("spawnTerminal", ...)                   │  │
│ │                                                         │  │
│ │  ┌──────────────┐  ┌───────────────┐  ┌─────────────┐  │  │
│ │  │ internal/fs  │  │internal/process│  │internal/    │  │  │
│ │  │ (VFS + FD表) │  │(进程调度+上下文)│  │interop      │  │  │
│ │  └──────┬───────┘  └──────┬────────┘  │(JS<->Go桥)  │  │  │
│ │         │                 │           └─────────────┘  │  │
│ │         └────────┬────────┘                            │  │
│ │                  ▼                                      │  │
│ │  ┌───────────────────────────────────────────────────┐  │  │
│ │  │    hackpadfs (VFS 抽象层)                          │  │  │
│ │  │   mem.FS / indexeddb.FS / mount.FS / tar.FS       │  │  │
│ │  └───────────────────────────────────────────────────┘  │  │
│ └─────────────────────────────────────────────────────────┘  │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐  │
│  │         子进程 wasm (例如 go build 编译的工具)           │  │
│  │   Go 标准库 syscall/fs_js.go                           │  │
│  │     → 调用 window.fs.open/read/write/...               │  │
│  │   Go 标准库 syscall/syscall_js.go (patched)            │  │
│  │     → 调用 window.child_process.spawn/wait             │  │
│  └────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────┘
```

---

## 模块详解

### `internal/fs` — 虚拟文件系统 + 文件描述符表

**作用**：为每个进程维护一套独立的文件描述符表（FID → 文件对象映射），并在其之上提供完整的 POSIX 文件操作语义。

**核心数据结构**：
- `FileDescriptors`：每个进程持有一个实例，包含 `map[FID]*fileDescriptor`，以及当前工作目录。
- `fileDescriptor`：封装了底层的 `hackpadfs.File`，记录文件名、打开参数等。
- `filesystem`（全局）：一个 `mount.FS`，根目录是内存 FS，可在任意挂载点叠加 IndexedDB FS 或 tar FS。

**关键实现**：

| 文件 | 功能 |
|------|------|
| `file_descriptors.go` | `Open/Close/Read/Write/Stat/Mkdir/Rename/Flock` 等完整 POSIX 操作 |
| `pipe.go` | 用 `chan byte`（32KiB 缓冲）实现双向有名管道，提供 `pipeReadOnly` / `pipeWriteOnly` 语义 |
| `fs.go` | 全局 VFS 初始化、`Overlay`/`OverlayTarGzip` 挂载点管理 |
| `wasm_cache.go` | 在 VFS 之上叠加 wasm 模块内存缓存（避免重复 `WebAssembly.compile`） |
| `stdout.go` | `/dev/stdout` 和 `/dev/stderr` 特殊文件，Write 走 `log.Print`/`log.Error` |
| `working_directory.go` | 进程当前目录，`Set` 是异步的（为兼容 IndexedDB FS 的异步 `Stat`）|

**为什么必须有这层**：浏览器没有 POSIX 文件系统。Go 标准库所有的 `os.Open`、`ioutil.ReadFile` 等调用最终落到 `syscall/fs_js.go`，后者调用 `window.fs.*`。如果 `window.fs.*` 全是 stub（返回 ENOSYS），那么任何涉及文件的 Go 程序都无法运行。

---

### `internal/process` — 进程调度器

**作用**：在单线程 wasm 运行时内，用协程（goroutine）模拟多进程语义，维护进程表（PID → process）和当前活跃进程上下文。

**核心概念**：

- **"进程"** 本质上是一个 wasm 模块实例。每次 `spawn` 都调用 `WebAssembly.instantiate` 加载一个 wasm 二进制，在独立的 goroutine 中运行，退出后回收。
- **上下文切换**（`switchContext`）：切换 `currentPID` 并通知 JS 侧更新 `process.pid`/`process.ppid`，保证 `window.child_process.spawn` 在任意时刻都看到正确的当前进程 ID。
- **文件描述符继承**：`NewFileDescriptors` 按 `attr.Files` 列表从父进程 FD 表中 `Dup` 出子进程所需的 FD（stdin/stdout/stderr 和额外 FD），实现 fork-exec 语义。

**关键文件**：

| 文件 | 功能 |
|------|------|
| `process.go` | `Process` 接口、`New`/`Start`/`Wait`、进程表管理 |
| `context.go` | `Init`（创建 PID=1 的 init 进程）、`switchContext`、`Current` |
| `wasm.go` (js) | `run`：加载 wasm → 创建 `Go` 实例 → 包装 `exports.run/resume` 做上下文切换 → `goInstance.run(wrapperInstance)` |
| `process_js.go` | `JSValue()`：把 process 序列化成 JS 对象返回给 `child_process.spawn` 调用方 |
| `attr.go` | `ProcAttr`：`Dir`/`Env`/`Files` |

**为什么必须有这层**：wasm 是单线程的，没有真正的 fork。标准 `os/exec.Cmd.Start` 最终调用 `syscall.StartProcess`，而标准工具链把这个函数实现为直接 `return 0, 0, ENOSYS`。Hackpad 的补丁把它替换成调用 `window.child_process.spawn`，后者由 `internal/js/process` 注册，实际上是在 Go 堆上创建一个新 wasm 实例并在新 goroutine 中运行。

---

### `internal/interop` — JS ↔ Go 类型桥接

**作用**：封装 `syscall/js` 的底层 API，提供高层次的 Go ↔ JS 转换工具。

**主要功能**：

- **`SetFunc`**：把 Go 函数注册到 JS 对象属性上，支持两种调用约定：
  - `Func`（同步）：直接返回值。
  - `CallbackFunc`（异步）：最后一个参数是 Node.js 风格的 `callback(err, ...results)`，Go 侧用 goroutine 异步执行后回调。
- **`WrapAsJSError`**：把 Go error 转换为 JS `{ message, code }` 对象，其中 `code` 是 errno 字符串（`ENOENT`、`EBADF` 等），使 Go 标准库能正确解析文件操作错误。
- **`mapToErrNo`**：把 `hackpadfs` 的平台无关错误映射到 POSIX errno 字符串。
- **类型转换工具**：`SliceFromStrings`、`Entries`、`StringMap` 等，消除 `js.Value` 的样板代码。

---

### `internal/js/fs` — `window.fs` 实现

**作用**：把 `internal/fs` 的 Go 文件系统操作暴露为 `window.fs.*` 方法（Node.js `fs` 模块兼容接口），供 Go 运行时的 `syscall/fs_js.go` 调用。

`Init()` 中注册了约 30 个函数（`open`/`read`/`write`/`stat`/`mkdir`/`rename`/`unlink`/`readdir`/`pipe`/`flock`/...），每个函数都有同步版（`*Sync`）和异步版（callback 风格）。

除了标准 `fs` 方法之外，还额外暴露了：
- `hackpad.overlayTarGzip`：用 tar.gz 文件覆盖挂载一个目录（用于安装 Go 工具链）
- `hackpad.overlayIndexedDB`：把 IndexedDB 挂载为持久化文件系统
- `hackpad.getMounts`/`destroyMount`：挂载点管理

**为什么必须有这层**：Go 运行时使用的 `window.fs` 默认只有极简 stub（`writeSync` + 一些 `enosys()`）。没有这一层注册，任何 `os.Open`、`os.Mkdir` 等调用都会返回 `ENOSYS`。

---

### `internal/js/process` — `window.child_process` 实现

**作用**：把 `internal/process` 暴露为 `window.child_process.*` 和 `window.process.*` 接口。

`Init()` 注册：
- `process.cwd`/`chdir`/`umask`/`getuid`/`getgid`/`getgroups`/`pid`/`ppid`
- `child_process.spawn`：创建新进程（加载 wasm 二进制）
- `child_process.wait`/`waitSync`：等待子进程退出，返回 `{ pid, exitCode }`

**调用链路**：

```
Go 程序调用 os/exec.Cmd.Start()
  → syscall.StartProcess()  [patch 0001 的 syscall_js_hackpad.go]
    → window.child_process.spawn(name, args, options)
      → internal/js/process.spawn()
        → internal/process.New() + Start()
          → WebAssembly.instantiate(wasmBlob, importObject)
            → 新 goroutine 中运行子进程的 wasm 实例
```

---

### `internal/terminal` — 终端会话管理

**作用**：把进程的 stdin/stdout/stderr 对接到浏览器 terminal 组件（如 xterm.js）。

`Open()` 的工作流程：
1. 用 `fs.FileDescriptors.Pipe()` 为 stdin/stdout/stderr 各创建一条管道。
2. 调用 `process.New()` 以管道的各端作为子进程的 stdio。
3. 注册 `term.onData(fn)` 回调，把 terminal 输入写入 stdin 管道的写端。
4. 启动两个 goroutine，分别从 stdout/stderr 管道读端读取数据，写回 `term.write(buf)`。

---

### `internal/global` — `window.hackpad` 命名空间

**作用**：提供 `window.hackpad` 对象的 `Get`/`Set`/`SetDefault` 操作，避免 hackpad 的 JS API 污染全局命名空间。

---

### `internal/console` — 控制台接口

**作用**：定义 `Console` 接口（`Stdout()`/`Stderr()`/`Note()` 返回 `io.Writer`），用于在不同平台（浏览器/native）提供统一的输出接口。

---

### `internal/promise` — JS Promise 包装

**作用**：封装 `syscall/js` 的 Promise 操作，提供 Go 风格的 `Await()` 阻塞式等待。

`Promise.Await()` 内部用 channel 阻塞当前 goroutine，由 JS 侧的 `.then`/`.catch` 回调来发送结果——这是 Go wasm 异步编程的标准范式。

---

### `internal/log` — 日志

**作用**：在 wasm 环境下路由 `log.Print`/`log.Error` 等到 `console.log`/`console.error`，在非 wasm 环境下路由到标准输出。

---

### `internal/common` — 通用类型

**作用**：定义 `PID`、`FID` 等核心类型，以及路径解析工具 `ResolvePath`，避免循环依赖。

---

## 为什么补丁是必须的

### 根本原因

Go 标准工具链对 `js/wasm` 目标的假设是：**这是一个没有文件系统、没有进程、没有管道的极简环境**。所有可能涉及这些能力的系统调用都被实现为直接返回 `ENOSYS`，或者 `lp_wasm.go` 这样的专用文件明确拒绝功能。

这对 Go 工具链自身来说是致命的：`go build`、`go test` 等工具大量使用 `os.Exec`、文件锁、管道——它们在标准 wasm 工具链下根本无法工作。

### Patch 0001 — 初始 wasm 补丁（syscall 级别）

**问题**：标准工具链中：
- `lp_wasm.go` 把 `exec.LookPath` 硬编码为返回 `ErrNotFound`——任何 `os/exec.Command` 都找不到可执行文件。
- `pipe_wasm.go` 把 `os.Pipe` 硬编码为返回 `ENOSYS`——无法创建管道，`os/exec.Cmd` 的 `StdoutPipe()` 等失效。
- `syscall_js.go` 把 `StartProcess`/`Wait4` 硬编码为 `ENOSYS`——任何子进程启动都失败。
- `WaitStatus.Exited()`/`ExitStatus()` 永远返回 `false`/`0`——无法获取退出码。
- `filelock_other.go` 的 build tag 排除了 `js && wasm`，导致 `go build` 内部的文件锁无法使用。

**补丁做了什么**：
1. 删除 `lp_wasm.go`，让 `lp_unix.go`（`//go:build unix || (js && wasm)`）处理 wasm 下的路径查找，从而走 `window.fs.stat` 检查文件可执行位。
2. 删除 `pipe_wasm.go`，让 `pipe_unix.go` 处理 wasm 下的 `os.Pipe`（走 `window.fs.pipe`）；新增 `fs_js_hackpad.go` 实现 `syscall.Pipe`，尝试调用 `window.fs.pipe`，不可用时 fallback 到 ENOSYS。
3. 新增 `syscall_js_hackpad.go`，实现 `StartProcess`（调用 `window.child_process.spawn`）、`Wait4`（调用 `window.child_process.wait`）、`Flock`（调用 `window.fs.flock`）、`WaitStatus` 编码等。
4. 修改 filelock 和 `internal/syscall/unix` 的 build tag，让文件锁走 Unix 实现路径（配合 `window.fs.flock`）。
5. **修复 `wasm_exec.js` 的循环崩溃**：把 `defaultWriteSync` 提升到模块作用域，让 Go 运行时的 `fd_write` 直接使用它，避免用户覆盖 `window.fs.writeSync` 时触发的无限递归。

**与 internal 的配合**：
```
patch 删除的 ENOSYS stub → patch 新增的 JS 调用 → internal/js/* 注册的实现 → internal/fs、internal/process 的 Go 实现
```

### Patch 0002 — FS syscall 常量默认值

**问题**：`wasm_exec.js` 把 `window.fs.constants` 里的所有标志位（`O_WRONLY`、`O_CREAT` 等）全部初始化为 `-1`（占位符）。Go 运行时的 `syscall/fs_js.go` 在 `init()` 中读取这些常量来确定内部的 `nodeDIRECTORY` 等标志位——读到 `-1` 就会用错误的值。

**补丁做了什么**：在 `fs_js.go` 的 `init()` 开头，用 Go 编译时已知的真实值（`O_WRONLY`、`O_RDWR`、`O_CREAT` 等）回写到 `window.fs.constants`，然后再读取。

**与 internal 的配合**：`internal/js/fs.Init()` 也会写这些常量（两者都写，先后顺序有保证），确保无论 init 顺序如何，JS 侧和 Go 侧看到的标志位都一致。

### Patch 0003 — 大 argv 支持

**问题**：标准工具链中，wasm 的命令行参数总大小上限是 `4096 + 8192 = 12288` 字节（约 12KB）。Go 工具链在传递编译参数时（特别是传递大量 `-I` 包含路径时）会轻易超过这个限制，导致运行时抛出 `"total length of command line and environment variables exceeds limit"` 错误。

**补丁做了什么**：把 `wasmMinDataAddr` 从 `4096 + 8192` 改为 `131072`（128KB），并同步修改 `wasm_exec.js` 中的对应常量（两者必须保持一致，注释也要求 "Keep in sync"）。

**为什么 128K**：Go 工具链在编译标准库时，一条 `compile` 命令的参数可能包含数十个包路径，轻松超过 12KB；128KB 提供了足够的余量。

### Patch 0004 — GitHub Actions 发布工作流

不涉及运行时行为，是 CI 基础设施，用于自动构建打包修改后的 Go 工具链并发布到 GitHub Releases。

---

## 补丁与 internal 模块的交互矩阵

| 补丁修改点 | 调用的 JS 接口 | 注册该接口的 internal 模块 | 实际实现 |
|-----------|---------------|--------------------------|---------|
| `syscall.StartProcess` | `child_process.spawn` | `internal/js/process` | `internal/process.New().Start()` |
| `syscall.Wait4` | `child_process.wait` | `internal/js/process` | `internal/process.Get(pid).Wait()` |
| `syscall.Flock` | `fs.flock` | `internal/js/fs` | `internal/fs.FileDescriptors.Flock()` |
| `syscall.Pipe` | `fs.pipe` | `internal/js/fs` | `internal/fs.FileDescriptors.Pipe()` |
| `exec.LookPath` (via lp_unix.go) | `fs.stat` | `internal/js/fs` | `internal/fs.FileDescriptors.Stat()` |
| `os.Pipe` (via pipe_unix.go) | `fs.pipe` | `internal/js/fs` | `internal/fs.FileDescriptors.Pipe()` |
| `filelock_unix.go` (flock) | `fs.flock` | `internal/js/fs` | `internal/fs.FileDescriptors.Flock()` |
| `fs_js.go init()` 常量写回 | `fs.constants.*` | `internal/js/fs.Init()` | Go 编译时常量 |
| `wasmMinDataAddr = 128K` | wasm_exec.js 参数解析 | N/A（运行时保护） | 扩大 argv 缓冲区 |
