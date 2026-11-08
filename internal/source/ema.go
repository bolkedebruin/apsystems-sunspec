package source

import (
	"context"
	"database/sql"
	"io"
	"os"
	"strings"
	"time"
)

// EMA upload bookkeeping on the ECU (fw C1.2.27A):
//
//   - /home/record.db, table Data(item, record, resendflag, date_time), is
//     the queue /home/applications/client uploads to EMA over a persistent
//     TCP connection. main.exe adds an "APS18…" production row every
//     5 minutes in normal mode and none in Service Mode; JSON meter rows
//     ("VER":"M0001") keep coming either way. The newest row of each kind
//     carries resendflag "2" until the next one arrives, older ones "0".
//   - /home/control_client.log is control_client's log; it contacts the
//     EMA control server every 15 minutes.
//   - /tmp/connectemaflag.txt holds "connected" while client is connected.
//
// record.db and the log live on the ECU's flash and belong to APsystems'
// processes, so they're read rarely (Builder.EMAInterval), record.db as
// an in-memory copy rather than through a SQLite connection to the file.

// EMAConnection is the state of client's link to EMA.
type EMAConnection int

const (
	EMAUnknown EMAConnection = iota
	EMADisconnected
	EMAConnected
)

// EMAStatus summarises what the ECU queues and sends to EMA.
type EMAStatus struct {
	Connected           EMAConnection
	LastProdRecord      time.Time // date_time of the newest APS18 row; zero if none
	ProdRecordsPending  int       // APS18 rows not yet marked sent (resendflag != "0")
	ProdRecordsLastHour int       // APS18 rows dated within the hour before now
	LastControlContact  time.Time // last successful control_client session; zero if unknown
}

const recordTimeLayout = "20060102150405"

// ParseRecordDB reads the EMA queue from raw, a byte copy of record.db.
// The copy is written to os.TempDir() — /tmp, a tmpfs (RAM) on the ECU —
// opened read-only and removed right away, so the live file on flash is
// never opened as a database and nothing is written to flash.
// (modernc.org/sqlite v1.34.1's (*conn).Deserialize frees a buffer it
// didn't malloc and crashes, hence the file round-trip.) A copy taken
// mid-write may be torn; that returns an error and the caller keeps its
// previous status.
func ParseRecordDB(ctx context.Context, raw []byte, now time.Time) (EMAStatus, error) {
	var st EMAStatus
	f, err := os.CreateTemp("", "ecu-sunspec-record-*.db")
	if err != nil {
		return st, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return st, err
	}
	if err := f.Close(); err != nil {
		return st, err
	}

	db, err := sql.Open("sqlite", "file:"+f.Name()+"?mode=ro&immutable=1")
	if err != nil {
		return st, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT resendflag, date_time FROM Data WHERE record LIKE 'APS18%'`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	hourAgo := now.Add(-time.Hour)
	for rows.Next() {
		var flag, dt sql.NullString
		if err := rows.Scan(&flag, &dt); err != nil {
			return st, err
		}
		if strings.TrimSpace(flag.String) != "0" {
			st.ProdRecordsPending++
		}
		at, err := time.ParseInLocation(recordTimeLayout, strings.TrimSpace(dt.String), time.Local)
		if err != nil {
			continue
		}
		if at.After(st.LastProdRecord) {
			st.LastProdRecord = at
		}
		if !at.Before(hourAgo) && !at.After(now) {
			st.ProdRecordsLastHour++
		}
	}
	return st, rows.Err()
}

// LastEMAControlContact returns the time of the last successful session
// in a control_client.log excerpt ("(2026-11-07 13:27:35)Connect to EMA
// successfully SSL"); zero if there is none.
func LastEMAControlContact(log string) time.Time {
	var last time.Time
	for _, line := range strings.Split(log, "\n") {
		if !strings.Contains(line, "Connect to EMA successfully") {
			continue
		}
		if len(line) < 21 || line[0] != '(' || line[20] != ')' {
			continue
		}
		if at, err := time.ParseInLocation("2006-01-02 15:04:05", line[1:20], time.Local); err == nil {
			last = at
		}
	}
	return last
}

// ReadEMAConnected reads /tmp/connectemaflag.txt.
func ReadEMAConnected(path string) EMAConnection {
	b, err := os.ReadFile(path)
	if err != nil {
		return EMAUnknown
	}
	if strings.TrimSpace(string(b)) == "connected" {
		return EMAConnected
	}
	return EMADisconnected
}

// controlLogTailBytes is how much of control_client.log is read: a few
// 15-minute sessions, out of a ~750 KB file.
const controlLogTailBytes = 8 << 10

// readTail returns at most n bytes from the end of path.
func readTail(path string, n int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	off := fi.Size() - n
	if off < 0 {
		off = 0
	}
	b, err := io.ReadAll(io.NewSectionReader(f, off, fi.Size()-off))
	return string(b), err
}

// readEMAStatus gathers the EMA status from the configured files. Each
// source is optional; a failure leaves that part at its previous value.
func (b *Builder) readEMAStatus(ctx context.Context, prev EMAStatus, now time.Time) EMAStatus {
	st := prev
	if b.EMAFlagFile != "" {
		st.Connected = ReadEMAConnected(b.EMAFlagFile)
	}
	if b.RecordDB != "" {
		if raw, err := os.ReadFile(b.RecordDB); err == nil {
			if rec, err := ParseRecordDB(ctx, raw, now); err == nil {
				st.LastProdRecord = rec.LastProdRecord
				st.ProdRecordsPending = rec.ProdRecordsPending
				st.ProdRecordsLastHour = rec.ProdRecordsLastHour
			}
		}
	}
	if b.ControlLog != "" {
		if tail, err := readTail(b.ControlLog, controlLogTailBytes); err == nil {
			if at := LastEMAControlContact(tail); !at.IsZero() {
				st.LastControlContact = at
			}
		}
	}
	return st
}
