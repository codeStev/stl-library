//go:build !linux

package diskfree

import "errors"

// Free is not available outside Linux.
func Free(string) (free, total uint64, err error) {
	return 0, 0, errors.New("free disk space is only read on Linux")
}
