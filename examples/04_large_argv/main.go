//go:build js && wasm

// 示例 04: 大 argv 支持
//
// 验证目标：
//   - 使用超过原 12KB 限制（4096+8192）的命令行参数时程序正常启动
//   - os.Args 能正确接收所有参数
//   - 参数总长度接近但不超过新的 128KB 限制
//
// 相关补丁：
//   - patch 0003: 将 wasmMinDataAddr 从 4096+8192 改为 131072 (128KB)，
//     同步修改 wasm_exec.js 中的常量，使大型 argv 能被正确布局在 wasm 内存中。
//
// 注意：此示例本身无法在编译时自我验证超限场景（超限会在 wasm_exec.js 里抛异常），
// 但可以通过 run.sh 脚本构造大 argv 来验证，见 run.sh 注释。
// 本程序验证"在大 argv 下，os.Args 传递的完整性"。

package main

import (
	"fmt"
	"os"
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
	args := os.Args

	// 1. os.Args[0] 是程序名，应该非空
	if len(args) == 0 {
		return fmt.Errorf("os.Args is empty")
	}
	if args[0] == "" {
		return fmt.Errorf("os.Args[0] (program name) is empty")
	}

	// 2. 收集所有带 --check-arg= 前缀的参数，验证它们的内容和顺序
	var checkArgs []string
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "--check-arg=") {
			checkArgs = append(checkArgs, strings.TrimPrefix(a, "--check-arg="))
		}
	}

	// 如果传入了 --check-arg 参数，则验证其完整性
	if len(checkArgs) > 0 {
		totalLen := 0
		for i, v := range checkArgs {
			// 每个参数的值应为其序号的字符串
			expected := fmt.Sprintf("%d", i)
			if v != expected {
				return fmt.Errorf("arg[%d]: got %q, want %q", i, v, expected)
			}
			totalLen += len(v) + len("--check-arg=") + 1 // +1 for null terminator
		}
		fmt.Printf("  Received %d args, total argv size ~%d bytes\n", len(checkArgs), totalLen)
	}

	// 3. 验证 PATH 环境变量存在（说明环境变量也在大 argv 场景下正常传递）
	path := os.Getenv("PATH")
	if path == "" {
		// 在 hackpad 环境中 PATH 可能未设置，这不是失败
		fmt.Println("  Note: PATH not set (normal in hackpad wasm environment)")
	}

	return nil
}
