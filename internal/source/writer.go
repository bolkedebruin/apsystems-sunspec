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

// SetProtectionParam queues a single grid-protection parameter write into
// `set_protection_parameters_inverter`. main.exe picks it up on the next
// ZigBee dispatch cycle and translates it into the radio command that
// programs the inverter. The parameter_name MUST be the long descriptive
// name accepted by main.exe (e.g. "grid_recovery_time", not "AG") — the
// firmware's strcmp ladder only matches the long form. See main.exe symbol
// table dump for the full set; common ones include:
//
//	grid_recovery_time            (AG, reconnect time, range_min 10s)
//	under_voltage_slow            (AC, UV stage 3 trip)
//	under_voltage_fast            (AQ, UV stage 2 fast trip)
//	over_voltage_slow             (AD, OV stage 2 slow trip)
//	Over_Voltage_stage3           (AY, OV stage 3 trip)
//	under_frequency_fast          (AJ, UF fast trip)
//	over_frequency_fast           (AK, OF fast trip)
//	Under_Voltage{1,2,3}_clearance_time
//	Over_Voltage{1,2,3}_clearance_time
//	Under_Frequency{1,2}_clearance_time
//	Over_Frequency{1,2}_clearance_time
//	Reconnection_under_voltage / over_voltage / under_frequency / over_frequency
//
// Caller is responsible for value-range validation — this function does NOT
// range-check (the menu values vary per regulatory profile, and validating
// against a particular spec's bounds requires reader access). The inverter
// firmware enforces its own range_max as a safety net (silent-rejects values
// out of range).
func (w *Writer) SetProtectionParam(ctx context.Context, invUID, paramName string, value float64) error {
	if w == nil {
		return errors.New("writer not initialized")
	}
	if invUID == "" {
		return errors.New("inverter UID required")
	}
	if paramName == "" {
		return errors.New("parameter name required")
	}
	_, err := w.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO set_protection_parameters_inverter
		 (id, parameter_name, parameter_value, set_flag) VALUES (?, ?, ?, 1)`,
		invUID, paramName, value)
	return err
}
