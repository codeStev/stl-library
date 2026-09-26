package printer

import "testing"

func TestJobProgressAndRemaining(t *testing.T) {
	j := Job{State: JobPrinting, Layer: 500, Layers: 2000, ElapsedMs: 1000, TotalMs: 5000}
	if j.Progress() != 0.25 || j.RemainingMs() != 4000 || !j.Active() {
		t.Errorf("%v %v %v", j.Progress(), j.RemainingMs(), j.Active())
	}
	done := Job{State: JobComplete, Layer: 2000, Layers: 2000, ElapsedMs: 5001, TotalMs: 5000}
	if done.Progress() != 1 || done.RemainingMs() != 0 || done.Active() {
		t.Errorf("done: %v %v %v", done.Progress(), done.RemainingMs(), done.Active())
	}
	if (Job{}).Progress() != 0 {
		t.Error("no layers")
	}
}

func TestPrintable(t *testing.T) {
	for name, want := range map[string]bool{"a.ctb": true, "B.GOO": true, "a.chitubox": false, "a.lys": false, "a.stl": false, "ctb": false} {
		if Printable(name) != want {
			t.Errorf("%s: %v", name, !want)
		}
	}
}
