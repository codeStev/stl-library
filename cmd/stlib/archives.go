package main

import (
	"log/slog"
	"os"
	"os/exec"

	"github.com/codeStev/stl-library/internal/adapters/disk"
)

// newDownloads reads the downloads folder. 7z and rar archives are unpacked
// when the 7z and unrar programs are found (on the PATH, or named by
// SEVENZIP_BIN / UNRAR_BIN); IMPORT_TMP is where an archive is unpacked
// (default: the system's temp folder - it needs room for the largest archive).
func newDownloads(root string) disk.Downloads {
	d := disk.Downloads{Root: root, TempDir: os.Getenv("IMPORT_TMP")}
	d.SevenZip = findTool("SEVENZIP_BIN", "7z", "7zz", "7za")
	d.Unrar = findTool("UNRAR_BIN", "unrar")
	return d
}

func findTool(env string, names ...string) string {
	if p := os.Getenv(env); p != "" {
		return p
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func logArchiveTools(d disk.Downloads) {
	slog.Info("archives in downloads", "zip", "built in", "7z", orNone(d.SevenZip), "rar", orNone(d.Unrar))
}

func orNone(s string) string {
	if s == "" {
		return "not available (kept as plain files)"
	}
	return s
}
