package source

import (
	"bufio"
	"os"
	"testing"
	"time"
)

// testdata/aps18/qt2.txt holds 144 consecutive APS18 records main.exe
// queued in record.db for a single QT2 (2026-11-07 08:20 → 2026-11-08
// 11:55), one per line.
func loadAPS18Fixture(t *testing.T) []string {
	t.Helper()
	f, err := os.Open("../../testdata/aps18/qt2.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text()+"\n")
	}
	if len(out) != 144 {
		t.Fatalf("fixture has %d records, want 144", len(out))
	}
	return out
}

// Parse then Format must reproduce every real record byte for byte.
func TestAPS18_RoundTrip(t *testing.T) {
	for _, rec := range loadAPS18Fixture(t) {
		r, err := ParseAPS18(rec)
		if err != nil {
			t.Fatalf("parse %q: %v", rec[:74], err)
		}
		if got := r.Format(); got != rec {
			t.Fatalf("round trip mismatch\n got %q\nwant %q", got, rec)
		}
	}
}

func TestAPS18_Fields(t *testing.T) {
	r, err := ParseAPS18("APS18AA293AAAAAAA121500005467700000309000000024744000001086420261108112000001100000END901500081373160049911810037502300082480005838007300399000000000000000200375024000824800061990077004000000000000000003003750240008248000624700780040200000000000000040037502400064590081000759129750560300000END\n")
	if err != nil {
		t.Fatal(err)
	}
	if r.ECUID != "215000054677" || r.PowerCentiW != 30900 || r.EnergyMilliWh != 24744 || r.LifetimeDecikWh != 10864 {
		t.Errorf("header: %+v", r)
	}
	if want := time.Date(2026, 11, 8, 11, 20, 0, 0, time.Local); !r.Time.Equal(want) {
		t.Errorf("time %v want %v", r.Time, want)
	}
	if len(r.Inverters) != 1 {
		t.Fatalf("inverters=%d", len(r.Inverters))
	}
	inv := r.Inverters[0]
	if inv.UID != "901500081373" || inv.FreqDeciHz != 499 || inv.TempRaw != 118 {
		t.Errorf("inverter: %+v", inv)
	}
	c := inv.Channels
	if c[0].DCDeciV != 375 || c[0].DCDeciA != 23 || c[0].PhaseEnergyMilliWh != 8248 ||
		c[0].EnergyMilliWh != 5838 || c[0].PowerW != 73 || c[0].LineV != 399 {
		t.Errorf("channel 1: %+v", c[0])
	}
	if c[3].DCDeciA != 24 || c[3].EnergyMilliWh != 6459 || c[3].PowerW != 81 {
		t.Errorf("channel 4: %+v", c[3])
	}
	if inv.Unknown760 != 759 || inv.RunSeconds != 12975 || inv.EnergyTodayDeciWh != 5603 {
		t.Errorf("counters: %+v", inv)
	}
}

func TestAPS18_RejectsOtherLayouts(t *testing.T) {
	for _, bad := range []string{
		"", "APS18AA293AAAAAAA1\n",
		`{"VER":"M0001","A":"215000054677"}`,
		// Length field doesn't match.
		"APS18AA294AAAAAAA121500005467700000309000000024744000001086420261108112000001100000END901500081373160049911810037502300082480005838007300399000000000000000200375024000824800061990077004000000000000000003003750240008248000624700780040200000000000000040037502400064590081000759129750560300000END\n",
	} {
		if _, err := ParseAPS18(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
