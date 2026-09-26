package app

import (
	"sync"
	"time"
)

// ScanState is what the last library scan did.
type ScanState struct {
	Running  bool
	Started  time.Time
	Finished time.Time // zero while the first scan runs
	Stats    SyncStats
	Error    string
	Pruned   int // stale thumbnails removed after the scan
}

// ScanStatus tracks the background scan and lets an admin ask for one.
type ScanStatus struct {
	mu     sync.Mutex
	state  ScanState
	wakeup chan struct{}
}

func NewScanStatus() *ScanStatus { return &ScanStatus{wakeup: make(chan struct{}, 1)} }

// Wakeup delivers rescan requests to the scan loop.
func (s *ScanStatus) Wakeup() <-chan struct{} { return s.wakeup }

// Request asks for a scan as soon as possible; false if one is already
// running or asked for.
func (s *ScanStatus) Request() bool {
	s.mu.Lock()
	running := s.state.Running
	s.mu.Unlock()
	if running {
		return false
	}
	select {
	case s.wakeup <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *ScanStatus) Start(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Running, s.state.Started = true, now
}

func (s *ScanStatus) Done(now time.Time, st SyncStats, err error, pruned int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Running, s.state.Finished, s.state.Stats, s.state.Pruned = false, now, st, pruned
	s.state.Error = ""
	if err != nil {
		s.state.Error = err.Error()
	}
}

func (s *ScanStatus) Get() ScanState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}
