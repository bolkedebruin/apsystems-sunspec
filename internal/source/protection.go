package source

import (
	"context"
	"database/sql"
	"strings"
)

// ProtectionParams holds the per-inverter active grid-protection thresholds
// loaded from the protection_parameters60code table.
//
// The 2-letter codes match protection_parameters60_info.parameter_code and
// MAIN_PROTOCOL.md §4.4. Values are in their native units:
//
//	V codes (AB, AC, AD, AH, AI, AQ, AY, BN, BO):  volts
//	Hz codes (AE, AF, AJ, AK, BP, BQ):             hertz
//	Time codes (AG=ReconnectS, AS=StartS,
//	            BB..BK clearance times):           seconds
//	CH (PFMode):                                   firmware-defined enum
//
// Has reports which codes the inverter firmware actually populated. Some
// firmware revisions / regulatory profiles leave cells NULL; consumers should
// emit "not implemented" sentinels for those.
type ProtectionParams struct {
	UVStg2  float64 // AC — under-voltage stage 2 (slow trip threshold)
	UVFast  float64 // AQ — under-voltage fast (deeper trip threshold)
	OVStg2  float64 // AD — over-voltage stage 2 (slow trip threshold)
	OVStg3  float64 // AY — over-voltage stage 3 (deeper trip threshold)
	AvgOV   float64 // AB — averaged over-voltage (10-min window)
	VWinLow float64 // AH — voltage window low bound
	VWinHi  float64 // AI — voltage window high bound

	UFSlow float64 // AE — under-frequency stage 2
	OFSlow float64 // AF — over-frequency stage 2
	UFFast float64 // AJ — under-frequency fast
	OFFast float64 // AK — over-frequency fast

	ReconnectS float64 // AG — grid recovery time
	StartS     float64 // AS — start time

	UV2ClrS float64 // BB — UV stage 2 trip clearance
	OV2ClrS float64 // BC — OV stage 2 trip clearance
	UV3ClrS float64 // BD — UV stage 3 / fast clearance
	OV3ClrS float64 // BE — OV stage 3 clearance
	UF1ClrS float64 // BH — UF fast clearance
	OF1ClrS float64 // BI — OF fast clearance
	UF2ClrS float64 // BJ — UF stage 2 clearance
	OF2ClrS float64 // BK — OF stage 2 clearance

	ReconnVLow float64 // BN
	ReconnVHi  float64 // BO
	ReconnFLow float64 // BP
	ReconnFHi  float64 // BQ

	PFMode float64 // CH — fixed power factor mode (enum, not numeric PF)

	// Frequency-Watt droop curve (over-frequency side). Maps the APsystems
	// Over_frequency_Watt_* settings exposed through 2-letter codes
	// observed in the gridProfile JSON and protection_parameters60code:
	//
	//	DC = OFDroopStart  (Over_frequency_Watt_Start_set, e.g. 50.2 Hz)
	//	CC = OFDroopEnd    (Over_frequency_Watt_High_set, e.g. 52.0 Hz — output → 0 here)
	//	DD = OFDroopSlope  (Over_Frequency_Watt_Slope_set, %P/Hz)
	//	CV = OFDroopMode   (Over_frequency_Watt_set; enum: 13 = AS/NZS,
	//	                    14 = other regions, 15 = disabled, others region-specific)
	//
	// These map directly to SunSpec Model 711 (DERFreqDroop) Ctl[].DbOf,
	// the curve endpoint, KOf slope, and Ena.
	OFDroopStart float64 // DC
	OFDroopEnd   float64 // CC
	OFDroopSlope float64 // DD
	OFDroopMode  float64 // CV

	Has map[string]bool
}

// PerInverterProtection returns active grid-protection thresholds keyed by
// inverter UID. Reads from protection_parameters60code, which main.exe fills
// after a successful "get protection params" cycle. Returns an empty map
// (no error) if the table is missing or empty — older firmwares may not
// populate it.
func (s *SQLiteReader) PerInverterProtection(ctx context.Context) (map[string]ProtectionParams, error) {
	const q = `SELECT id,
	  AB, AC, AD, AE, AF,
	  AG, AH, AI, AJ, AK,
	  AQ, "AS", AY,
	  BB, BC, BD, BE,
	  BH, BI, BJ, BK,
	  BN, BO, BP, BQ,
	  CH,
	  CC, CV, DC, DD
	FROM protection_parameters60code`
	rows, err := s.live.QueryContext(ctx, q)
	if err != nil {
		// Table missing → not an error condition; older firmware.
		if strings.Contains(err.Error(), "no such table") {
			return map[string]ProtectionParams{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]ProtectionParams)
	for rows.Next() {
		var (
			uid string
			ab, ac, ad, ae, af                 sql.NullFloat64
			ag, ah, ai, aj, ak                 sql.NullFloat64
			aq, asStart, ay                    sql.NullFloat64
			bb, bc, bd, be                     sql.NullFloat64
			bh, bi, bj, bk                     sql.NullFloat64
			bn, bo, bp, bq                     sql.NullFloat64
			ch                                 sql.NullFloat64
			cc, cv, dc, dd                     sql.NullFloat64
		)
		if err := rows.Scan(&uid,
			&ab, &ac, &ad, &ae, &af,
			&ag, &ah, &ai, &aj, &ak,
			&aq, &asStart, &ay,
			&bb, &bc, &bd, &be,
			&bh, &bi, &bj, &bk,
			&bn, &bo, &bp, &bq,
			&ch,
			&cc, &cv, &dc, &dd,
		); err != nil {
			return nil, err
		}
		p := ProtectionParams{Has: make(map[string]bool, 26)}
		set := func(code string, v sql.NullFloat64, dst *float64) {
			if v.Valid {
				*dst = v.Float64
				p.Has[code] = true
			}
		}
		set("AB", ab, &p.AvgOV)
		set("AC", ac, &p.UVStg2)
		set("AD", ad, &p.OVStg2)
		set("AE", ae, &p.UFSlow)
		set("AF", af, &p.OFSlow)
		set("AG", ag, &p.ReconnectS)
		set("AH", ah, &p.VWinLow)
		set("AI", ai, &p.VWinHi)
		set("AJ", aj, &p.UFFast)
		set("AK", ak, &p.OFFast)
		set("AQ", aq, &p.UVFast)
		set("AS", asStart, &p.StartS)
		set("AY", ay, &p.OVStg3)
		set("BB", bb, &p.UV2ClrS)
		set("BC", bc, &p.OV2ClrS)
		set("BD", bd, &p.UV3ClrS)
		set("BE", be, &p.OV3ClrS)
		set("BH", bh, &p.UF1ClrS)
		set("BI", bi, &p.OF1ClrS)
		set("BJ", bj, &p.UF2ClrS)
		set("BK", bk, &p.OF2ClrS)
		set("BN", bn, &p.ReconnVLow)
		set("BO", bo, &p.ReconnVHi)
		set("BP", bp, &p.ReconnFLow)
		set("BQ", bq, &p.ReconnFHi)
		set("CH", ch, &p.PFMode)
		set("CC", cc, &p.OFDroopEnd)
		set("CV", cv, &p.OFDroopMode)
		set("DC", dc, &p.OFDroopStart)
		set("DD", dd, &p.OFDroopSlope)
		out[uid] = p
	}
	return out, rows.Err()
}
