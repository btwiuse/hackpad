//go:build js && wasm

package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime/debug"
	"syscall/js"

	"github.com/hack-pad/go-indexeddb/idb"
	"github.com/hack-pad/hackpad/internal/common"
	"github.com/hack-pad/hackpad/internal/fs"
	"github.com/hack-pad/hackpad/internal/global"
	"github.com/hack-pad/hackpad/internal/install"
	"github.com/hack-pad/hackpad/internal/interop"
	"github.com/hack-pad/hackpad/internal/jsworker"
	"github.com/hack-pad/hackpad/internal/log"
	"github.com/hack-pad/hackpad/internal/terminal"
	"github.com/hack-pad/hackpad/internal/worker"
	"github.com/hack-pad/hackpadfs"
	"github.com/hack-pad/hackpadfs/indexeddb"
	"github.com/johnstarich/go/datasize"
)

func main() {
	defer common.CatchExceptionHandler(func(err error) {
		log.Error("Hackpad panic:", err, "\n", string(debug.Stack()))
		os.Exit(1)
	})

	bootCtx := context.Background()
	dom, err := worker.ExecDOM(
		bootCtx,
		jsworker.GetLocal(),
		"editor",
		[]string{"-editor=editor"},
		"/home/me",
		map[string]string{
			"GOMODCACHE": "/home/me/.cache/go-mod",
			"GOPROXY":    "https://proxy.golang.org/",
			"GOROOT":     "/usr/local/go",
			"HOME":       "/home/me",
			"PATH":       "/bin:/home/me/go/bin:/usr/local/go/bin/js_wasm:/usr/local/go/pkg/tool/js_wasm",
		},
	)
	if err != nil {
		panic(err)
	}

	global.Set("profile", js.FuncOf(interop.ProfileJS))
	global.Set("install", js.FuncOf(install.InstallFunc))
	global.Set("spawnTerminal", js.FuncOf(terminal.SpawnTerminal))

	if err := setUpFS(); err != nil {
		panic(err)
	}
	if err := install.InstallPath("wasm/editor.wasm"); err != nil {
		panic(err)
	}
	if err := install.InstallPath("wasm/sh.wasm"); err != nil {
		panic(err)
	}
	if err := dom.Start(); err != nil {
		panic(err)
	}
	interop.SetInitialized()

	select {}
}

func setUpFS() error {
	const dirPerm = 0o700
	mkdirMount := func(mountPath string, durability idb.TransactionDurability) error {
		if err := os.MkdirAll(mountPath, dirPerm); err != nil {
			return err
		}
		return overlayIndexedDB(mountPath, durability)
	}

	if err := mkdirMount("/bin", idb.DurabilityRelaxed); err != nil {
		return err
	}
	if err := mkdirMount("/home/me", idb.DurabilityDefault); err != nil {
		return err
	}
	if err := mkdirMount("/home/me/.cache", idb.DurabilityRelaxed); err != nil {
		return err
	}
	if err := mkdirMount("/tmp", idb.DurabilityRelaxed); err != nil {
		return err
	}

	if err := os.MkdirAll("/usr/local/go", dirPerm); err != nil {
		return err
	}
	return overlayTarGzip("/usr/local/go", "wasm/go.tar.gz", []string{
		"/usr/local/go/bin/js_wasm",
		"/usr/local/go/pkg/tool/js_wasm",
	})
}

func overlayIndexedDB(mountPath string, durability idb.TransactionDurability) error {
	idbFS, err := indexeddb.NewFS(context.Background(), mountPath, indexeddb.Options{
		TransactionDurability: durability,
	})
	if err != nil {
		return err
	}
	return fs.Overlay(mountPath, idbFS)
}

func overlayTarGzip(mountPath, downloadPath string, skipCacheDirs []string) error {
	u, err := url.Parse(downloadPath)
	if err != nil {
		return err
	}
	resp, err := http.Get(u.Path) // nolint:bodyclose
	if err != nil {
		return err
	}

	skipDirs := make(map[string]bool)
	for _, d := range skipCacheDirs {
		skipDirs[common.ResolvePath("/", d)] = true
	}
	maxFileBytes := datasize.Kibibytes(100).Bytes()
	shouldCache := func(name string, info hackpadfs.FileInfo) bool {
		return !skipDirs[path.Dir(name)] && info.Size() < maxFileBytes
	}
	return fs.OverlayTarGzip(mountPath, resp.Body, true, shouldCache)
}
