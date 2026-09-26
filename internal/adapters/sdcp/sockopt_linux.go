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
// round-trip time and what the kernel saw while sending.
func waitAcked(ctx context.Context, c net.Conn) (time.Duration, TCPStats, bool) {
	var st TCPStats
	sc, ok := c.(syscall.Conn)
	if !ok {
		return 0, st, false
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return 0, st, false
	}
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		var info *unix.TCPInfo
		var ierr error
		if err := raw.Control(func(fd uintptr) { info, ierr = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO) }); err != nil || ierr != nil {
			return 0, st, false // closed
		}
		if st.MinWindow == 0 || info.Snd_wnd < st.MinWindow {
			st.MinWindow = info.Snd_wnd
		}
		st.MaxWindow = max(st.MaxWindow, info.Snd_wnd)
		if info.Unacked == 0 && info.Notsent_bytes == 0 {
			us := func(v uint64) time.Duration { return time.Duration(v) * time.Microsecond }
			st.Busy, st.RwndLimited, st.SndbufLimited = us(info.Busy_time), us(info.Rwnd_limited), us(info.Sndbuf_limited)
			st.MSS, st.Retransmits = info.Snd_mss, info.Total_retrans
			return time.Duration(info.Rtt) * time.Microsecond, st, true
		}
		time.Sleep(time.Millisecond)
	}
	return 0, st, false
}
