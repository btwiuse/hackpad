//go:build js && wasm

// 示例 02: 管道 (Pipe) 通信
//
// 验证目标：
//   - os.Pipe() 成功创建读/写端文件（依赖 patch 0001 删除 pipe_wasm.go）
//   - 向写端写入数据，从读端读到完整数据
//   - 关闭写端后读端收到 EOF
//   - io.Pipe() 在 wasm 下与标准行为一致（纯 Go 实现，无 syscall 依赖）
//   - 通过管道实现跨 goroutine 通信（模拟进程间管道场景）
//
// 相关补丁：
//   - patch 0001: 删除 src/os/pipe_wasm.go，让 os.Pipe 走 pipe_unix.go，
//     pipe_unix.go 调用 syscall.Pipe，后者通过 fs_js_hackpad.go 调用 window.fs.pipe。
//     window.fs.pipe 由 internal/js/fs 注册，最终调用 internal/fs.FileDescriptors.Pipe()，
//     用 chan byte 实现真正可用的管道。

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
)

func main() {
	if err := run(); err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS")
}

func run() error {
	// 测试 1: os.Pipe 基础读写
	if err := testOsPipe(); err != nil {
		return fmt.Errorf("testOsPipe: %w", err)
	}

	// 测试 2: os.Pipe 关闭写端后 EOF
	if err := testPipeEOF(); err != nil {
		return fmt.Errorf("testPipeEOF: %w", err)
	}

	// 测试 3: 大批量数据通过管道传输
	if err := testPipeLargeData(); err != nil {
		return fmt.Errorf("testPipeLargeData: %w", err)
	}

	// 测试 4: io.Pipe（纯 Go，不依赖 syscall）
	if err := testIoPipe(); err != nil {
		return fmt.Errorf("testIoPipe: %w", err)
	}

	return nil
}

func testOsPipe() error {
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("os.Pipe: %w", err)
	}
	defer r.Close()

	msg := "hello from pipe"
	done := make(chan error, 1)
	go func() {
		_, err := fmt.Fprint(w, msg)
		w.Close()
		done <- err
	}()

	buf, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("ReadAll: %w", err)
	}
	if werr := <-done; werr != nil {
		return fmt.Errorf("write: %w", werr)
	}
	if string(buf) != msg {
		return fmt.Errorf("got %q, want %q", buf, msg)
	}
	return nil
}

func testPipeEOF() error {
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("os.Pipe: %w", err)
	}

	// 立即关闭写端，读端应该直接得到 EOF
	w.Close()

	buf := make([]byte, 16)
	n, err := r.Read(buf)
	r.Close()
	if n != 0 {
		return fmt.Errorf("expected 0 bytes, got %d", n)
	}
	if err != io.EOF {
		return fmt.Errorf("expected EOF, got %v", err)
	}
	return nil
}

func testPipeLargeData() error {
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("os.Pipe: %w", err)
	}

	const size = 64 * 1024 // 64KB，超过单次 Write 缓冲
	data := bytes.Repeat([]byte("x"), size)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer w.Close()
		w.Write(data) //nolint:errcheck
	}()

	got, err := io.ReadAll(r)
	r.Close()
	wg.Wait()
	if err != nil {
		return fmt.Errorf("ReadAll large: %w", err)
	}
	if len(got) != size {
		return fmt.Errorf("large data: got %d bytes, want %d", len(got), size)
	}
	return nil
}

func testIoPipe() error {
	pr, pw := io.Pipe()

	lines := []string{"alpha\n", "beta\n", "gamma\n"}
	go func() {
		for _, l := range lines {
			pw.Write([]byte(l)) //nolint:errcheck
		}
		pw.Close()
	}()

	got, err := io.ReadAll(pr)
	if err != nil {
		return fmt.Errorf("io.Pipe ReadAll: %w", err)
	}
	want := "alpha\nbeta\ngamma\n"
	if string(got) != want {
		return fmt.Errorf("io.Pipe: got %q, want %q", got, want)
	}
	return nil
}
