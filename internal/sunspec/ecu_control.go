package sunspec

import (
	"time"

	"github.com/bolke/ecu-sunspec/internal/source"
)

// ECUControlModelID is a second vendor model next to 64202, kept separate
// so adding it doesn't shift 64202's repeating per-inverter block under
// clients that already decode it.
const ECUControlModelID uint16 = 64203

// Model 64203 layout (aggregate bank only):
//
//	body[0]    FastPoll          enum16, RW — Service Mode choice: 0 normal, 1 on, 2 auto
//	                                          (reads the ECU flag, 0/1, when the adapter holds no choice)
//	body[1]    FastPollElapsedS  uint16     — seconds since Service Mode was entered; 0 when off
//	body[2..3] LastPollTms       uint32     — Unix time of the ECU's last radio cycle
//	body[4]    DataAgeS          uint16     — seconds since LastPollTms
//	body[5]    EmaConnected      enum16     — client's link to EMA: 0 down, 1 up
//	body[6..7] LastProdRecTms    uint32     — Unix time of the newest APS18 row in record.db
//	body[8]    ProdRecPending    uint16     — APS18 rows not yet marked sent
//	body[9]    ProdRecLastHour   uint16     — APS18 rows dated in the last hour (12 normally, 0 in Service Mode)
//	body[10..11] LastEmaCtrlTms  uint32     — Unix time of control_client's last EMA session
//	body[12]   ProdRecAgeS       uint16     — seconds since LastProdRecTms
//	body[13]   FastPollThresholdW uint16, RW — auto-mode production threshold (W)
//
// The ECU web UI claims Service Mode quits after 30 minutes; on fw
// C1.2.27A main.exe resets fast_polling.conf after 60 minutes.
// FastPollElapsedS counts up from activation and is > 0 exactly while
// Service Mode is on. The EMA fields are refreshed at most once a minute
// (see source/ema.go).
const (
	ECUControlBodyLen          uint16 = 14
	ECUControlBodyFastPollOff  uint16 = 0
	ECUControlBodyThresholdOff uint16 = 13
)

func emitECUControl(bank *Bank, s source.Snapshot) {
	bank.put16(ECUControlModelID, ECUControlBodyLen)

	var fastPoll, elapsed uint16
	if s.FastPolling {
		fastPoll = 1
		if !s.FastPollingSince.IsZero() {
			elapsed = secondsU16(s.Captured.Sub(s.FastPollingSince))
			if elapsed == 0 {
				elapsed = 1 // > 0 means "on"
			}
		}
	}
	if s.FastPollConfigured {
		fastPoll = uint16(s.FastPollMode)
	}
	bank.put16(fastPoll, elapsed)

	putUnixTime(bank, s.LastPollTime)
	if s.LastPollTime.IsZero() {
		bank.put16(notImplU16)
	} else {
		bank.put16(secondsU16(s.Captured.Sub(s.LastPollTime)))
	}

	switch s.EMA.Connected {
	case source.EMAConnected:
		bank.put16(1)
	case source.EMADisconnected:
		bank.put16(0)
	default:
		bank.put16(notImplU16)
	}
	putUnixTime(bank, s.EMA.LastProdRecord)
	bank.put16(uint16(clampInt32(int32(s.EMA.ProdRecordsPending), 0, 65534)))
	bank.put16(uint16(clampInt32(int32(s.EMA.ProdRecordsLastHour), 0, 65534)))
	putUnixTime(bank, s.EMA.LastControlContact)
	if s.EMA.LastProdRecord.IsZero() {
		bank.put16(notImplU16)
	} else {
		bank.put16(secondsU16(s.Captured.Sub(s.EMA.LastProdRecord)))
	}
	if s.FastPollConfigured {
		bank.put16(uint16(clampInt32(int32(s.FastPollThresholdW), 0, 65534))) // FastPollThresholdW
	} else {
		bank.put16(notImplU16)
	}
}

// putUnixTime emits t as uint32 Unix seconds, not-implemented when zero.
func putUnixTime(bank *Bank, t time.Time) {
	if t.IsZero() {
		bank.putAcc32(uint64(notImplU32))
		return
	}
	bank.putAcc32(uint64(t.Unix()))
}

// secondsU16 converts d to whole seconds clamped to 0..65534, keeping
// 0xFFFF free for "not implemented".
func secondsU16(d time.Duration) uint16 {
	sec := d / time.Second
	if sec < 0 {
		return 0
	}
	if sec > 65534 {
		return 65534
	}
	return uint16(sec)
}
