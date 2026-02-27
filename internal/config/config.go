package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// RCCMasterAddresses lists valid master addresses for VR90 room controllers.
var RCCMasterAddresses = []byte{0x17, 0x30, 0x37, 0x70, 0x77, 0xF0, 0xF7}

// Config is the top-level configuration for helianthus-ebus-vdev.
type Config struct {
	Adapter AdapterConfig  `yaml:"adapter"`
	Gateway GatewayConfig  `yaml:"gateway"`
	Devices []DeviceConfig `yaml:"devices"`
}

// AdapterConfig configures the eBUS adapter connection.
type AdapterConfig struct {
	Protocol string `yaml:"protocol"` // "enh" or "tcp-plain"
	Address  string `yaml:"address"`  // host:port
}

// GatewayConfig configures the gateway GraphQL connection.
type GatewayConfig struct {
	GraphQLURL string `yaml:"graphql_url"`
}

// DeviceConfig configures a single virtual device.
type DeviceConfig struct {
	Type                 string  `yaml:"type"`                   // e.g., "thermostat/vr90"
	Name                 string  `yaml:"name"`                   // human-readable name
	SourceZone           string  `yaml:"source_zone"`            // zone ID for data source
	PreferredAddress     byte    `yaml:"preferred_address"`      // master address (must be in RCC list)
	TempJitterC          float64 `yaml:"temp_jitter_c"`          // jitter bound in °C
	ResponseDelayMs      int     `yaml:"response_delay_ms"`      // response delay in ms
	ScanID               string  `yaml:"scan_id"`                // optional scan ID
	ForceAddressConflict bool    `yaml:"force_address_conflict"` // ignore address collisions
}

// SlaveAddress returns the slave address for a master address (master + 5).
func SlaveAddress(master byte) byte {
	return master + 5
}

// ValidateRCCAddress checks if addr is in the RCC master address list.
func ValidateRCCAddress(addr byte) error {
	for _, valid := range RCCMasterAddresses {
		if addr == valid {
			return nil
		}
	}
	return fmt.Errorf("address 0x%02x is not a valid RCC master address; valid: %s",
		addr, formatAddressList(RCCMasterAddresses))
}

func formatAddressList(addrs []byte) string {
	s := ""
	for i, a := range addrs {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("0x%02X", a)
	}
	return s
}

// Load reads and parses a YAML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if c.Adapter.Address == "" {
		return fmt.Errorf("adapter address is required")
	}
	if c.Adapter.Protocol == "" {
		c.Adapter.Protocol = "enh"
	}
	if c.Adapter.Protocol != "enh" && c.Adapter.Protocol != "tcp-plain" {
		return fmt.Errorf("unsupported adapter protocol %q; use 'enh' or 'tcp-plain'", c.Adapter.Protocol)
	}
	if c.Gateway.GraphQLURL == "" {
		return fmt.Errorf("gateway graphql_url is required")
	}
	if len(c.Devices) == 0 {
		return fmt.Errorf("at least one device must be configured")
	}

	seen := make(map[byte]bool)
	for i, dev := range c.Devices {
		if dev.Type == "" {
			return fmt.Errorf("device[%d]: type is required", i)
		}
		if dev.PreferredAddress == 0 {
			return fmt.Errorf("device[%d]: preferred_address is required", i)
		}
		if err := ValidateRCCAddress(dev.PreferredAddress); err != nil {
			return fmt.Errorf("device[%d]: %w", i, err)
		}
		if seen[dev.PreferredAddress] {
			return fmt.Errorf("device[%d]: duplicate preferred_address 0x%02X", i, dev.PreferredAddress)
		}
		seen[dev.PreferredAddress] = true
		if dev.SourceZone == "" {
			return fmt.Errorf("device[%d]: source_zone is required", i)
		}
	}

	return nil
}
