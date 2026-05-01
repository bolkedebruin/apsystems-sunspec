package source

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// ParseParamsApp parses /tmp/parameters_app.conf which main.exe rewrites after
// each successful ZigBee poll cycle. Format observed on firmware 2.1.29D:
//
//	01,3,20260501093000                                  ; protocol_version, count, yyyymmddhhmmss
//	<UID>,<online>,<type>,<freq>,<temp_raw>,<col5..colN> ; one line per inverter
//
// Per-inverter telemetry semantics for columns 5+ depend on TypeCode and are
// only partially decoded — see the SunSpec layer for which fields are used.
func ParseParamsApp(r io.Reader) (header ParamsHeader, invs []Inverter, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if lineNo == 1 {
			header, err = parseHeader(fields)
			if err != nil {
				return header, nil, fmt.Errorf("line 1: %w", err)
			}
			continue
		}
		inv, err := parseInverterLine(fields)
		if err != nil {
			return header, nil, fmt.Errorf("line %d (%q): %w", lineNo, line, err)
		}
		invs = append(invs, inv)
	}
	if err := sc.Err(); err != nil {
		return header, nil, err
	}
	return header, invs, nil
}

// ParamsHeader is line 1 of parameters_app.conf.
type ParamsHeader struct {
	ProtocolVersion string
	InverterCount   int
	Timestamp       time.Time
}

func parseHeader(f []string) (ParamsHeader, error) {
	if len(f) < 3 {
		return ParamsHeader{}, errors.New("expected 3 header fields")
	}
	cnt, err := strconv.Atoi(f[1])
	if err != nil {
		return ParamsHeader{}, fmt.Errorf("count: %w", err)
	}
	ts, err := time.ParseInLocation("20060102150405", f[2], time.Local)
	if err != nil {
		return ParamsHeader{}, fmt.Errorf("timestamp: %w", err)
	}
	return ParamsHeader{ProtocolVersion: f[0], InverterCount: cnt, Timestamp: ts}, nil
}

func parseInverterLine(f []string) (Inverter, error) {
	if len(f) < 5 {
		return Inverter{}, fmt.Errorf("need >=5 fields, got %d", len(f))
	}
	online, err := strconv.Atoi(f[1])
	if err != nil {
		return Inverter{}, fmt.Errorf("online: %w", err)
	}
	freq, err := strconv.ParseFloat(f[3], 64)
	if err != nil {
		return Inverter{}, fmt.Errorf("freq: %w", err)
	}
	tempRaw, err := strconv.Atoi(f[4])
	if err != nil {
		return Inverter{}, fmt.Errorf("temp: %w", err)
	}

	tail := make([]int, 0, len(f)-4)
	for i := 4; i < len(f); i++ {
		v, err := strconv.Atoi(f[i])
		if err != nil {
			return Inverter{}, fmt.Errorf("col %d: %w", i, err)
		}
		tail = append(tail, v)
	}

	inv := Inverter{
		UID:          f[0],
		Online:       online != 0,
		TypeCode:     f[2],
		FrequencyHz:  freq,
		TemperatureC: tempRaw - 100,
		RawTail:      tail,
	}
	inv.ACVoltageV, inv.ACPowerW = guessACFromTail(inv.TypeCode, tail)
	return inv, nil
}

// guessACFromTail picks the AC voltage and total AC power from the
// per-inverter tail in /tmp/parameters_app.conf.
//
// Layouts (verified against historical_data.db.each_system_power at the same
// timestamp; tail starts at temp_raw):
//
//	type 01 (DS3 2-channel):  [tmp, P0, V_ac, P1, V_ac]                   5 cols
//	type 03 (DS3-L variant):  [tmp, P0, V_ac, P1, P2, P3]                 6 cols
//	type 04 (DS3-H/DS3D-L):   not yet sampled — falls through to type-01 layout
func guessACFromTail(typeCode string, tail []int) (vAC int, wAC int) {
	switch typeCode {
	case "01", "04":
		if len(tail) >= 5 {
			wAC = tail[1] + tail[3]
			vAC = tail[2]
		}
	case "03":
		if len(tail) >= 6 {
			wAC = tail[1] + tail[3] + tail[4] + tail[5]
			vAC = tail[2]
		}
	}
	return vAC, wAC
}

// LoadParamsFile is a convenience helper.
func LoadParamsFile(path string) (ParamsHeader, []Inverter, error) {
	f, err := os.Open(path)
	if err != nil {
		return ParamsHeader{}, nil, err
	}
	defer f.Close()
	return ParseParamsApp(f)
}
