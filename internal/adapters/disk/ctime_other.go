//go:build !linux

package disk

import "io/fs"

// changeTime falls back to the modification time where the change time
// isn't available.
func changeTime(info fs.FileInfo) int64 { return info.ModTime().Unix() }
