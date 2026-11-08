package source

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// APS18 is one inverter production record as main.exe queues it in
// record.db for upload to EMA (see docs/SERVICE_MODE.md). The layout is
// fixed-width zero-padded decimals; it was decoded from a single QT2
// (4 channels, channels 1–3 one per phase), the only inverter layout
// supported here.
type APS18 struct {
	ECUID           string
	PowerCentiW     int64 // system power, W × 100
	EnergyMilliWh   int64 // energy of the 5-minute interval, Wh × 1000
	LifetimeDecikWh int64 // ECU lifetime energy, kWh × 10
	Time            time.Time
	HeaderTail      string // 9 characters, constant ("001100000") on the decoded site
	Inverters       []APS18Inverter
}

// APS18Inverter is one inverter block.
type APS18Inverter struct {
	UID        string
	Code       string // 2 characters, constant ("16") for the QT2
	FreqDeciHz int
	TempRaw    int // °C + 100
	Channels   [4]APS18Channel

	// Inverter-level counters carried by channel 4.
	Unknown760        int // 6 digits; ~760 while producing
	RunSeconds        int // running time today
	EnergyTodayDeciWh int // inverter energy today, Wh × 10
}

// APS18Channel is one DC input. PhaseEnergyMilliWh and LineV only exist
// on channels 1–3.
type APS18Channel struct {
	DCDeciV            int
	DCDeciA            int // sampled at the cycle, not averaged
	PhaseEnergyMilliWh int
	EnergyMilliWh      int
	PowerW             int
	LineV              int
}

const (
	aps18Prefix        = "APS18AA"
	aps18HeaderConst   = "AAAAAAA1"
	aps18HeaderLen     = 83  // up to the first "END"
	aps18InverterLen   = 204 // one inverter block
	aps18PhaseChLen    = 47
	aps18TimeLayout    = "20060102150405"
	aps18PhaseChZeros  = "000000000000000"
	aps18FourthChZeros = "00000"
)

// ParseAPS18 decodes a record, including its trailing newline.
func ParseAPS18(s string) (APS18, error) {
	var r APS18
	line, ok := strings.CutSuffix(s, "\n")
	if !ok || !strings.HasPrefix(line, aps18Prefix) || !strings.HasSuffix(line, "END") {
		return r, errors.New("aps18: not an APS18 record")
	}
	if len(line) < aps18HeaderLen+3+aps18InverterLen+3 {
		return r, fmt.Errorf("aps18: record too short (%d)", len(line))
	}
	if n, err := strconv.Atoi(line[7:10]); err != nil || n != len(line) {
		return r, fmt.Errorf("aps18: length field %q, record is %d", line[7:10], len(line))
	}
	h := line[:aps18HeaderLen]
	if h[10:18] != aps18HeaderConst || line[aps18HeaderLen:aps18HeaderLen+3] != "END" {
		return r, errors.New("aps18: unexpected header layout")
	}
	p := &digits{s: h, off: 18}
	r.ECUID = p.str(12)
	r.PowerCentiW = int64(p.num(10))
	r.EnergyMilliWh = int64(p.num(10))
	r.LifetimeDecikWh = int64(p.num(10))
	ts := p.str(14)
	r.HeaderTail = p.str(9)
	if p.err != nil {
		return r, p.err
	}
	t, err := time.ParseInLocation(aps18TimeLayout, ts, time.Local)
	if err != nil {
		return r, fmt.Errorf("aps18: time %q: %w", ts, err)
	}
	r.Time = t

	body := line[aps18HeaderLen+3 : len(line)-3]
	if len(body)%aps18InverterLen != 0 {
		return r, fmt.Errorf("aps18: inverter section is %d characters, not a multiple of %d", len(body), aps18InverterLen)
	}
	for off := 0; off < len(body); off += aps18InverterLen {
		inv, err := parseAPS18Inverter(body[off : off+aps18InverterLen])
		if err != nil {
			return r, err
		}
		r.Inverters = append(r.Inverters, inv)
	}
	return r, nil
}

func parseAPS18Inverter(b string) (APS18Inverter, error) {
	var inv APS18Inverter
	p := &digits{s: b}
	inv.UID = p.str(12)
	inv.Code = p.str(2)
	inv.FreqDeciHz = p.num(5)
	inv.TempRaw = p.num(3)
	for i := 0; i < 4; i++ {
		if n := p.num(1); n != i+1 {
			return inv, fmt.Errorf("aps18: channel %d numbered %d", i+1, n)
		}
		c := &inv.Channels[i]
		c.DCDeciV = p.num(5)
		c.DCDeciA = p.num(3)
		if i < 3 {
			c.PhaseEnergyMilliWh = p.num(7)
		}
		c.EnergyMilliWh = p.num(7)
		c.PowerW = p.num(4)
		if i < 3 {
			c.LineV = p.num(5)
			if z := p.str(15); z != aps18PhaseChZeros {
				return inv, fmt.Errorf("aps18: channel %d trailer %q", i+1, z)
			}
		}
	}
	inv.Unknown760 = p.num(6)
	inv.RunSeconds = p.num(5)
	inv.EnergyTodayDeciWh = p.num(5)
	if z := p.str(5); z != aps18FourthChZeros {
		return inv, fmt.Errorf("aps18: channel 4 trailer %q", z)
	}
	return inv, p.err
}

// Format encodes the record exactly as main.exe does, trailing newline
// included.
func (r APS18) Format() string {
	header := fmt.Sprintf("%s%s%010d%010d%010d%s%s", aps18HeaderConst, r.ECUID,
		r.PowerCentiW, r.EnergyMilliWh, r.LifetimeDecikWh, r.Time.Format(aps18TimeLayout), r.HeaderTail)
	var b strings.Builder
	for _, inv := range r.Inverters {
		fmt.Fprintf(&b, "%s%s%05d%03d", inv.UID, inv.Code, inv.FreqDeciHz, inv.TempRaw)
		for i, c := range inv.Channels {
			if i < 3 {
				fmt.Fprintf(&b, "%d%05d%03d%07d%07d%04d%05d%s", i+1, c.DCDeciV, c.DCDeciA,
					c.PhaseEnergyMilliWh, c.EnergyMilliWh, c.PowerW, c.LineV, aps18PhaseChZeros)
				continue
			}
			fmt.Fprintf(&b, "%d%05d%03d%07d%04d%06d%05d%05d%s", i+1, c.DCDeciV, c.DCDeciA,
				c.EnergyMilliWh, c.PowerW, inv.Unknown760, inv.RunSeconds, inv.EnergyTodayDeciWh, aps18FourthChZeros)
		}
	}
	// "APS18AA" + length + header + "END" + inverters + "END"; the length
	// counts the whole line without its newline.
	rest := header + "END" + b.String() + "END"
	return fmt.Sprintf("%s%03d%s\n", aps18Prefix, len(aps18Prefix)+3+len(rest), rest)
}

// digits reads fixed-width fields from s, remembering the first error.
type digits struct {
	s   string
	off int
	err error
}

func (d *digits) str(n int) string {
	if d.err != nil {
		return ""
	}
	if d.off+n > len(d.s) {
		d.err = fmt.Errorf("aps18: field at %d overruns record", d.off)
		return ""
	}
	v := d.s[d.off : d.off+n]
	d.off += n
	return v
}

func (d *digits) num(n int) int {
	v := d.str(n)
	if d.err != nil {
		return 0
	}
	x, err := strconv.Atoi(v)
	if err != nil {
		d.err = fmt.Errorf("aps18: %q at %d is not a number", v, d.off-n)
	}
	return x
}
