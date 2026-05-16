# Hackpad Examples

本目录包含一系列可运行的示例，用于验证 `./internal` 模块配合 `./patches` 补丁能否达到预期效果。

每个示例都是一个独立的 `GOOS=js GOARCH=wasm` Go 程序，需要使用已应用补丁的 Go 工具链编译。

## 前置条件

### 1. 构建打了补丁的 Go 工具链

```bash
# 克隆上游 Go（以实际版本为准）
git clone https://go.googlesource.com/go go-patched
cd go-patched

# 应用所有补丁
git am ../hackpad/patches/*.patch

# 构建工具链
cd src
./make.bash
cd ../..
```

### 2. 设置环境变量

```bash
export GOROOT_PATCHED="$(pwd)/go-patched"
export PATH="$GOROOT_PATCHED/bin:$PATH"
```

### 3. 获取 wasm_exec.js

```bash
cp "$GOROOT_PATCHED/lib/wasm/wasm_exec.js" examples/
```

## 示例列表

| 目录 | 验证目标 | 相关 patch |
|------|---------|-----------|
| `01_fs_readwrite/` | 文件系统读写、目录操作 | 0001 (window.fs)、0002 (fs constants) |
| `02_pipe/` | 管道创建与进程间通信 | 0001 (Pipe / pipe_unix.go) |
| `03_process_context/` | 进程上下文切换、工作目录 | 0001 (StartProcess/Wait4) |
| `04_large_argv/` | 超过原 12KB 限制的大 argv | 0003 (wasmMinDataAddr = 128K) |
| `05_flock/` | 文件锁（flock）跨进程互斥 | 0001 (filelock_unix.go / Flock) |
| `06_lookpath/` | PATH 路径查找与可执行文件检测 | 0001 (lp_unix.go) |

## 运行方式

### 方式一：使用 run.sh（推荐）

```bash
cd examples
./run.sh 01_fs_readwrite   # 编译并在 Node.js 中运行
./run.sh all               # 运行所有示例
```

### 方式二：手动编译运行

```bash
# 编译（需要已打补丁的工具链）
GOOS=js GOARCH=wasm go build -o /tmp/example.wasm ./examples/01_fs_readwrite/

# 在 Node.js 中运行（需要 wasm_exec.js）
node examples/run_wasm.js /tmp/example.wasm
```

### 方式三：在浏览器中运行

```bash
# 启动静态文件服务
cd examples
python3 -m http.server 8080
# 浏览器访问 http://localhost:8080/browser_runner.html
```

## 预期输出

每个示例在成功时输出 `PASS`，在失败时输出 `FAIL: <原因>` 并以非零退出码退出。

示例 `./run.sh all` 预期输出：

```
[01_fs_readwrite]  PASS
[02_pipe]          PASS
[03_process_context] PASS
[04_large_argv]    PASS
[05_flock]         PASS
[06_lookpath]      PASS
All examples passed.
```
