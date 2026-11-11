package source

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// FastPollingFile is the flag file, relative to the yuneng config dir,
// that switches main.exe into Service Mode: "1" polls the inverters every
// ~20 s instead of every 5 min and stops the inverter production records
// (APS18) queued for EMA. The ECU web UI reads it with intval(fgets()).
// Its help text promises an automatic exit after 30 minutes; on fw
// C1.2.27A main.exe writes "0" back after 60 minutes.
const FastPollingFile = "fast_polling.conf"

// ReadFastPolling reports whether Service Mode is on, and since when (the
// file's mtime). A missing or unreadable file means normal mode.
func ReadFastPolling(path string) (on bool, since time.Time) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, time.Time{}
	}
	line, _, _ := strings.Cut(string(b), "\n")
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n != 1 {
		return false, time.Time{}
	}
	if fi, err := os.Stat(path); err == nil {
		since = fi.ModTime()
	}
	return true, since
}

// SetFastPolling writes the Service Mode flag the way the ECU web UI
// expects it ("1\n" or "0\n").
func SetFastPolling(path string, on bool) error {
	v := "0\n"
	if on {
		v = "1\n"
	}
	return os.WriteFile(path, []byte(v), 0o644)
}
