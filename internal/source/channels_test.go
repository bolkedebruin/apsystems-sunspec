package source

import (
	"strings"
	"testing"
)

// /tmp/parameters.conf as written by main.exe on ECU-C fw C1.2.27A for a
// QT2: one row per channel, column 3 is what the ECU web UI labels
// "DC Voltage".
func TestParseChannelDC(t *testing.T) {
	in := strings.NewReader(`901500081373-1, 154, 37, 50.0, AB: 407, 28, 2026-11-01 15:30:00
901500081373-2, 159, 37, 50.0, BC: 407, 28, 2026-11-01 15:30:00
901500081373-3, 151, 36, 50.0, CA: 408, 28, 2026-11-01 15:30:00
901500081373-4, 153, 38, 50.0, -, 28, 2026-11-01 15:30:00
999900000001-1, 120, -, 50.0, 230, 30, 2026-11-01 15:30:00
`)
	got := ParseChannelDC(in)
	if want := []int{37, 37, 36, 38}; !equalInts(got["901500081373"], want) {
		t.Errorf("QT2 DC=%v want %v", got["901500081373"], want)
	}
	// "-" means the ECU has no DC voltage for that channel.
	if want := []int{0}; !equalInts(got["999900000001"], want) {
		t.Errorf("DS3 DC=%v want %v", got["999900000001"], want)
	}
}

func TestInverter_PanelDCV(t *testing.T) {
	inv := Inverter{PanelDCVoltageV: []int{37, 0}}
	if got := inv.PanelDCV(0); got != 37 {
		t.Errorf("PanelDCV(0)=%d want 37", got)
	}
	if got := inv.PanelDCV(1); got != 0 {
		t.Errorf("PanelDCV(1)=%d want 0 (unknown)", got)
	}
	if got := inv.PanelDCV(3); got != 0 {
		t.Errorf("PanelDCV(3)=%d want 0 (out of range)", got)
	}
}
