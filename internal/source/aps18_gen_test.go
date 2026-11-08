package source

import (
	"testing"
	"time"
)

// inputFromRecord rebuilds the generator input a perfect sampler would
// have produced for record r: the same powers, DC values, line voltages
// and channel energies.
func inputFromRecord(r APS18) APS18Input {
	in := APS18Input{Time: r.Time, LifetimeKWh: float64(r.LifetimeDecikWh) / 10}
	for _, inv := range r.Inverters {
		ii := APS18InverterInput{
			UID:    inv.UID,
			FreqHz: float64(inv.FreqDeciHz) / 10,
			TempC:  inv.TempRaw - 100,
		}
		for i, c := range inv.Channels {
			if i < 3 {
				ii.LineV[i] = c.LineV
			}
			ii.Channels[i] = APS18ChannelInput{
				PowerW:   c.PowerW,
				EnergyWh: float64(c.EnergyMilliWh) / 1000,
				DCV:      float64(c.DCDeciV) / 10,
				DCA:      float64(c.DCDeciA) / 10,
			}
		}
		in.Inverters = append(in.Inverters, ii)
	}
	return in
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// For every pair of consecutive real records 5 minutes apart, generating
// the second from the first plus the second's measurements reproduces
// it: exactly for every derived field, within the measured tolerance for
// the counters the inverter keeps itself.
func TestNextAPS18_ReproducesMainExe(t *testing.T) {
	recs := loadAPS18Fixture(t)
	pairs, resets := 0, 0
	for i := 1; i < len(recs); i++ {
		prev, _ := ParseAPS18(recs[i-1])
		want, _ := ParseAPS18(recs[i])
		if want.Time.Sub(prev.Time) != 5*time.Minute || want.Time.YearDay() != prev.Time.YearDay() {
			continue
		}
		got, err := NextAPS18(prev, inputFromRecord(want))
		if err != nil {
			t.Fatalf("%v: %v", want.Time, err)
		}
		pairs++

		if got.ECUID != want.ECUID || got.HeaderTail != want.HeaderTail || !got.Time.Equal(want.Time) ||
			got.PowerCentiW != want.PowerCentiW || got.LifetimeDecikWh != want.LifetimeDecikWh {
			t.Errorf("%v header: got %+v want %+v", want.Time, got, want)
		}
		// Channel energies are truncated, the total isn't: main.exe's total
		// is 0–3 mWh above the sum of the truncated channels.
		if d := want.EnergyMilliWh - got.EnergyMilliWh; d < 0 || d > 3 {
			t.Errorf("%v energy: got %d want %d", want.Time, got.EnergyMilliWh, want.EnergyMilliWh)
		}
		gi, wi := got.Inverters[0], want.Inverters[0]
		if gi.UID != wi.UID || gi.Code != wi.Code || gi.FreqDeciHz != wi.FreqDeciHz || gi.TempRaw != wi.TempRaw {
			t.Errorf("%v inverter: got %+v want %+v", want.Time, gi, wi)
		}
		for c := 0; c < 4; c++ {
			g, w := gi.Channels[c], wi.Channels[c]
			// Phase energy: ±2 mWh because this test feeds already-truncated
			// channel energies back in; main.exe divides the exact total.
			if g.DCDeciV != w.DCDeciV || g.DCDeciA != w.DCDeciA || g.EnergyMilliWh != w.EnergyMilliWh ||
				g.PowerW != w.PowerW || g.LineV != w.LineV || abs(g.PhaseEnergyMilliWh-w.PhaseEnergyMilliWh) > 2 {
				t.Errorf("%v channel %d: got %+v want %+v", want.Time, c+1, g, w)
			}
		}
		// Counters the inverter keeps itself, extrapolated from the previous
		// record. At dawn and dusk the inverter starts and stops: it resets
		// them, or runs only part of the interval. That can't be predicted,
		// so those pairs are only counted.
		pi := prev.Inverters[0]
		if wi.RunSeconds < pi.RunSeconds || wi.EnergyTodayDeciWh < pi.EnergyTodayDeciWh ||
			wi.RunSeconds-pi.RunSeconds < 290 {
			resets++
		} else {
			// Running time: main.exe's cycles are 300 s ± ~10 s apart.
			if d := abs(gi.RunSeconds - wi.RunSeconds); d > 10 {
				t.Errorf("%v run seconds: got %d want %d", want.Time, gi.RunSeconds, wi.RunSeconds)
			}
			// Energy today: the inverter's counter drifts a few % from the
			// channel sum, up to ~5 Wh per interval at high power.
			if d := abs(gi.EnergyTodayDeciWh - wi.EnergyTodayDeciWh); d > 60 {
				t.Errorf("%v energy today: got %d want %d", want.Time, gi.EnergyTodayDeciWh, wi.EnergyTodayDeciWh)
			}
		}
		if gi.Unknown760 != prev.Inverters[0].Unknown760 {
			t.Errorf("%v unknown field should be carried over from the template", want.Time)
		}
		if _, err := ParseAPS18(got.Format()); err != nil {
			t.Errorf("%v: generated record does not parse: %v", want.Time, err)
		}
	}
	if pairs < 100 {
		t.Fatalf("only %d consecutive pairs checked", pairs)
	}
	// Dusk restart (17:35, 17:40) and dawn start (07:45) in the fixture.
	if resets > 4 {
		t.Errorf("%d counter discontinuities in %d pairs; expected only dawn/dusk", resets, pairs)
	}
}

func TestNextAPS18_RejectsInverterMismatch(t *testing.T) {
	prev, _ := ParseAPS18(loadAPS18Fixture(t)[0])
	in := inputFromRecord(prev)
	in.Time = prev.Time.Add(5 * time.Minute)
	in.Inverters[0].UID = "999999999999"
	if _, err := NextAPS18(prev, in); err == nil {
		t.Error("accepted an input for a different inverter")
	}
}
