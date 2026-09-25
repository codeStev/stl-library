package lowprio

import (
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

func TestApplyLowersCPUPriorityOfTheCurrentThread(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	if err := Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Getpriority returns 20-nice on Linux; nice 19 -> 1.
	prio, err := unix.Getpriority(unix.PRIO_PROCESS, unix.Gettid())
	if err != nil {
		t.Fatal(err)
	}
	if prio != 1 {
		t.Errorf("want nice 19 (getpriority 1), got %d", prio)
	}
}
