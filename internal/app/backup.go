package app

import "time"

// BackupStatus tells when the library was last backed up, from a marker the backup job leaves
// behind: a file it touches when it is done, or the backup folder itself. The app knows nothing
// else about the backup; it only reads the marker's modification time.
type BackupStatus struct {
	Marker string
	// MaxAge is how old the marker may get before the backup counts as overdue.
	MaxAge time.Duration
	// ModTime reads the marker's modification time.
	ModTime func(path string) (time.Time, error)
	Now     func() time.Time
}

// BackupInfo is what is known about the backup.
type BackupInfo struct {
	Configured bool
	LastUnix   int64 // 0 when the marker can't be read
	Overdue    bool  // no backup seen, or older than MaxAge
	Error      string
}

// Info reads the marker. A nil BackupStatus is "not configured".
func (b *BackupStatus) Info() BackupInfo {
	if b == nil || b.Marker == "" || b.ModTime == nil {
		return BackupInfo{}
	}
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	info := BackupInfo{Configured: true}
	t, err := b.ModTime(b.Marker)
	if err != nil {
		info.Overdue, info.Error = true, "the backup marker can't be read: "+err.Error()
		return info
	}
	info.LastUnix = t.Unix()
	info.Overdue = b.MaxAge > 0 && now().Sub(t) > b.MaxAge
	return info
}
