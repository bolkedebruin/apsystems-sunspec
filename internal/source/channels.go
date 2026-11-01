package source

import (
	"bufio"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

// ParseChannelDC parses /tmp/parameters.conf, the per-channel file main.exe
// writes next to parameters_app.conf and the ECU web UI renders as its
// real-time data table:
//
//	<UID>-<channel>, <W>, <DC V>, <Hz>, <grid V>, <temp °C>, <timestamp>
//
// Only the DC voltage is used. The result maps UID to one value per
// channel (index channel-1); 0 means the ECU reported "-" or nothing.
func ParseChannelDC(r io.Reader) map[string][]int {
	out := map[string][]int{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Split(sc.Text(), ",")
		if len(f) < 3 {
			continue
		}
		dash := strings.LastIndex(f[0], "-")
		if dash < 0 {
			continue
		}
		uid := strings.TrimSpace(f[0][:dash])
		ch, err := strconv.Atoi(strings.TrimSpace(f[0][dash+1:]))
		if err != nil || ch < 1 || ch > 8 {
			continue
		}
		v := 0
		if x, err := strconv.ParseFloat(strings.TrimSpace(f[2]), 64); err == nil && x > 0 {
			v = int(math.Round(x))
		}
		vals := out[uid]
		for len(vals) < ch {
			vals = append(vals, 0)
		}
		vals[ch-1] = v
		out[uid] = vals
	}
	return out
}

// LoadChannelDCFile is a convenience helper. A missing or unreadable file
// yields nil: DC voltage is optional telemetry.
func LoadChannelDCFile(path string) map[string][]int {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	return ParseChannelDC(f)
}

// PanelDCV returns the DC input voltage of channel i, or 0 when unknown.
func (inv Inverter) PanelDCV(i int) int {
	if i < 0 || i >= len(inv.PanelDCVoltageV) {
		return 0
	}
	return inv.PanelDCVoltageV[i]
}
