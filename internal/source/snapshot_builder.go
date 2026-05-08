package source

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Builder composes a Snapshot from the on-box files and SQLite databases.
type Builder struct {
	DB         *SQLiteReader
	ParamsFile string
	YunengDir  string

	ECUID    string
	Firmware string
	Model    string
}

// NewBuilder helper that loads metadata from /etc/yuneng/*.conf if available.
// Missing files are tolerated — they're surfaced as empty strings.
func NewBuilder(db *SQLiteReader, paramsFile, yunengDir string) *Builder {
	b := &Builder{
		DB:         db,
		ParamsFile: paramsFile,
		ECUID:      readTrim(yunengDir, "ecuid.conf"),
		Firmware:   readTrim(yunengDir, "version.conf"),
		Model:      readTrim(yunengDir, "model.conf"),
		YunengDir:  yunengDir,
	}
	return b
}

func readTrim(dir, name string) string {
	if dir == "" {
		return ""
	}
	b, err := os.ReadFile(dir + "/" + name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readIntFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// Build produces a fresh snapshot. Errors from optional sources are not fatal:
// they log into the snapshot's ECUID-prefixed missing fields as zeros and the
// caller decides what to surface upstream.
func (b *Builder) Build(ctx context.Context) (Snapshot, error) {
	s := Snapshot{
		Captured: time.Now(),
		ECUID:    b.ECUID,
		Firmware: b.Firmware,
		Model:    b.Model,
	}
	if v := readTrim(b.YunengDir, "polling_interval.conf"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			s.PollingInterval = n
		}
	}

	// Authoritative fleet capacity, written by main.exe at startup as the
	// sum of per-inverter nameplate watts (the same number the EMA app
	// reports as "system capacity"). Read once per snapshot — main.exe
	// only rewrites this file when the inverter inventory changes.
	if v, err := readIntFile("/tmp/powerALL.conf"); err == nil && v > 0 {
		s.SystemMaxPowerW = int32(v)
	}

	// Energy aggregates from historical.db. Energy fields are monotonic
	// (or fixed-window monotonic) so freezing at sundown is correct;
	// SystemPowerW is recomputed below from live params telemetry, not
	// from each_system_power, because that table goes stale at night.
	if b.DB != nil {
		if kwh, err := b.DB.LifetimeEnergyKWh(ctx); err == nil {
			s.LifetimeEnergyWh = uint64(kwh*1000 + 0.5)
		}
		if kwh, err := b.DB.TodayEnergyKWh(ctx); err == nil {
			s.TodayEnergyWh = uint64(kwh*1000 + 0.5)
		}
		if kwh, err := b.DB.MonthEnergyKWh(ctx); err == nil {
			s.MonthEnergyWh = uint64(kwh*1000 + 0.5)
		}
		if kwh, err := b.DB.YearEnergyKWh(ctx); err == nil {
			s.YearEnergyWh = uint64(kwh*1000 + 0.5)
		}
	}

	// Per-inverter telemetry from /tmp/parameters_app.conf.
	hdr, invs, err := LoadParamsFile(b.ParamsFile)
	if err != nil {
		return s, fmt.Errorf("params: %w", err)
	}

	// Join SQLite metadata onto inverters when available.
	var sigByUID map[string]int
	var limByUID map[string]int
	var evByUID map[string][4]uint32
	var metaByUID map[string]InverterMeta
	if b.DB != nil {
		if m, err := b.DB.SignalStrengths(ctx); err == nil {
			sigByUID = m
		}
		if m, err := b.DB.PerInverterLimits(ctx); err == nil {
			limByUID = m
		}
		if m, err := b.DB.LatestEventBits(ctx); err == nil {
			evByUID = m
		}
		if list, err := b.DB.InverterList(ctx); err == nil {
			metaByUID = make(map[string]InverterMeta, len(list))
			for _, m := range list {
				metaByUID[m.UID] = m
			}
		}
		if m, err := b.DB.PerInverterProtection(ctx); err == nil {
			s.Protection = m
		}
	}

	var (
		freqSum float64
		freqN   int
		vSum    int
		vN      int
		tempMax int
		hasTemp bool
		online  int
	)
	for i := range invs {
		inv := &invs[i]
		if m, ok := metaByUID[inv.UID]; ok {
			inv.Model = m.Model
			inv.SoftwareVer = m.SoftwareVer
			inv.Phase = m.Phase
		}
		if rssi, ok := sigByUID[inv.UID]; ok {
			inv.SignalStrength = rssi
		}
		if w, ok := limByUID[inv.UID]; ok {
			inv.LimitedPowerW = w
		}
		if bits, ok := evByUID[inv.UID]; ok {
			inv.EventBits = bits
		}
		if inv.Online {
			online++
			if inv.FrequencyHz > 0 {
				freqSum += inv.FrequencyHz
				freqN++
			}
			if inv.ACVoltageV > 0 {
				vSum += inv.ACVoltageV
				vN++
			}
			if !hasTemp || inv.TemperatureC > tempMax {
				tempMax = inv.TemperatureC
				hasTemp = true
			}
		}
	}

	s.Inverters = invs
	s.InverterCount = hdr.InverterCount
	if s.InverterCount == 0 {
		s.InverterCount = len(invs)
	}
	s.InverterOnlineCount = online
	if freqN > 0 {
		s.GridFrequencyHz = freqSum / float64(freqN)
	}
	if vN > 0 {
		s.GridVoltageV = float64(vSum) / float64(vN)
	}
	if hasTemp {
		s.MaxTemperatureC = tempMax
	}

	// SystemPowerW is the live sum of online inverters' AC power. This
	// is the ONLY source for the field — we do not consult
	// each_system_power, because main.exe stops appending rows when the
	// inverters go quiet, so its latest row freezes at the pre-sunset
	// value. The pre-fix builder used `each_system_power` as the
	// default and only overrode when the live sum was non-zero, which
	// meant a dusk window (online=N, every inverter producing 0 W) kept
	// the stale row's reading and the encoder reported phantom StMPPT
	// + phantom watts to Victron well past sundown. Using the live sum
	// even when it's zero fixes the state and the W field together.
	s.SystemPowerW = liveSystemPowerW(invs)

	return s, nil
}

// liveSystemPowerW returns the current sum of online inverters' AC
// power, used as the authoritative source for Snapshot.SystemPowerW.
// Offline inverters never contribute, regardless of any stale
// ACPowerW reading their last params row may carry.
func liveSystemPowerW(invs []Inverter) int32 {
	var sum int
	for _, inv := range invs {
		if inv.Online {
			sum += inv.ACPowerW
		}
	}
	return int32(sum)
}
