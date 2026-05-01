package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// SQLiteReader reads cached state main.exe writes after each poll cycle.
//
// Files held: /home/database.db (live, mmapped by main.exe — open RO),
// /home/historical.db (energy aggregates).
type SQLiteReader struct {
	live *sql.DB
	hist *sql.DB
}

// OpenSQLite opens both databases read-only. Concurrent writes from main.exe
// are non-blocking: the ECU's WAL setup is owned by the writer.
//
// File names: the live DB is "database.db". The historical DB is "historical_data.db"
// on stock firmware 2.1.29D. We accept "historical.db" as well for flexibility.
func OpenSQLite(dir string) (*SQLiteReader, error) {
	live, err := openRO(filepath.Join(dir, "database.db"))
	if err != nil {
		return nil, fmt.Errorf("open database.db: %w", err)
	}
	hist, err := openFirstExisting(dir, "historical_data.db", "historical.db")
	if err != nil {
		live.Close()
		return nil, fmt.Errorf("open historical_data.db: %w", err)
	}
	return &SQLiteReader{live: live, hist: hist}, nil
}

func openFirstExisting(dir string, names ...string) (*sql.DB, error) {
	var lastErr error
	for _, n := range names {
		db, err := openRO(filepath.Join(dir, n))
		if err == nil {
			return db, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func openRO(path string) (*sql.DB, error) {
	// mode=ro opens the file read-only; we skip journal_mode=WAL because
	// changing it requires a writeable handle. Live DB on the ECU is already
	// in WAL via main.exe, so concurrent reads are non-blocking either way.
	q := url.Values{}
	q.Set("mode", "ro")
	dsn := fmt.Sprintf("file:%s?%s", path, q.Encode())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func (s *SQLiteReader) Close() error {
	var first error
	if err := s.live.Close(); err != nil {
		first = err
	}
	if err := s.hist.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

// LifetimeEnergyKWh returns the running lifetime energy counter from
// historical.db (updated each poll).
func (s *SQLiteReader) LifetimeEnergyKWh(ctx context.Context) (float64, error) {
	var v float64
	err := s.hist.QueryRowContext(ctx,
		"SELECT lifetime_energy FROM lifetime_energy WHERE item=1").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// TodayEnergyKWh returns daily_energy for the most recent date stored.
// (Date format on disk is yyyymmdd; we trust the latest row rather than
// time.Now() to avoid timezone drift between Pi and ECU.)
func (s *SQLiteReader) TodayEnergyKWh(ctx context.Context) (float64, error) {
	var v float64
	err := s.hist.QueryRowContext(ctx,
		"SELECT daily_energy FROM daily_energy ORDER BY date DESC LIMIT 1").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// MonthEnergyKWh returns this month's energy from the latest monthly_energy row.
func (s *SQLiteReader) MonthEnergyKWh(ctx context.Context) (float64, error) {
	var v float64
	err := s.hist.QueryRowContext(ctx,
		"SELECT monthly_energy FROM monthly_energy ORDER BY date DESC LIMIT 1").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// YearEnergyKWh returns this year's energy from the latest yearly_energy row.
func (s *SQLiteReader) YearEnergyKWh(ctx context.Context) (float64, error) {
	var v float64
	err := s.hist.QueryRowContext(ctx,
		"SELECT yearly_energy FROM yearly_energy ORDER BY date DESC LIMIT 1").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// PerInverterLimits returns the curtailment cap (limitedpower, W per panel)
// keyed by inverter UID.
func (s *SQLiteReader) PerInverterLimits(ctx context.Context) (map[string]int, error) {
	rows, err := s.live.QueryContext(ctx,
		"SELECT id, COALESCE(limitedpower,0) FROM power")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var uid string
		var w int
		if err := rows.Scan(&uid, &w); err != nil {
			return nil, err
		}
		out[uid] = w
	}
	return out, rows.Err()
}

// LatestSystemPowerW returns the most recent each_system_power sample (W).
// Resolution is whatever main.exe writes (≈5 min in normal-poll mode, 30 s in
// fast-poll mode).
func (s *SQLiteReader) LatestSystemPowerW(ctx context.Context) (int32, error) {
	var v float64
	err := s.hist.QueryRowContext(ctx,
		"SELECT each_system_power FROM each_system_power "+
			"ORDER BY date DESC, time DESC LIMIT 1").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int32(v + 0.5), nil
}

// InverterMeta from the id table.
type InverterMeta struct {
	UID         string
	Model       int
	SoftwareVer int
	Phase       int
	Bound       bool
}

// InverterList enumerates registered inverters.
func (s *SQLiteReader) InverterList(ctx context.Context) ([]InverterMeta, error) {
	rows, err := s.live.QueryContext(ctx,
		"SELECT id, COALESCE(model,0), COALESCE(software_version,0), "+
			"COALESCE(phase,0), COALESCE(bind_zigbee_flag,0) "+
			"FROM id WHERE id NOT LIKE '_1%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InverterMeta
	for rows.Next() {
		var m InverterMeta
		var bind int
		if err := rows.Scan(&m.UID, &m.Model, &m.SoftwareVer, &m.Phase, &bind); err != nil {
			return nil, err
		}
		m.Bound = bind == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// LatestEventBits returns the most recent Event-table row's `eve` bitstring
// for each inverter, parsed into the SunSpec bitfield slots:
//
//	[0] = Evt1   (bits 0..31  of the 86-char string)
//	[1] = Evt2   (bits 32..63)
//	[2] = EvtVnd1 (bits 64..85)
//	[3] = always 0 (no source bits to fill it)
//
// Bit 0 of slot N corresponds to character index N*32 of the bitstring; '1'
// in the string sets the bit, '0' clears it.
func (s *SQLiteReader) LatestEventBits(ctx context.Context) (map[string][4]uint32, error) {
	rows, err := s.live.QueryContext(ctx,
		"SELECT device, eve FROM Event "+
			"GROUP BY device HAVING rowid = MAX(rowid)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][4]uint32)
	for rows.Next() {
		var uid, eve string
		if err := rows.Scan(&uid, &eve); err != nil {
			return nil, err
		}
		out[uid] = parseEventBits(eve)
	}
	return out, rows.Err()
}

// parseEventBits packs an APsystems Event bitstring into 4×uint32 slots.
// LSB-first within each slot — character index 0 → bit 0 of slot 0.
func parseEventBits(eve string) [4]uint32 {
	var out [4]uint32
	for i, c := range eve {
		if i >= 128 {
			break
		}
		if c == '1' {
			slot := i / 32
			bit := i % 32
			out[slot] |= 1 << bit
		}
	}
	return out
}

// SignalStrengths returns the latest RSSI (0..255) keyed by inverter UID.
func (s *SQLiteReader) SignalStrengths(ctx context.Context) (map[string]int, error) {
	rows, err := s.live.QueryContext(ctx,
		"SELECT id, COALESCE(signal_strength,0) FROM signal_strength")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var uid string
		var rssi int
		if err := rows.Scan(&uid, &rssi); err != nil {
			return nil, err
		}
		out[uid] = rssi
	}
	return out, rows.Err()
}
