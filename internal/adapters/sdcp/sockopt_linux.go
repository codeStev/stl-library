package sdcp

import "syscall"

// smallSendBuffer shrinks a socket's send buffer (tracing only).
func smallSendBuffer(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, traceSendBuffer)
	})
	if err != nil {
		return err
	}
	return serr
}
