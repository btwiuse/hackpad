package fs

import (
	"io"
	"os"
	"time"

	"github.com/hack-pad/hackpadfs"
	"github.com/pkg/errors"
)

type deviceFile struct {
	unimplementedFile

	name      string
	rawDevice io.ReadWriteCloser
}

var _ hackpadfs.File = &deviceFile{}

func newDeviceFile(name string, rawDevice io.ReadWriteCloser) *deviceFile {
	return &deviceFile{
		name:      name,
		rawDevice: rawDevice,
	}
}

func (d *deviceFile) Read(p []byte) (n int, err error) {
	n, err = d.rawDevice.Read(p)
	return n, errors.WithStack(err)
}

func (d *deviceFile) Write(p []byte) (n int, err error) {
	n, err = d.rawDevice.Write(p)
	return n, errors.WithStack(err)
}

func (d *deviceFile) Close() error {
	return d.rawDevice.Close()
}

func (d *deviceFile) Stat() (hackpadfs.FileInfo, error) {
	return namedFileInfo(d.name), nil
}

type namedFileInfo string

func (n namedFileInfo) Name() string       { return string(n) }
func (n namedFileInfo) Size() int64        { return 0 }
func (n namedFileInfo) Mode() os.FileMode  { return os.ModeDevice }
func (n namedFileInfo) ModTime() time.Time { return time.Time{} }
func (n namedFileInfo) IsDir() bool        { return false }
func (n namedFileInfo) Sys() interface{}   { return nil }
