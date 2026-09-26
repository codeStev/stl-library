package disk

import (
	"io/fs"
	"syscall"
)

// changeTime returns the file's change time (ctime) in Unix seconds.
func changeTime(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ctim.Sec
	}
	return info.ModTime().Unix()
}
