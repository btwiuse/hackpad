//go:build js && wasm

// 示例 03: 进程上下文与工作目录
//
// 验证目标：
//   - os.Getwd() 返回正确的工作目录
//   - os.Chdir() 切换工作目录后，相对路径文件操作基于新目录
//   - os.Getenv / os.Environ 能读取环境变量
//   - os.Getpid() 返回非零 PID（依赖 patch 0001 / internal/js/process 注册 process.pid）
//   - 进程初始工作目录为 /home/me（由 internal/process.context.go 中 initialDirectory 设定）
//
// 相关补丁：
//   - patch 0001: internal/js/process.Init() 注册 process.cwd/chdir/pid，
//     通过 syscall.Getwd / syscall.Chdir 传达到 Go 标准库。

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS")
}

func run() error {
	// 测试 1: 初始工作目录
	if err := testInitialCwd(); err != nil {
		return fmt.Errorf("testInitialCwd: %w", err)
	}

	// 测试 2: Chdir 改变工作目录后相对路径解析
	if err := testChdir(); err != nil {
		return fmt.Errorf("testChdir: %w", err)
	}

	// 测试 3: PID 非零（说明 process.pid 被正确设置）
	if err := testPid(); err != nil {
		return fmt.Errorf("testPid: %w", err)
	}

	// 测试 4: 环境变量可读
	if err := testEnv(); err != nil {
		return fmt.Errorf("testEnv: %w", err)
	}

	return nil
}

func testInitialCwd() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("Getwd: %w", err)
	}
	// hackpad 的 init 进程工作目录固定为 /home/me
	if cwd != "/home/me" {
		return fmt.Errorf("expected /home/me, got %q", cwd)
	}
	return nil
}

func testChdir() error {
	// 先准备一个目录
	target := "/tmp/example03_chdir"
	if err := os.MkdirAll(target, 0755); err != nil {
		return fmt.Errorf("MkdirAll: %w", err)
	}

	original, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("Getwd before: %w", err)
	}

	if err := os.Chdir(target); err != nil {
		return fmt.Errorf("Chdir: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("Getwd after: %w", err)
	}
	if cwd != target {
		return fmt.Errorf("after Chdir: got %q, want %q", cwd, target)
	}

	// 用相对路径写文件
	relFile := "chdir_test.txt"
	if err := os.WriteFile(relFile, []byte("chdir works"), 0644); err != nil {
		return fmt.Errorf("WriteFile relative: %w", err)
	}

	// 绝对路径校验
	absFile := filepath.Join(target, relFile)
	data, err := os.ReadFile(absFile)
	if err != nil {
		return fmt.Errorf("ReadFile absolute: %w", err)
	}
	if string(data) != "chdir works" {
		return fmt.Errorf("content mismatch: %q", data)
	}

	// 还原
	if err := os.Chdir(original); err != nil {
		return fmt.Errorf("Chdir restore: %w", err)
	}
	return nil
}

func testPid() error {
	pid := os.Getpid()
	if pid <= 0 {
		return fmt.Errorf("expected positive PID, got %d", pid)
	}
	// hackpad 中 PID=1 是 init 进程
	return nil
}

func testEnv() error {
	// 设置并读取一个环境变量
	const key = "HACKPAD_EXAMPLE_VAR"
	const val = "hello_env"
	os.Setenv(key, val) //nolint:errcheck

	got := os.Getenv(key)
	if got != val {
		return fmt.Errorf("Getenv: got %q, want %q", got, val)
	}

	// Environ 应包含该变量
	found := false
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, key+"=") {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("Environ: %s not found", key)
	}
	return nil
}
