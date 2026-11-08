package source

import (
	"fmt"
	"math"
	"time"
)

// APS18Input carries what the adapter measured for one 5-minute interval
// ending at Time.
type APS18Input struct {
	Time        time.Time
	LifetimeKWh float64 // ECU lifetime energy
	Inverters   []APS18InverterInput
}

// APS18InverterInput is one inverter's share of an APS18Input.
type APS18InverterInput struct {
	UID      string
	FreqHz   float64
	TempC    int
	LineV    [3]int // AB, BC, CA
	Channels [4]APS18ChannelInput
}

// APS18ChannelInput is one DC input: the last power sample, the energy
// integrated over the interval, and the DC voltage / current at the last
// sample.
type APS18ChannelInput struct {
	PowerW   int
	EnergyWh float64
	DCV      float64
	DCA      float64
}

// NextAPS18 builds the record main.exe would have queued for in.Time,
// using prev (the record of the previous interval, normally main.exe's
// own) as template for what the adapter can't measure: the constant
// header/inverter codes, the unknown channel-4 field, and the inverter's
// running-time and energy-today counters, which are advanced from prev.
//
// Arithmetic follows main.exe: system power is the channel sum × 100,
// channel energies are truncated while the interval total is computed
// before truncation, and each phase gets a third of the total.
func NextAPS18(prev APS18, in APS18Input) (APS18, error) {
	if len(prev.Inverters) != len(in.Inverters) {
		return APS18{}, fmt.Errorf("aps18: template has %d inverters, input %d", len(prev.Inverters), len(in.Inverters))
	}
	out := APS18{
		ECUID:           prev.ECUID,
		Time:            in.Time,
		LifetimeDecikWh: int64(math.Floor(in.LifetimeKWh*10 + 1e-9)),
		HeaderTail:      prev.HeaderTail,
	}
	elapsed := in.Time.Sub(prev.Time)
	sameDay := in.Time.YearDay() == prev.Time.YearDay() && in.Time.Year() == prev.Time.Year()

	var sysW int64
	var sysWh float64
	for i, ii := range in.Inverters {
		p := prev.Inverters[i]
		if ii.UID != p.UID {
			return APS18{}, fmt.Errorf("aps18: inverter %d is %s in the template, %s in the input", i, p.UID, ii.UID)
		}
		inv := APS18Inverter{
			UID:        p.UID,
			Code:       p.Code,
			FreqDeciHz: int(math.Round(ii.FreqHz * 10)),
			TempRaw:    ii.TempC + 100,
			Unknown760: p.Unknown760,
		}
		var invWh float64
		for _, c := range ii.Channels {
			invWh += c.EnergyWh
		}
		phaseMilliWh := int(math.Floor(invWh * 1000 / 3))
		for c, ci := range ii.Channels {
			ch := APS18Channel{
				DCDeciV:       int(math.Round(ci.DCV * 10)),
				DCDeciA:       int(math.Round(ci.DCA * 10)),
				EnergyMilliWh: int(math.Floor(ci.EnergyWh*1000 + 1e-9)),
				PowerW:        ci.PowerW,
			}
			if c < 3 {
				ch.PhaseEnergyMilliWh = phaseMilliWh
				ch.LineV = ii.LineV[c]
			}
			inv.Channels[c] = ch
			sysW += int64(ci.PowerW)
		}
		if sameDay {
			inv.RunSeconds = p.RunSeconds + int(elapsed/time.Second)
			inv.EnergyTodayDeciWh = p.EnergyTodayDeciWh + int(math.Floor(invWh*10))
		} else {
			inv.RunSeconds = int(elapsed / time.Second)
			inv.EnergyTodayDeciWh = int(math.Floor(invWh * 10))
		}
		sysWh += invWh
		out.Inverters = append(out.Inverters, inv)
	}
	out.PowerCentiW = sysW * 100
	out.EnergyMilliWh = int64(math.Floor(sysWh*1000 + 1e-9))
	return out, nil
}
