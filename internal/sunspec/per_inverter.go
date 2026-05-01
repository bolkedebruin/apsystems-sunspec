package sunspec

import (
	"strconv"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// EncodePerInverter produces a self-contained SunSpec bank for a single
// microinverter. Layout is the SunSpec equivalent of "one Fronius behind the
// Datalogger" — Common Model identifies the microinverter, the Inverter Model
// (101, single-phase) exposes its electrical state, and Multi-MPPT (160) lists
// its panels.
//
// The bank is intended to be served at a per-inverter Modbus unit ID, so a
// SunSpec scanner walking unit IDs 2..N+1 sees N microinverters as separate
// devices.
func EncodePerInverter(inv source.Inverter, ecuid string, unitID uint16, opt Options) Bank {
	if opt.Manufacturer == "" {
		opt.Manufacturer = DefaultManufacturer
	}

	bank := Bank{Regs: make([]uint16, 0, 256), Base: BaseRegister}

	bank.put16(0x5375, 0x6E53) // SunS

	// --- Common Model ---
	bank.put16(CommonModelID, CommonModelBodyLen)
	bank.putString(opt.Manufacturer, 16)
	bank.putString(inverterModelLabel(inv), 16)
	bank.putString("", 8)                              // Opt
	bank.putString(strconv.Itoa(inv.SoftwareVer), 8)   // Vr
	bank.putString(inv.UID, 16)                        // SN = inverter UID
	bank.put16(unitID)                                 // DA — Modbus unit address
	bank.put16(0)                                      // pad

	// --- Inverter Model 101 (single-phase) ---
	bank.put16(InverterModelSinglePhase, InverterModelBodyLen)

	// A / per-phase A — derive from W/V; 0.1 A units (ASF=-1)
	var a uint16
	if inv.ACVoltageV > 0 && inv.ACPowerW >= 0 {
		amps10 := float64(inv.ACPowerW) / float64(inv.ACVoltageV) * 10
		if amps10 < 65535 {
			a = uint16(amps10 + 0.5)
		}
	}
	bank.put16(a)
	bank.put16(a, notImplU16, notImplU16) // AphA, AphB, AphC
	bank.put16(scaleFactor(-1))           // ASF

	// PPVphAB/BC/CA — line-line voltages, not measured
	bank.put16(notImplU16, notImplU16, notImplU16)

	// PhVphA — line-neutral, B/C unimplemented for single-phase
	v := uint16(0)
	if inv.ACVoltageV > 0 && inv.ACVoltageV < 65535 {
		v = uint16(inv.ACVoltageV)
	}
	bank.put16(v, notImplU16, notImplU16)
	bank.put16(scaleFactor(0)) // VSF

	// W / WSF
	w := int32(inv.ACPowerW)
	bank.put16(uint16(int16(clampInt32(w, -32760, 32760))))
	bank.put16(scaleFactor(0))

	// Hz / HzSF — ×100
	hz := uint16(0)
	if inv.FrequencyHz > 0 {
		hz = uint16(inv.FrequencyHz*100 + 0.5)
	}
	bank.put16(hz)
	bank.put16(scaleFactor(-2))

	// VA / VAr / PF — not measured
	bank.put16(notImplS16)
	bank.put16(scaleFactor(0))
	bank.put16(notImplS16)
	bank.put16(scaleFactor(0))
	bank.put16(notImplS16)
	bank.put16(scaleFactor(0))

	// WH (acc32) — per-inverter lifetime not tracked; emit 0 (acc32 sentinel)
	bank.putAcc32(0)
	bank.put16(scaleFactor(0))

	// DC fields not exposed per-inverter
	bank.put16(notImplU16) // DCA
	bank.put16(scaleFactor(0))
	bank.put16(notImplU16) // DCV
	bank.put16(scaleFactor(0))
	bank.put16(notImplS16) // DCW
	bank.put16(scaleFactor(0))

	// Tmp
	bank.put16(uint16(int16(clampInt32(int32(inv.TemperatureC), -100, 200))))
	bank.put16(notImplS16) // TmpSnk
	bank.put16(notImplS16) // TmpTrns
	bank.put16(notImplS16) // TmpOt
	bank.put16(scaleFactor(0))

	// St
	st := StOff
	if inv.Online && inv.ACPowerW > 0 {
		st = StMPPT
	} else if inv.Online {
		st = StStandby
	}
	bank.put16(st)
	bank.put16(0) // StVnd

	// Evt1/Evt2 + 4× Vendor events (acc32 each)
	for i := 0; i < 6; i++ {
		bank.putAcc32(0)
	}

	// --- Multi-MPPT (160) — only this inverter's panels ---
	if !opt.DisableMPPT {
		emitPerInverterMPPT(&bank, inv)
	}

	bank.put16(EndModelID, 0)
	return bank
}

func emitPerInverterMPPT(bank *Bank, inv source.Inverter) {
	powers := inv.PanelPowers()
	if len(powers) == 0 {
		return
	}
	n := uint16(len(powers))
	bodyLen := MultiMPPTFixedBlockLen + MultiMPPTPerModuleLen*n

	bank.put16(MultiMPPTModelID, bodyLen)

	// Fixed block (8 regs body)
	bank.put16(scaleFactor(-1))
	bank.put16(scaleFactor(0))
	bank.put16(scaleFactor(0))
	bank.put16(scaleFactor(0))
	bank.putAcc32(0)
	bank.put16(n)
	bank.put16(notImplU16)

	// One module per panel
	for i, p := range powers {
		bank.put16(uint16(i + 1))
		bank.putString(panelLabel(inv, i), 8)

		var dca uint16 = notImplU16
		if inv.ACVoltageV > 0 && p >= 0 {
			amps10 := float64(p) / float64(inv.ACVoltageV) * 10
			if amps10 < 65535 {
				dca = uint16(amps10 + 0.5)
			}
		}
		bank.put16(dca)

		dcv := uint16(notImplU16)
		if inv.ACVoltageV > 0 && inv.ACVoltageV < 65535 {
			dcv = uint16(inv.ACVoltageV)
		}
		bank.put16(dcv)

		dcw := uint16(notImplU16)
		if p >= 0 && p < 65535 {
			dcw = uint16(p)
		}
		bank.put16(dcw)

		bank.putAcc32(0) // DCWH
		bank.putAcc32(0) // Tms — not tracked per-panel

		bank.put16(uint16(int16(clampInt32(int32(inv.TemperatureC), -100, 200))))

		st := StOff
		if p > 0 {
			st = StMPPT
		}
		bank.put16(st)
		bank.putAcc32(0) // DCEvt
	}
}

// inverterModelLabel returns a friendly Md value for the Common Model based
// on the wire-protocol type code.
func inverterModelLabel(inv source.Inverter) string {
	switch inv.TypeCode {
	case "01":
		return "APsystems DS3"
	case "03":
		return "APsystems DS3-L"
	case "04":
		return "APsystems DS3-H"
	}
	return "APsystems microinverter"
}
