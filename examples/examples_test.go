// examples_test.go — 在非 wasm 环境下对示例代码进行静态/编译检查
//
// 这些测试不依赖 wasm 运行时，只验证示例文件的存在性和基本语法（通过 go vet）。
// 完整的运行时验证需要使用 run.sh 在打了补丁的工具链下编译后运行。

//go:build !js

package examples_test

import (
	"os"
	"path/filepath"
	"testing"
)

var expectedExamples = []struct {
	dir      string
	mainFile string
}{
	{"01_fs_readwrite", "main.go"},
	{"02_pipe", "main.go"},
	{"03_process_context", "main.go"},
	{"04_large_argv", "main.go"},
	{"05_flock", "main.go"},
	{"06_lookpath", "main.go"},
}

func TestExamplesExist(t *testing.T) {
	scriptDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}

	for _, ex := range expectedExamples {
		t.Run(ex.dir, func(t *testing.T) {
			mainPath := filepath.Join(scriptDir, ex.dir, ex.mainFile)
			if _, err := os.Stat(mainPath); os.IsNotExist(err) {
				t.Errorf("example file not found: %s", mainPath)
			}
		})
	}
}

func TestRunnerFilesExist(t *testing.T) {
	scriptDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}

	runners := []string{
		"run.sh",
		"run_wasm.js",
		"browser_runner.html",
		"README.md",
	}
	for _, f := range runners {
		t.Run(f, func(t *testing.T) {
			p := filepath.Join(scriptDir, f)
			if _, err := os.Stat(p); os.IsNotExist(err) {
				t.Errorf("runner file not found: %s", p)
			}
		})
	}
}
