//go:build js && wasm

// 示例 06: LookPath 与可执行文件查找
//
// 验证目标：
//   - exec.LookPath 在 js/wasm 下能正常工作（依赖 patch 0001 删除 lp_wasm.go）
//   - 将可执行的 wasm 文件放到 PATH 中的目录后，LookPath 能找到它
//   - 对于不存在的命令，LookPath 返回 exec.ErrNotFound 而不是 panic
//   - os/exec.Command 能通过 LookPath 解析命令路径
//
// 相关补丁：
//   - patch 0001: 删除 src/os/exec/lp_wasm.go（它把所有 LookPath 硬编码为 ErrNotFound），
//     改为让 lp_unix.go（build tag 扩展到 js && wasm）处理路径查找。
//     lp_unix.go 会遍历 PATH 中的目录，对每个候选路径调用 os.Stat 检查可执行位，
//     而 os.Stat → window.fs.stat → internal/js/fs.stat → internal/fs.Stat。

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	if err := run(); err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS")
}

func run() error {
	// 测试 1: 对不存在的命令，LookPath 返回 ErrNotFound
	if err := testLookPathNotFound(); err != nil {
		return fmt.Errorf("testLookPathNotFound: %w", err)
	}

	// 测试 2: 将伪 wasm 可执行文件放入 PATH，LookPath 能找到
	if err := testLookPathFound(); err != nil {
		return fmt.Errorf("testLookPathFound: %w", err)
	}

	// 测试 3: 绝对路径的 LookPath
	if err := testLookPathAbsolute(); err != nil {
		return fmt.Errorf("testLookPathAbsolute: %w", err)
	}

	return nil
}

func testLookPathNotFound() error {
	_, err := exec.LookPath("this_command_definitely_does_not_exist_xyz123")
	if err == nil {
		return fmt.Errorf("expected error, got nil")
	}
	// 应该是 *exec.Error 包装的 exec.ErrNotFound
	if !isNotFound(err) {
		return fmt.Errorf("expected ErrNotFound, got: %v", err)
	}
	return nil
}

func testLookPathFound() error {
	// 创建一个临时 bin 目录
	binDir := "/tmp/example06_bin"
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("MkdirAll: %w", err)
	}
	defer os.RemoveAll(binDir)

	// 写入一个伪可执行文件（内容是合法的 wasm magic number）
	// 在 hackpad 环境中，可执行文件必须是 wasm 格式（magic: \x00asm）
	cmdPath := filepath.Join(binDir, "myfakecommand")
	wasmMagic := []byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00}
	if err := os.WriteFile(cmdPath, wasmMagic, 0755); err != nil {
		return fmt.Errorf("WriteFile: %w", err)
	}

	// 设置 PATH 包含 binDir
	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)
	os.Setenv("PATH", binDir+":"+origPath)

	// LookPath 应能找到该命令
	found, err := exec.LookPath("myfakecommand")
	if err != nil {
		return fmt.Errorf("LookPath: %w", err)
	}
	if found != cmdPath {
		return fmt.Errorf("found %q, want %q", found, cmdPath)
	}

	return nil
}

func testLookPathAbsolute() error {
	// 用绝对路径时，LookPath 直接检查该路径是否可执行
	absPath := "/tmp/example06_abstest"
	wasmMagic := []byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00}
	if err := os.WriteFile(absPath, wasmMagic, 0755); err != nil {
		return fmt.Errorf("WriteFile abs: %w", err)
	}
	defer os.Remove(absPath)

	found, err := exec.LookPath(absPath)
	if err != nil {
		return fmt.Errorf("LookPath abs: %w", err)
	}
	if found != absPath {
		return fmt.Errorf("abs: found %q, want %q", found, absPath)
	}
	return nil
}

// isNotFound 检查错误是否是"找不到可执行文件"
func isNotFound(err error) bool {
	if err == exec.ErrNotFound {
		return true
	}
	if e, ok := err.(*exec.Error); ok {
		return e.Err == exec.ErrNotFound
	}
	return false
}
