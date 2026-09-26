package sdcp

import (
	"context"
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// waitAcked waits until everything written to c has been acknowledged by
// the other side (tracing only), and returns the connection's smoothed
// round-trip time.
func waitAcked(ctx context.Context, c net.Conn) (time.Duration, bool) {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return 0, false
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return 0, false
	}
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		var info *unix.TCPInfo
		var ierr error
		if err := raw.Control(func(fd uintptr) { info, ierr = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO) }); err != nil || ierr != nil {
			return 0, false // closed
		}
		if info.Unacked == 0 && info.Notsent_bytes == 0 {
			return time.Duration(info.Rtt) * time.Microsecond, true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return 0, false
}
