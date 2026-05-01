// Package server hosts a Modbus TCP server backed by a periodically refreshed
// SunSpec Bank.
package server

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bolke/ecu-sunspec/internal/source"
	"github.com/bolke/ecu-sunspec/internal/sunspec"
	"github.com/simonvetter/modbus"
)

// Provider is anything that can produce a fresh Snapshot. Implementations must
// be safe to call from a single goroutine.
type Provider interface {
	Build(ctx context.Context) (source.Snapshot, error)
}

// Config tunes server behavior. Zero values fall back to sensible defaults.
type Config struct {
	URL             string        // tcp://0.0.0.0:1502
	RefreshInterval time.Duration // default 5s
	Timeout         time.Duration // session idle timeout
	MaxClients      uint
	Encoder         sunspec.Options
	Logger          *log.Logger
}

// Server owns the Modbus listener and the snapshot refresh goroutine.
type Server struct {
	cfg      Config
	provider Provider

	bank atomic.Pointer[sunspec.Bank]

	mu  sync.Mutex
	srv *modbus.ModbusServer

	logger *log.Logger
}

// New constructs a Server. Call Start to begin listening, Stop to shut down.
func New(p Provider, cfg Config) *Server {
	if cfg.URL == "" {
		cfg.URL = "tcp://0.0.0.0:1502"
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = 5 * time.Second
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxClients == 0 {
		cfg.MaxClients = 8
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	return &Server{cfg: cfg, provider: p, logger: cfg.Logger}
}

// Start launches the refresh loop, primes the bank with one synchronous
// refresh, then starts the Modbus listener. Returns when the listener is
// ready (or the priming refresh fails fatally).
func (s *Server) Start(ctx context.Context) error {
	if err := s.refresh(ctx); err != nil {
		// Don't abort startup — clients will receive zero-value registers
		// until a successful refresh lands.
		s.logger.Printf("initial refresh failed: %v", err)
	}

	go s.refreshLoop(ctx)

	srv, err := modbus.NewServer(&modbus.ServerConfiguration{
		URL:        s.cfg.URL,
		Timeout:    s.cfg.Timeout,
		MaxClients: s.cfg.MaxClients,
		Logger:     s.cfg.Logger,
	}, &handler{owner: s})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.srv = srv
	s.mu.Unlock()
	if err := srv.Start(); err != nil {
		return err
	}
	s.logger.Printf("modbus tcp listening on %s", s.cfg.URL)
	return nil
}

// Stop drains the server.
func (s *Server) Stop() error {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Stop()
}

// SetSnapshot is exposed for tests so they can drive the server with a fixed
// snapshot without wiring a Provider.
func (s *Server) SetSnapshot(snap source.Snapshot) {
	bank := sunspec.Encode(snap, s.cfg.Encoder)
	s.bank.Store(&bank)
}

func (s *Server) refresh(ctx context.Context) error {
	if s.provider == nil {
		return errors.New("no snapshot provider configured")
	}
	snap, err := s.provider.Build(ctx)
	if err != nil {
		return err
	}
	bank := sunspec.Encode(snap, s.cfg.Encoder)
	s.bank.Store(&bank)
	return nil
}

func (s *Server) refreshLoop(ctx context.Context) {
	t := time.NewTicker(s.cfg.RefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.refresh(ctx); err != nil {
				s.logger.Printf("refresh failed: %v", err)
			}
		}
	}
}

// --- modbus handler glue ---

type handler struct {
	owner *Server
}

func (h *handler) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	if req.IsWrite {
		h.owner.logger.Printf("FC06/10 write from %s uid=%d addr=%d (rejecting)",
			req.ClientAddr, req.UnitId, req.Addr)
		return nil, modbus.ErrIllegalFunction
	}
	bank := h.owner.bank.Load()
	if bank == nil || !bank.Contains(req.Addr, req.Quantity) {
		h.owner.logger.Printf("FC03 read from %s uid=%d addr=%d qty=%d → IllegalDataAddress",
			req.ClientAddr, req.UnitId, req.Addr, req.Quantity)
		return nil, modbus.ErrIllegalDataAddress
	}
	h.owner.logger.Printf("FC03 read from %s uid=%d addr=%d qty=%d",
		req.ClientAddr, req.UnitId, req.Addr, req.Quantity)
	return bank.Slice(req.Addr, req.Quantity), nil
}

func (h *handler) HandleInputRegisters(req *modbus.InputRegistersRequest) ([]uint16, error) {
	bank := h.owner.bank.Load()
	if bank == nil || !bank.Contains(req.Addr, req.Quantity) {
		h.owner.logger.Printf("FC04 read from %s uid=%d addr=%d qty=%d → IllegalDataAddress",
			req.ClientAddr, req.UnitId, req.Addr, req.Quantity)
		return nil, modbus.ErrIllegalDataAddress
	}
	h.owner.logger.Printf("FC04 read from %s uid=%d addr=%d qty=%d",
		req.ClientAddr, req.UnitId, req.Addr, req.Quantity)
	return bank.Slice(req.Addr, req.Quantity), nil
}

func (h *handler) HandleCoils(req *modbus.CoilsRequest) ([]bool, error) {
	h.owner.logger.Printf("FC01/05/0F coils from %s uid=%d addr=%d qty=%d (rejecting)",
		req.ClientAddr, req.UnitId, req.Addr, req.Quantity)
	return nil, modbus.ErrIllegalFunction
}

func (h *handler) HandleDiscreteInputs(req *modbus.DiscreteInputsRequest) ([]bool, error) {
	h.owner.logger.Printf("FC02 discrete from %s uid=%d addr=%d qty=%d (rejecting)",
		req.ClientAddr, req.UnitId, req.Addr, req.Quantity)
	return nil, modbus.ErrIllegalFunction
}
