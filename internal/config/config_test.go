package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRCCAddress_Valid(t *testing.T) {
	t.Parallel()

	for _, addr := range RCCMasterAddresses {
		if err := ValidateRCCAddress(addr); err != nil {
			t.Errorf("ValidateRCCAddress(0x%02X) = %v; want nil", addr, err)
		}
	}
}

func TestValidateRCCAddress_Invalid(t *testing.T) {
	t.Parallel()

	invalid := []byte{0x00, 0x10, 0x15, 0x75, 0xA9, 0xAA, 0xFF}
	for _, addr := range invalid {
		if err := ValidateRCCAddress(addr); err == nil {
			t.Errorf("ValidateRCCAddress(0x%02X) = nil; want error", addr)
		}
	}
}

func TestSlaveAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		master byte
		slave  byte
	}{
		{0x17, 0x1C},
		{0x30, 0x35},
		{0x70, 0x75},
		{0xF7, 0xFC},
	}

	for _, tc := range tests {
		if got := SlaveAddress(tc.master); got != tc.slave {
			t.Errorf("SlaveAddress(0x%02X) = 0x%02X; want 0x%02X", tc.master, got, tc.slave)
		}
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	t.Parallel()

	yaml := `
adapter:
  protocol: enh
  address: "192.168.100.2:9999"
gateway:
  graphql_url: "ws://localhost:8080/graphql"
devices:
  - type: thermostat/vr90
    name: "Zone3 Virtual Thermostat"
    source_zone: "zone-2"
    preferred_address: 0x70
    temp_jitter_c: 0.5
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	if cfg.Adapter.Protocol != "enh" {
		t.Fatalf("protocol = %q; want 'enh'", cfg.Adapter.Protocol)
	}
	if cfg.Adapter.Address != "192.168.100.2:9999" {
		t.Fatalf("address = %q", cfg.Adapter.Address)
	}
	if len(cfg.Devices) != 1 {
		t.Fatalf("devices len = %d; want 1", len(cfg.Devices))
	}
	if cfg.Devices[0].PreferredAddress != 0x70 {
		t.Fatalf("preferred_address = 0x%02X; want 0x70", cfg.Devices[0].PreferredAddress)
	}
}

func TestValidate_MissingAdapter(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Gateway: GatewayConfig{GraphQLURL: "ws://localhost:8080/graphql"},
		Devices: []DeviceConfig{{Type: "thermostat/vr90", PreferredAddress: 0x70, SourceZone: "zone-2"}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() = nil; want error for missing adapter address")
	}
}

func TestValidate_InvalidProtocol(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Adapter: AdapterConfig{Protocol: "invalid", Address: "host:1234"},
		Gateway: GatewayConfig{GraphQLURL: "ws://localhost:8080/graphql"},
		Devices: []DeviceConfig{{Type: "thermostat/vr90", PreferredAddress: 0x70, SourceZone: "zone-2"}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() = nil; want error for invalid protocol")
	}
}

func TestValidate_InvalidRCCAddress(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Adapter: AdapterConfig{Protocol: "enh", Address: "host:1234"},
		Gateway: GatewayConfig{GraphQLURL: "ws://localhost:8080/graphql"},
		Devices: []DeviceConfig{{Type: "thermostat/vr90", PreferredAddress: 0x15, SourceZone: "zone-2"}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() = nil; want error for invalid RCC address")
	}
}

func TestValidate_DuplicateAddress(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Adapter: AdapterConfig{Protocol: "enh", Address: "host:1234"},
		Gateway: GatewayConfig{GraphQLURL: "ws://localhost:8080/graphql"},
		Devices: []DeviceConfig{
			{Type: "thermostat/vr90", PreferredAddress: 0x70, SourceZone: "zone-2"},
			{Type: "thermostat/vr90", PreferredAddress: 0x70, SourceZone: "zone-3"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() = nil; want error for duplicate address")
	}
}

func TestValidate_NoDevices(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Adapter: AdapterConfig{Protocol: "enh", Address: "host:1234"},
		Gateway: GatewayConfig{GraphQLURL: "ws://localhost:8080/graphql"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() = nil; want error for no devices")
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Adapter: AdapterConfig{Protocol: "enh", Address: "host:1234"},
		Gateway: GatewayConfig{GraphQLURL: "ws://localhost:8080/graphql"},
		Devices: []DeviceConfig{
			{Type: "thermostat/vr90", PreferredAddress: 0x70, SourceZone: "zone-2"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v; want nil", err)
	}
}
