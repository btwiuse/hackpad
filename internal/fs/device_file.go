package fs

import (
	"io"
	"os"
	"time"

	"github.com/hack-pad/hackpadfs"
	"github.com/pkg/errors"
)

type deviceFile struct {
	name      string
	rawDevice io.ReadWriteCloser
	mode      os.FileMode
}

var _ hackpadfs.File = &deviceFile{}

func newDeviceFile(name string, rawDevice io.ReadWriteCloser, mode os.FileMode) *deviceFile {
	return &deviceFile{
		name:      name,
		rawDevice: rawDevice,
		mode:      mode,
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
	return deviceFileInfo{name: d.name, mode: d.mode}, nil
}

type deviceFileInfo struct {
	name string
	mode os.FileMode
}

func (i deviceFileInfo) Name() string             { return i.name }
func (i deviceFileInfo) Size() int64              { return 0 }
func (i deviceFileInfo) Mode() hackpadfs.FileMode { return hackpadfs.FileMode(i.mode) }
func (i deviceFileInfo) ModTime() time.Time       { return time.Time{} }
func (i deviceFileInfo) IsDir() bool              { return false }
func (i deviceFileInfo) Sys() interface{}         { return nil }
