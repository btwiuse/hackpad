package fs

import "context"

type fileLockManager interface {
	Lock(ctx context.Context, filePath string, shared bool) error
	Unlock(ctx context.Context, filePath string) error
}
