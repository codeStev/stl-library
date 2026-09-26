// Package printer describes a resin printer's state and print jobs,
// independent of how the printer is reached. Pure.
package printer

import "strings"

// Machine is what the printer is doing overall.
type Machine string

const (
	Offline      Machine = "offline"
	Idle         Machine = "idle"
	Printing     Machine = "printing"
	Transferring Machine = "transferring" // receiving a file
	Testing      Machine = "testing"      // exposure or device test
)

// JobState is the state of the current (or last) print.
type JobState string

const (
	JobIdle      JobState = "idle"
	JobPreparing JobState = "preparing" // homing, dropping, checking the file
	JobPrinting  JobState = "printing"
	JobPausing   JobState = "pausing"
	JobPaused    JobState = "paused"
	JobStopping  JobState = "stopping"
	JobStopped   JobState = "stopped"
	JobComplete  JobState = "complete"
)

// Status is a snapshot of the printer.
type Status struct {
	Name     string // e.g. "Saturn 4 Ultra"
	Firmware string
	Machine  Machine
	Job      *Job // nil when the printer reports none
	UVTemp   float64
}

// Job is the current or last print.
type Job struct {
	File      string
	State     JobState
	Layer     int
	Layers    int
	ElapsedMs int64
	TotalMs   int64 // the printer's estimate for the whole job
	Error     string
}

// Progress is the share of layers done, 0..1.
func (j Job) Progress() float64 {
	if j.Layers <= 0 {
		return 0
	}
	p := float64(j.Layer) / float64(j.Layers)
	return min(max(p, 0), 1)
}

// RemainingMs is the printer's estimate of time left, 0 when done or
// unknown.
func (j Job) RemainingMs() int64 {
	if j.State == JobComplete || j.State == JobStopped || j.TotalMs <= j.ElapsedMs {
		return 0
	}
	return j.TotalMs - j.ElapsedMs
}

// Active reports a job the printer is working on (including paused).
func (j Job) Active() bool {
	switch j.State {
	case JobPreparing, JobPrinting, JobPausing, JobPaused, JobStopping:
		return true
	}
	return false
}

// Printable reports sliced files a network resin printer can print
// directly (Chitubox's .ctb and ELEGOO's .goo). Project files (.chitubox,
// .lys) and models (.stl) have to be sliced first.
func Printable(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".ctb") || strings.HasSuffix(n, ".goo")
}

// File is an entry of the printer's storage.
type File struct {
	Path   string // "/local/model.ctb"
	Folder bool
	Size   int64 // files
	Used   int64 // storage volumes
	Total  int64
}

// RemotePath turns a name into a path on the printer: bare names are in
// the internal storage ("/local").
func RemotePath(name string) string {
	if strings.HasPrefix(name, "/") {
		return name
	}
	return "/local/" + name
}
