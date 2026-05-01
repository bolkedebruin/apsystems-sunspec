package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
)

// MinPanelLimitW and MaxPanelLimitW are the bounds enforced by the ECU's
// own PHP set_maxpower() endpoint:
//
//	if ($maxpower < 20 || $maxpower > 500) error;
//
// We mirror them here so a client writing through SunSpec gets the same
// error semantics as one using the local web UI.
const (
	MinPanelLimitW = 20
	MaxPanelLimitW = 500
)

// Writer issues control actions to the ECU by inserting / updating rows in
// /home/database.db. main.exe polls the relevant tables on its next ZigBee
// cycle and dispatches whatever it finds with set_flag=1.
//
// Latency from a Writer call to actual radio dispatch is bounded by the
// ECU's polling cadence — typically 30 s in fast-poll mode, 300 s default
// (see /etc/yuneng/polling_interval.conf).
type Writer struct {
	db *sql.DB
}

// OpenWriter opens a read-write handle on database.db. Concurrent writes
// from main.exe are safe because both processes use SQLite's WAL journal.
//
// The caller must Close() when done.
func OpenWriter(dir string) (*Writer, error) {
	q := url.Values{}
	q.Set("mode", "rw")
	q.Set("_txlock", "immediate") // grab the write lock at BEGIN, fail fast on contention
	dsn := fmt.Sprintf("file:%s?%s", filepath.Join(dir, "database.db"), q.Encode())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database.db rw: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1) // serialize all our writes against ourselves
	return &Writer{db: db}, nil
}

func (w *Writer) Close() error {
	if w == nil || w.db == nil {
		return nil
	}
	return w.db.Close()
}

// SetMaxPower writes a per-panel watt cap for one inverter. Mirrors the
// ECU's own PHP set_maxpower endpoint (CodeIgniter), which writes:
//
//	UPDATE power SET limitedpower=?,flag=1 WHERE id=?
//
// Range-checks against [MinPanelLimitW, MaxPanelLimitW] before issuing.
func (w *Writer) SetMaxPower(ctx context.Context, invUID string, panelW int) error {
	if w == nil {
		return errors.New("writer not initialized")
	}
	if panelW < MinPanelLimitW || panelW > MaxPanelLimitW {
		return fmt.Errorf("panel power %d outside [%d..%d]",
			panelW, MinPanelLimitW, MaxPanelLimitW)
	}
	_, err := w.db.ExecContext(ctx,
		`UPDATE power SET limitedpower = ?, flag = 1 WHERE id = ?`,
		panelW, invUID)
	return err
}

// SetTurnOnOff queues an on/off command for one inverter. main.exe's
// process_turn_on_off picks this up on its next radio cycle.
//
// state: 1 = on, 0 = off.
func (w *Writer) SetTurnOnOff(ctx context.Context, invUID string, on bool) error {
	if w == nil {
		return errors.New("writer not initialized")
	}
	state := 0
	if on {
		state = 1
	}
	_, err := w.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO turn_on_off (id, set_flag) VALUES (?, ?)`,
		invUID, state)
	return err
}

// RestoreFullPower undoes any active per-panel curtailment for one inverter
// by setting the cap back to MaxPanelLimitW.
func (w *Writer) RestoreFullPower(ctx context.Context, invUID string) error {
	return w.SetMaxPower(ctx, invUID, MaxPanelLimitW)
}
