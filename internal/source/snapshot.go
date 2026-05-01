package source

import "time"

// AggregateEventBits returns the bitwise-OR of every inverter's EventBits.
// Used by the aggregate SunSpec bank's Inverter Model so an alarm on any one
// microinverter shows up at the system level.
func (s Snapshot) AggregateEventBits() [4]uint32 {
	var out [4]uint32
	for _, inv := range s.Inverters {
		for i := range out {
			out[i] |= inv.EventBits[i]
		}
	}
	return out
}

// Snapshot is the unified view of ECU state used by the SunSpec encoder.
// All values are best-effort: missing fields are zero, freshness varies per source.
type Snapshot struct {
	Captured time.Time

	ECUID           string
	Firmware        string
	Model           string
	PollingInterval int // seconds; 0 = unknown / default

	SystemPowerW        int32   // each_system_power latest sample
	LifetimeEnergyWh    uint64  // lifetime_energy * 1000
	TodayEnergyWh       uint64  // daily_energy of today * 1000
	MonthEnergyWh       uint64  // monthly_energy of current month * 1000
	YearEnergyWh        uint64  // yearly_energy of current year * 1000
	GridFrequencyHz     float64 // averaged over online inverters; 0 if none
	GridVoltageV        float64 // averaged over inverters' first AC-voltage column; 0 if none
	MaxTemperatureC     int     // max(per-inverter raw temp - 100)
	InverterCount       int
	InverterOnlineCount int
	Inverters           []Inverter
}

// TotalNameplateW returns the sum of per-inverter rated AC output, regardless
// of online state. Used for SunSpec Model 120 (Nameplate Ratings).
func (s Snapshot) TotalNameplateW() int {
	sum := 0
	for _, inv := range s.Inverters {
		sum += inv.NameplateW()
	}
	return sum
}

// TotalPanelCount returns the number of PV input channels across all online
// inverters. Used to size SunSpec Model 160 (per-panel granularity).
func (s Snapshot) TotalPanelCount() int {
	n := 0
	for _, inv := range s.Inverters {
		if inv.Online {
			n += inv.PanelCount()
		}
	}
	return n
}

// TotalLimitedW returns the sum of per-panel curtailment caps × panel count.
// Used for SunSpec Model 121 (Basic Settings).
func (s Snapshot) TotalLimitedW() int {
	sum := 0
	for _, inv := range s.Inverters {
		sum += inv.LimitedPowerW * inv.PanelCount()
	}
	return sum
}

// Inverter holds per-inverter values parsed from /tmp/parameters_app.conf
// plus any joined SQLite metadata.
//
// Columns 5+ in the params file are inverter-type-dependent. The first AC
// voltage and the (estimated) AC power are extracted heuristically; the full
// raw column slice is preserved on RawTail for downstream decoders.
type Inverter struct {
	UID            string
	Online         bool
	TypeCode       string  // "01"=DS3 (2-channel), "03"=DS3-L variant on this firmware, "04"=DS3-H...
	FrequencyHz    float64 // params col 3
	TemperatureC   int     // params col 4 - 100
	ACVoltageV     int     // best-guess phase voltage (params col 6 for type 01, col 6 for type 03)
	ACPowerW       int     // best-guess phase power (sum of channel powers when knowable)
	SignalStrength int     // 0..255 from signal_strength table
	Phase          int     // from id.phase (1/2/3 or 0)
	Model          int     // from id.model
	SoftwareVer    int     // from id.software_version
	LimitedPowerW  int     // per-panel curtailment cap from power.limitedpower (W)
	RawTail        []int   // params columns 4..N as integers

	// EventBits holds the 86-bit event bitstring from database.db.Event,
	// packed LSB-first into uint32 slots: [Evt1, Evt2, EvtVnd1, EvtVnd2].
	// The semantics of each bit are not publicly documented by APsystems —
	// we surface the raw bits and let downstream consumers decode them.
	EventBits [4]uint32
}

// PanelCount is the number of PV input channels per the inverter type. We
// avoid hardcoding the fleet shape — every count derives from this single
// switch keyed on the wire-protocol type code.
func (inv Inverter) PanelCount() int {
	switch inv.TypeCode {
	case "01", "04":
		return 2 // DS3, DS3-H/DS3D-L
	case "03":
		return 4 // DS3-L variant on this firmware (4 PV inputs)
	}
	return 0
}

// PanelPowers returns one watts value per panel; len(result)==PanelCount().
// Returns nil when offline or RawTail is too short.
//
// Layouts (RawTail starts at temp_raw):
//
//	type 01/04: [tmp, P0, V_ac, P1, V_ac]
//	type 03   : [tmp, P0, V_ac, P1, P2, P3]
func (inv Inverter) PanelPowers() []int {
	if !inv.Online {
		return nil
	}
	switch inv.TypeCode {
	case "01", "04":
		if len(inv.RawTail) >= 5 {
			return []int{inv.RawTail[1], inv.RawTail[3]}
		}
	case "03":
		if len(inv.RawTail) >= 6 {
			return []int{inv.RawTail[1], inv.RawTail[3], inv.RawTail[4], inv.RawTail[5]}
		}
	}
	return nil
}

// NameplateW is the rated AC output watts for this inverter type.
//
// Reference: APsystems datasheets — DS3 730W, DS3-L 1460W (2x730W),
// DS3-H/DS3D-L 880W. Values are conservative typicals; vendor model 64xxx
// can override per-region (NA/EU/SAA) if needed.
func (inv Inverter) NameplateW() int {
	switch inv.TypeCode {
	case "01":
		return 730
	case "03":
		return 1460
	case "04":
		return 880
	}
	return 0
}
