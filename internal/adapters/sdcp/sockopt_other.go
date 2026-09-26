//go:build !linux

package sdcp

import (
	"context"
	"net"
	"time"
)

// waitAcked can't ask the kernel here: Send then only measures until the
// request was written (the kernel may still be sending).
func waitAcked(context.Context, net.Conn) (time.Duration, TCPStats, bool) {
	return 0, TCPStats{}, false
}
