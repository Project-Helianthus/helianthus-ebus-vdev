package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os/signal"
	"syscall"
	"time"

	"github.com/Project-Helianthus/helianthus-ebusgo/emulation"
	"github.com/Project-Helianthus/helianthus-ebusgo/transport"

	"github.com/Project-Helianthus/helianthus-ebus-vdev/internal/bus"
	"github.com/Project-Helianthus/helianthus-ebus-vdev/internal/config"
	"github.com/Project-Helianthus/helianthus-ebus-vdev/internal/datasource"
	"github.com/Project-Helianthus/helianthus-ebus-vdev/internal/device/thermostat"
)

const preflightDuration = 3 * time.Second

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Dial adapter.
	tr, err := dialTransport(cfg.Adapter)
	if err != nil {
		log.Fatalf("dial adapter: %v", err)
	}
	defer tr.Close()
	log.Printf("connected to adapter %s (%s)", cfg.Adapter.Address, cfg.Adapter.Protocol)

	// Build targets for all devices.
	targets := make(map[byte]*emulation.Target)
	sources := make([]*datasource.GraphQLZoneSource, 0, len(cfg.Devices))
	forceConflict := false

	for i, devCfg := range cfg.Devices {
		slaveAddr := config.SlaveAddress(devCfg.PreferredAddress)

		// Preflight: check if slave address is already in use.
		log.Printf("device[%d] %q: master=0x%02X slave=0x%02X",
			i, devCfg.Name, devCfg.PreferredAddress, slaveAddr)

		// Create data source.
		src := datasource.NewGraphQLZoneSource(datasource.GraphQLZoneConfig{
			GatewayURL: cfg.Gateway.GraphQLURL,
			ZoneID:     devCfg.SourceZone,
		})
		sources = append(sources, src)

		// Create VR90 device.
		delay := 8 * time.Millisecond
		if devCfg.ResponseDelayMs > 0 {
			delay = time.Duration(devCfg.ResponseDelayMs) * time.Millisecond
		}
		jitter := devCfg.TempJitterC
		dev := thermostat.NewVR90Device(thermostat.VR90Config{
			Name:          devCfg.Name,
			ScanID:        devCfg.ScanID,
			TempJitterC:   jitter,
			ResponseDelay: delay,
		}, src)

		target, err := dev.Target(slaveAddr)
		if err != nil {
			log.Fatalf("device[%d] %q: build target: %v", i, devCfg.Name, err)
		}

		targets[slaveAddr] = target
		if devCfg.ForceAddressConflict {
			forceConflict = true
		}
	}

	// Run preflight listen.
	log.Printf("preflight: listening for %v...", preflightDuration)
	if err := preflight(ctx, tr, targets, preflightDuration); err != nil {
		log.Fatalf("preflight failed: %v", err)
	}
	log.Printf("preflight: no address conflicts detected")

	// Start data source subscriptions.
	for i, src := range sources {
		src := src
		devName := cfg.Devices[i].Name
		go func() {
			log.Printf("data source %q: connecting to %s", devName, cfg.Gateway.GraphQLURL)
			if err := src.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("data source %q: %v", devName, err)
			}
		}()
	}

	// Start slave responder.
	responder := bus.NewSlaveResponder(tr, targets, forceConflict)
	log.Printf("slave responder started with %d target(s)", len(targets))

	if err := responder.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("slave responder: %v", err)
	}

	log.Printf("shutdown complete (late_dropped=%d)", responder.LateDropped())
}

func dialTransport(cfg config.AdapterConfig) (transport.RawTransport, error) {
	conn, err := net.DialTimeout("tcp", cfg.Address, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("tcp connect %s: %w", cfg.Address, err)
	}

	readTimeout := 1 * time.Second
	writeTimeout := 1 * time.Second

	switch cfg.Protocol {
	case "enh":
		return transport.NewENHTransport(conn, readTimeout, writeTimeout), nil
	case "tcp-plain":
		return transport.NewTCPPlainTransport(conn, readTimeout, writeTimeout), nil
	default:
		conn.Close()
		return nil, fmt.Errorf("unsupported protocol %q", cfg.Protocol)
	}
}

func preflight(ctx context.Context, tr transport.RawTransport, targets map[byte]*emulation.Target, duration time.Duration) error {
	preCtx, preCancel := context.WithTimeout(ctx, duration)
	defer preCancel()

	reader := bus.NewFrameReader(tr)
	for {
		parsed, err := reader.ReadFrame(preCtx)
		if err != nil {
			if preCtx.Err() != nil {
				// Timeout — no conflicts found.
				return nil
			}
			return fmt.Errorf("preflight read: %w", err)
		}

		// Check if any frame's source matches our slave addresses.
		if _, isOurs := targets[parsed.Frame.Source]; isOurs {
			return fmt.Errorf("address conflict: slave address 0x%02X already active on bus (source of frame from 0x%02X to 0x%02X)",
				parsed.Frame.Source, parsed.Frame.Source, parsed.Frame.Target)
		}
	}
}
