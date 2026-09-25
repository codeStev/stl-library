//go:build !linux

package lowprio

// Apply is a no-op outside Linux.
func Apply() error { return nil }
