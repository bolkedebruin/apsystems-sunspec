// Command ecu-sunspec exposes an APsystems ECU as a SunSpec inverter over
// Modbus TCP. Run on the ECU itself or as a sidecar that reaches the ECU's
// SQLite + /tmp/parameters_app.conf via a shared filesystem.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bolke/ecu-sunspec/internal/server"
	"github.com/bolke/ecu-sunspec/internal/source"
	"github.com/bolke/ecu-sunspec/internal/sunspec"
	"gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	var (
		bind         = flag.String("bind", "tcp://0.0.0.0:1502", "modbus TCP listen URL")
		dbDir        = flag.String("db-dir", "/home", "directory containing database.db and historical.db")
		paramsFile   = flag.String("params-file", "/tmp/parameters_app.conf", "path to parameters_app.conf")
		yunengDir    = flag.String("yuneng-dir", "/etc/yuneng", "directory containing ecuid.conf, version.conf, model.conf (\"\" to skip)")
		manufacturer = flag.String("manufacturer", sunspec.DefaultManufacturer, "SunSpec Mn field; \"Fronius\" enables Victron auto-detection")
		modelName    = flag.String("model-name", sunspec.DefaultModelName, "SunSpec Md field; appears as the device name in Victron's UI")
		phase        = flag.String("phase-mode", "auto", "SunSpec inverter model: auto|single|split|three (101|102|103)")
		serialOverride = flag.String("serial-override", "", "force this SN regardless of ecuid.conf (use to re-spawn under a new device id)")
		serialFallback = flag.String("serial-fallback", "", "SN to use when ecuid.conf is unavailable")
		refresh      = flag.Duration("refresh-interval", 5*time.Second, "snapshot refresh cadence")

		logFile       = flag.String("log-file", "", "rotated log file path; empty means stderr")
		logMaxSizeMB  = flag.Int("log-max-size", 5, "max log size MB before rotation")
		logMaxBackups = flag.Int("log-max-backups", 3, "rotated log files retained")
		logMaxAgeDays = flag.Int("log-max-age", 7, "rotated log retention days")
	)
	flag.Parse()

	var logSink io.Writer = os.Stderr
	if *logFile != "" {
		logSink = &lumberjack.Logger{
			Filename:   *logFile,
			MaxSize:    *logMaxSizeMB,
			MaxBackups: *logMaxBackups,
			MaxAge:     *logMaxAgeDays,
			Compress:   true,
		}
	}
	logger := log.New(logSink, "ecu-sunspec ", log.LstdFlags|log.Lmsgprefix)

	db, err := source.OpenSQLite(*dbDir)
	if err != nil {
		logger.Fatalf("open sqlite at %s: %v", *dbDir, err)
	}
	defer db.Close()

	builder := source.NewBuilder(db, *paramsFile, *yunengDir)

	phaseMode, err := parsePhase(*phase)
	if err != nil {
		logger.Fatalf("phase-mode: %v", err)
	}

	srv := server.New(builder, server.Config{
		URL:             *bind,
		RefreshInterval: *refresh,
		Encoder: sunspec.Options{
			Manufacturer:   *manufacturer,
			ModelName:      *modelName,
			SerialOverride: *serialOverride,
			SerialFallback: *serialFallback,
			Phase:          phaseMode,
		},
		Logger: logger,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		logger.Fatalf("server start: %v", err)
	}
	<-ctx.Done()
	logger.Println("shutting down")
	if err := srv.Stop(); err != nil {
		logger.Printf("stop: %v", err)
	}
}

func parsePhase(s string) (sunspec.PhaseMode, error) {
	switch s {
	case "auto", "":
		return sunspec.PhaseAuto, nil
	case "single", "1", "101":
		return sunspec.PhaseSingle, nil
	case "split", "2", "102":
		return sunspec.PhaseSplit, nil
	case "three", "3", "103":
		return sunspec.PhaseThree, nil
	default:
		return sunspec.PhaseAuto, fmt.Errorf("unknown phase mode %q", s)
	}
}
