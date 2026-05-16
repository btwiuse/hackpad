# Patches

本仓库是 **Go toolchain** 的 fork（基于上游 Go 源码），通过 `hackpad/` 目录下的补丁对其做了定制修改，生成用于 WebAssembly 等场景的 mvp-go。

## Patch 说明

| Patch | 作用 |
|---|---|
| `0001-hackpad-Add-initial-go-wasm-patches.patch` | 初始的 wasm 运行时补丁，包含 wasm 执行器、内存管理、胶水代码等 |
| `0002-hackpad-provide-sane-defaults-for-fs-syscalls.patch` | 为 wasm 环境下的文件系统系统调用提供合理的默认值，避免未实现的 syscall 导致 panic |
| `0003-hackpad-allow-extra-large-argv.patch` | 放宽 wasm 环境下 argv 的大小限制，支持传递更大的命令行参数 |

## 重新应用

按顺序使用 `git am` 一次性应用所有 patch：

```bash
cd go
git am patches/*.patch
```

逐个应用（按依赖顺序）：

```bash
git am patches/0001-*.patch  # wasm 补丁基础
git am patches/0002-*.patch  # fs syscall 默认值
git am patches/0003-*.patch  # 大 argv 支持
```

> **注意**：这些 patch 必须按编号顺序应用，后面的 patch 可能依赖前面的修改。

## 构建产物

应用 patch 后，可以通过以下方式构建定制 toolchain：

```bash
# 本地构建
cd go/src
./make.bash

# 或通过 GitHub Actions
# 在仓库页面 Actions → Build and release modified Go toolchain → Run workflow
```
