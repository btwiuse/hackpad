#!/usr/bin/env bash
# run.sh — 编译并运行 examples/ 下的示例
#
# 用法：
#   ./run.sh                 # 运行所有示例
#   ./run.sh all             # 同上
#   ./run.sh 01_fs_readwrite # 只运行指定示例
#   ./run.sh 04_large_argv   # 带大 argv 的特殊测试
#
# 前置条件：
#   1. GOROOT_PATCHED 指向已应用 patches/*.patch 的 Go 工具链根目录
#      export GOROOT_PATCHED=/path/to/patched-go
#
#   2. wasm_exec.js 已复制到 examples/ 目录：
#      cp "$GOROOT_PATCHED/lib/wasm/wasm_exec.js" examples/
#
#   3. Node.js 12+ 已安装

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# ── 环境检查 ─────────────────────────────────────────────────────────────────

if [[ -z "${GOROOT_PATCHED:-}" ]]; then
  # 尝试从 PATH 里找已打补丁的 go 工具链（以版本号包含 "btwiuse" 为特征）
  PATCHED_GO="$(command -v go 2>/dev/null || true)"
  if [[ -n "$PATCHED_GO" ]]; then
    GOROOT_PATCHED="$(go env GOROOT)"
  else
    echo "ERROR: GOROOT_PATCHED is not set."
    echo "Please build the patched Go toolchain and set:"
    echo "  export GOROOT_PATCHED=/path/to/patched-go"
    exit 1
  fi
fi

GO_CMD="$GOROOT_PATCHED/bin/go"
if [[ ! -x "$GO_CMD" ]]; then
  echo "ERROR: go binary not found at $GO_CMD"
  exit 1
fi

if ! command -v node &>/dev/null; then
  echo "ERROR: node is not installed. Please install Node.js 12+."
  exit 1
fi

WASM_EXEC_JS="$SCRIPT_DIR/wasm_exec.js"
if [[ ! -f "$WASM_EXEC_JS" ]]; then
  echo "Copying wasm_exec.js from patched toolchain..."
  cp "$GOROOT_PATCHED/lib/wasm/wasm_exec.js" "$WASM_EXEC_JS"
fi

# ── 示例列表 ─────────────────────────────────────────────────────────────────

ALL_EXAMPLES=(
  "01_fs_readwrite"
  "02_pipe"
  "03_process_context"
  "04_large_argv"
  "05_flock"
  "06_lookpath"
)

# ── 编译函数 ─────────────────────────────────────────────────────────────────

compile_example() {
  local name="$1"
  local src_dir="$SCRIPT_DIR/$name"
  local out="/tmp/hackpad_example_${name}.wasm"

  if [[ ! -d "$src_dir" ]]; then
    echo "  ERROR: directory $src_dir not found"
    return 1
  fi

  echo -n "  Compiling $name... "
  if GOOS=js GOARCH=wasm "$GO_CMD" build -o "$out" "$src_dir/main.go" 2>&1; then
    echo "OK"
  else
    echo "FAILED"
    return 1
  fi
  echo "$out"
}

# ── 运行函数 ─────────────────────────────────────────────────────────────────

run_example() {
  local name="$1"
  shift
  local wasm_file="/tmp/hackpad_example_${name}.wasm"
  local extra_args=("$@")

  echo -n "  Running $name... "
  local output
  if output=$(node "$SCRIPT_DIR/run_wasm.js" "$wasm_file" "${extra_args[@]}" 2>&1); then
    if echo "$output" | grep -q "^PASS"; then
      echo "PASS"
      return 0
    else
      echo "FAIL (no PASS in output)"
      echo "    Output: $output"
      return 1
    fi
  else
    echo "FAIL (exit code $?)"
    echo "    Output: $output"
    return 1
  fi
}

# ── 特殊参数构造 ──────────────────────────────────────────────────────────────

# 为 04_large_argv 构造超过原 12KB 限制的 argv
build_large_args() {
  local args=()
  # 构造约 800 个参数，总长度约 17KB（超过原 12KB = 4096+8192 限制）
  for i in $(seq 0 799); do
    args+=("--check-arg=$i")
  done
  echo "${args[@]}"
}

# ── 主逻辑 ───────────────────────────────────────────────────────────────────

TARGETS=("${@:-all}")
if [[ "${TARGETS[0]}" == "all" ]]; then
  TARGETS=("${ALL_EXAMPLES[@]}")
fi

PASS=0
FAIL=0
SKIP=0

for target in "${TARGETS[@]}"; do
  printf "\n[%s]\n" "$target"

  # 编译
  compile_example "$target" || { ((FAIL++)); continue; }

  # 运行（04_large_argv 需要特殊参数）
  if [[ "$target" == "04_large_argv" ]]; then
    large_args=$(build_large_args)
    if run_example "$target" $large_args; then
      ((PASS++))
    else
      ((FAIL++))
    fi
  else
    if run_example "$target"; then
      ((PASS++))
    else
      ((FAIL++))
    fi
  fi
done

# ── 汇总 ─────────────────────────────────────────────────────────────────────

echo ""
echo "────────────────────────────────"
echo "Results: $PASS passed, $FAIL failed, $SKIP skipped"
echo "────────────────────────────────"

if [[ $FAIL -gt 0 ]]; then
  echo "OVERALL: FAIL"
  exit 1
else
  echo "OVERALL: PASS"
  exit 0
fi
