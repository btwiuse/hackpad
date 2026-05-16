#!/usr/bin/env node
// run_wasm.js — 在 Node.js 中运行 wasm 文件（使用打补丁后的 wasm_exec.js）
//
// 用法：
//   node run_wasm.js <path-to.wasm> [arg1 arg2 ...]
//
// 依赖：
//   - wasm_exec.js 必须与本文件在同一目录（从打了补丁的 Go 工具链复制而来）
//   - Node.js 12+（需要 WebAssembly.instantiateStreaming 或 WebAssembly.instantiate）

'use strict';

const fs = require('fs');
const path = require('path');

// 加载打了补丁的 wasm_exec.js
const wasmExecPath = path.join(__dirname, 'wasm_exec.js');
if (!fs.existsSync(wasmExecPath)) {
  console.error('ERROR: wasm_exec.js not found at', wasmExecPath);
  console.error('Please copy it from your patched Go toolchain:');
  console.error('  cp $GOROOT_PATCHED/lib/wasm/wasm_exec.js', __dirname);
  process.exit(1);
}
require(wasmExecPath);

const wasmFile = process.argv[2];
if (!wasmFile) {
  console.error('Usage: node run_wasm.js <wasm-file> [args...]');
  process.exit(1);
}

const extraArgs = process.argv.slice(3);

async function main() {
  const go = new Go();

  // 将额外参数传给 wasm 程序（os.Args）
  go.argv = [wasmFile, ...extraArgs];

  // 读取并实例化 wasm 模块
  const wasmBuffer = fs.readFileSync(wasmFile);
  let result;
  try {
    result = await WebAssembly.instantiate(wasmBuffer, go.importObject);
  } catch (e) {
    console.error('Failed to instantiate wasm:', e.message);
    process.exit(1);
  }

  // 运行 Go 程序，等待退出
  try {
    await go.run(result.instance);
  } catch (e) {
    console.error('Runtime error:', e.message);
    process.exit(1);
  }
}

main().catch(e => {
  console.error('Unhandled error:', e);
  process.exit(1);
});
