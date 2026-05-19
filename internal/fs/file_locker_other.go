//go:build !js

package fs

import (
	"context"
	"sync"
)

type localFileLocker struct{}

var (
	processFileLocks = make(map[string]*sync.RWMutex)
	newFileLockMu    sync.Mutex
)

func newFileLocker(context.Context) (fileLockManager, error) {
	return localFileLocker{}, nil
}

func (localFileLocker) Lock(_ context.Context, filePath string, _ bool) error {
	if _, ok := processFileLocks[filePath]; !ok {
		newFileLockMu.Lock()
		if _, ok := processFileLocks[filePath]; !ok {
			processFileLocks[filePath] = new(sync.RWMutex)
		}
		newFileLockMu.Unlock()
	}
	processFileLocks[filePath].Lock()
	return nil
}

func (localFileLocker) Unlock(_ context.Context, filePath string) error {
	if lock, ok := processFileLocks[filePath]; ok {
		lock.Unlock()
	}
	return nil
}
