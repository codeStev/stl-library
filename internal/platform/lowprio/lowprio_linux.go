// Package lowprio makes the running process as polite as possible towards
// everything else on a small, shared server: lowest CPU priority and idle
// I/O class, so a multi-minute library walk never slows down other
// services.
package lowprio

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

const (
	ioprioClassShift = 13
	ioprioClassIdle  = 3
	ioprioWhoProcess = 1
)

// Apply lowers the priority of every thread that exists right now. Both
// settings apply per thread on Linux, and the Go runtime has already
// started several threads before main runs - so each one is set
// individually. Threads created later inherit the setting from the
// thread that spawns them. Best effort: returns the first error but keeps
// going.
func Apply() error {
	tids, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return err
	}
	var firstErr error
	for _, t := range tids {
		tid, err := strconv.Atoi(t.Name())
		if err != nil {
			continue
		}
		if err := unix.Setpriority(unix.PRIO_PROCESS, tid, 19); err != nil && firstErr == nil {
			firstErr = err
		}
		prio := ioprioClassIdle << ioprioClassShift
		if _, _, errno := unix.Syscall(unix.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(tid), uintptr(prio)); errno != 0 && firstErr == nil {
			firstErr = errno
		}
	}
	return firstErr
}
