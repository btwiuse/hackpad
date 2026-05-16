//go:build js && wasm

// 示例 05: 文件锁 (flock)
//
// 验证目标：
//   - syscall.Flock 在 js/wasm 下可用（依赖 patch 0001 filelock_unix.go build tag 扩展）
//   - os.File 的文件锁定通过 internal/fs.FileDescriptors.Flock() 实现互斥
//   - 多个 goroutine 并发访问同一文件时，文件锁保证串行化写入
//
// 相关补丁：
//   - patch 0001:
//     * filelock_other.go 的 build tag 加入 !(js && wasm)，防止 js/wasm 用空实现
//     * filelock_unix.go 的 build tag 加入 (js && wasm)，让 js/wasm 走 Unix flock 路径
//     * syscall_js_hackpad.go 实现 Flock()，调用 window.fs.flock
//     * internal/js/fs 注册 fs.flock，调用 internal/fs.FileDescriptors.Flock()
//     * internal/fs.FileDescriptors.Flock() 用 sync.RWMutex 实现进程级互斥

package main

import (
	"fmt"
	"os"
	"sync"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS")
}

func run() error {
	// 测试 1: 基础 flock 获取与释放
	if err := testBasicFlock(); err != nil {
		return fmt.Errorf("testBasicFlock: %w", err)
	}

	// 测试 2: 并发 goroutine 通过 flock 实现互斥写入
	if err := testConcurrentFlock(); err != nil {
		return fmt.Errorf("testConcurrentFlock: %w", err)
	}

	return nil
}

func testBasicFlock() error {
	lockFile := "/tmp/example05_basic.lock"
	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("OpenFile: %w", err)
	}
	defer os.Remove(lockFile)
	defer f.Close()

	// 获取独占锁
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("LOCK_EX: %w", err)
	}

	// 写入内容
	if _, err := f.WriteString("locked\n"); err != nil {
		return fmt.Errorf("Write while locked: %w", err)
	}

	// 释放锁
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("LOCK_UN: %w", err)
	}

	return nil
}

func testConcurrentFlock() error {
	lockFile := "/tmp/example05_concurrent.lock"
	dataFile := "/tmp/example05_data.txt"

	// 创建锁文件
	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("OpenFile lock: %w", err)
	}
	defer os.Remove(lockFile)
	defer os.Remove(dataFile)
	defer f.Close()

	const workers = 5
	const writes = 3
	var wg sync.WaitGroup
	errors := make(chan error, workers)

	for w := 0; w < workers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			lf, err := os.OpenFile(lockFile, os.O_RDWR, 0600)
			if err != nil {
				errors <- fmt.Errorf("worker %d: open lock: %w", w, err)
				return
			}
			defer lf.Close()

			for i := 0; i < writes; i++ {
				// 获取独占锁
				if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
					errors <- fmt.Errorf("worker %d: LOCK_EX: %w", w, err)
					return
				}

				// 临界区：追加写入
				df, err := os.OpenFile(dataFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) //nolint:errcheck
					errors <- fmt.Errorf("worker %d: open data: %w", w, err)
					return
				}
				fmt.Fprintf(df, "w%d-i%d\n", w, i)
				df.Close()

				// 释放锁
				if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_UN); err != nil {
					errors <- fmt.Errorf("worker %d: LOCK_UN: %w", w, err)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			return err
		}
	}

	// 验证数据文件：每条记录都完整（没有被并发写入损坏）
	data, err := os.ReadFile(dataFile)
	if err != nil {
		return fmt.Errorf("ReadFile data: %w", err)
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	expected := workers * writes
	if lines != expected {
		return fmt.Errorf("expected %d lines, got %d\ndata:\n%s", expected, lines, data)
	}

	return nil
}
