//go:build !linux

package sdcp

import "syscall"

// smallSendBuffer is a no-op here: Send then includes less of the network
// time (the kernel buffers more of each chunk).
func smallSendBuffer(_, _ string, _ syscall.RawConn) error { return nil }
