//go:build js && wasm

// 示例 01: 基础文件系统读写
//
// 验证目标：
//   - os.MkdirAll 创建目录（依赖 window.fs.mkdir）
//   - os.WriteFile 写入文件（依赖 window.fs.open + write + close）
//   - os.ReadFile 读取文件内容（依赖 window.fs.open + read + close）
//   - os.Stat 获取文件元信息（依赖 window.fs.stat）
//   - os.Rename 重命名文件（依赖 window.fs.rename）
//   - os.Remove 删除文件（依赖 window.fs.unlink）
//   - os.ReadDir 列举目录（依赖 window.fs.readdir）
//
// 相关补丁：
//   - patch 0001: window.fs.* 接口通过 syscall_js_hackpad.go 与 fs_js_hackpad.go 打通
//   - patch 0002: fs.constants 由 init() 写入真实值，保证 O_CREAT/O_WRONLY 等标志正确

package main

import (
	"fmt"
	"os"
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
	base := "/tmp/example01"

	// 1. 创建目录
	dir := filepath.Join(base, "subdir")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("MkdirAll: %w", err)
	}

	// 2. 写入文件
	file := filepath.Join(dir, "hello.txt")
	content := []byte("Hello, Hackpad!\n")
	if err := os.WriteFile(file, content, 0644); err != nil {
		return fmt.Errorf("WriteFile: %w", err)
	}

	// 3. 读取文件内容并校验
	got, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("ReadFile: %w", err)
	}
	if string(got) != string(content) {
		return fmt.Errorf("content mismatch: got %q, want %q", got, content)
	}

	// 4. Stat 检查文件属性
	info, err := os.Stat(file)
	if err != nil {
		return fmt.Errorf("Stat: %w", err)
	}
	if info.Size() != int64(len(content)) {
		return fmt.Errorf("size mismatch: got %d, want %d", info.Size(), len(content))
	}
	if info.IsDir() {
		return fmt.Errorf("expected file, got directory")
	}

	// 5. 重命名文件
	renamed := filepath.Join(dir, "renamed.txt")
	if err := os.Rename(file, renamed); err != nil {
		return fmt.Errorf("Rename: %w", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		return fmt.Errorf("old file still exists after rename")
	}

	// 6. 列举目录
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("ReadDir: %w", err)
	}
	if len(entries) != 1 || entries[0].Name() != "renamed.txt" {
		return fmt.Errorf("unexpected dir entries: %v", entries)
	}

	// 7. 删除文件
	if err := os.Remove(renamed); err != nil {
		return fmt.Errorf("Remove: %w", err)
	}
	if _, err := os.Stat(renamed); !os.IsNotExist(err) {
		return fmt.Errorf("file still exists after remove")
	}

	// 8. 多层目录下的文件追加写
	deepDir := filepath.Join(base, "a", "b", "c")
	if err := os.MkdirAll(deepDir, 0755); err != nil {
		return fmt.Errorf("MkdirAll deep: %w", err)
	}
	deepFile := filepath.Join(deepDir, "data.bin")
	f, err := os.OpenFile(deepFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("OpenFile create: %w", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := fmt.Fprintf(f, "line %d\n", i); err != nil {
			f.Close()
			return fmt.Errorf("Write line %d: %w", i, err)
		}
	}
	f.Close()

	got, err = os.ReadFile(deepFile)
	if err != nil {
		return fmt.Errorf("ReadFile deep: %w", err)
	}
	expected := "line 0\nline 1\nline 2\n"
	if string(got) != expected {
		return fmt.Errorf("deep file content mismatch: got %q", got)
	}

	return nil
}
