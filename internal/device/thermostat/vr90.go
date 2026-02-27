package thermostat

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/d3vi1/helianthus-ebusgo/emulation"
	"github.com/d3vi1/helianthus-ebusgo/protocol"
	"github.com/d3vi1/helianthus-ebusgo/types"

	"github.com/d3vi1/helianthus-ebus-vdev/internal/datasource"
)

const (
	vr90Manufacturer = byte(0xB5)
	vr90DeviceID     = "RC C "
	vr90Software     = uint16(0x0508)
	vr90Hardware     = uint16(0x6201)

	// D2C tick size: 1/16 °C.
	d2cTick = 1.0 / 16.0

	// Default jitter bound in °C.
	defaultTempJitterC = 0.5

	// Sensor status bytes.
	sensorOK    = byte(0x00)
	sensorFault = byte(0x55)
)

// RCC master/slave address pairs valid for VR90 room controllers.
var rccAddressPairs = []struct{ master, slave byte }{
	{0x17, 0x1C},
	{0x30, 0x35},
	{0x37, 0x3C},
	{0x70, 0x75},
	{0x77, 0x7C},
	{0xF0, 0xF5},
	{0xF7, 0xFC},
}

// VR90Config configures a virtual VR90 thermostat.
type VR90Config struct {
	Name          string
	ScanID        string
	TempJitterC   float64
	ResponseDelay time.Duration
}

// VR90Device implements device.Device for a Vaillant VR90 room thermostat.
type VR90Device struct {
	config     VR90Config
	dataSource datasource.ZoneSource

	mu           sync.Mutex
	jitterOffset float64
	rng          *rand.Rand
}

// NewVR90Device creates a VR90 virtual thermostat backed by the given data source.
func NewVR90Device(config VR90Config, source datasource.ZoneSource) *VR90Device {
	if config.Name == "" {
		config.Name = "VR90"
	}
	if config.TempJitterC <= 0 {
		config.TempJitterC = defaultTempJitterC
	}
	if config.ResponseDelay <= 0 {
		config.ResponseDelay = 8 * time.Millisecond
	}

	return &VR90Device{
		config:     config,
		dataSource: source,
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (d *VR90Device) Name() string {
	return d.config.Name
}

func (d *VR90Device) CandidateAddresses() []byte {
	addrs := make([]byte, len(rccAddressPairs))
	for i, pair := range rccAddressPairs {
		addrs[i] = pair.master
	}
	return addrs
}

// Target builds an emulation.Target for the given slave address with all VR90 rules.
func (d *VR90Device) Target(address byte) (*emulation.Target, error) {
	scanID := d.config.ScanID
	if scanID == "" {
		scanID = emulation.DefaultVR90ScanID
	}

	profile := emulation.VR90Profile{
		Address:             address,
		Manufacturer:        vr90Manufacturer,
		DeviceID:            vr90DeviceID,
		Software:            vr90Software,
		Hardware:            vr90Hardware,
		ScanID:              scanID,
		EnableB509Discovery: true,
		ResponseDelay:       d.config.ResponseDelay,
		MappedCommands:      d.buildMappedCommands(),
	}

	target, err := emulation.NewVR90Target(profile)
	if err != nil {
		return nil, err
	}

	d.addDynamicRules(target)
	return target, nil
}

func (d *VR90Device) buildMappedCommands() []emulation.VR90MappedCommand {
	return []emulation.VR90MappedCommand{
		// B509 writes (prefix [0x0E]) — accept silently.
		{
			Name:          "b509-write-accept",
			Primary:       0xB5,
			Secondary:     0x09,
			PayloadPrefix: []byte{0x0E},
			ResponseData:  []byte{0x00},
		},
	}
}

// buildDynamicTarget creates a target with dynamic RoomTemp rule injected before
// the static mapped commands. This is called by Target() but the dynamic rule
// must be added after NewVR90Target since VR90Profile only supports static commands.
func (d *VR90Device) addDynamicRules(target *emulation.Target) {
	delay := d.config.ResponseDelay

	// B509 RoomTemp read: prefix [0x0D, 0x00, 0x00]
	roomTempRule := emulation.Rule{
		Name:    "b509-roomtemp-read",
		Matcher: emulation.MatchPrimarySecondaryWithPrefix(0xB5, 0x09, []byte{0x0D, 0x00, 0x00}),
		Builder: emulation.BuildFunc(func(_ protocol.Frame) (emulation.ResponsePlan, error) {
			data, err := d.buildRoomTempResponse()
			if err != nil {
				return emulation.ResponsePlan{}, err
			}
			return emulation.ResponsePlan{Delay: delay, Data: data}, nil
		}),
	}

	// B509 other register reads: prefix [0x0D] — static defaults (value=0).
	otherReadRule := emulation.Rule{
		Name:    "b509-register-read-default",
		Matcher: emulation.MatchPrimarySecondaryWithPrefix(0xB5, 0x09, []byte{0x0D}),
		Builder: emulation.BuildFunc(func(_ protocol.Frame) (emulation.ResponsePlan, error) {
			// Return zero-valued register with sensor OK.
			return emulation.ResponsePlan{Delay: delay, Data: []byte{0x00, 0x00, 0x00}}, nil
		}),
	}

	// Insert dynamic rules before mapped commands (which are at the end).
	// Rule order: identify, b509-scanid (from VR90Target), roomtemp, other-read, write-accept.
	existingRules := target.Rules
	newRules := make([]emulation.Rule, 0, len(existingRules)+2)

	// Keep identify and scanid rules from the VR90Target.
	for _, r := range existingRules {
		if r.Name == "b509-write-accept" {
			// Insert dynamic rules before write-accept.
			newRules = append(newRules, roomTempRule, otherReadRule)
		}
		newRules = append(newRules, r)
	}
	target.Rules = newRules
}

func (d *VR90Device) buildRoomTempResponse() ([]byte, error) {
	temp, err := d.dataSource.Temperature()
	if err != nil {
		// Sensor not ready — return zero temp with fault status.
		return []byte{0x00, 0x00, sensorFault}, nil
	}

	jittered := d.applyJitter(temp)
	quantized := roundToD2CTick(jittered)

	encoded, encErr := types.DATA2c{}.Encode(quantized)
	if encErr != nil {
		// Fallback: if encoding fails (out of range), use zero with fault.
		return []byte{0x00, 0x00, sensorFault}, nil
	}

	return append(encoded, sensorOK), nil
}

// applyJitter implements slow-drift random walk: ±1 D2C tick per poll,
// bounded to ±TempJitterC from source temperature.
func (d *VR90Device) applyJitter(sourceTemp float64) float64 {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Random walk: -1, 0, or +1 D2C tick.
	step := (d.rng.Intn(3) - 1) // -1, 0, or 1
	d.jitterOffset += float64(step) * d2cTick

	// Clamp to bounds.
	if d.jitterOffset > d.config.TempJitterC {
		d.jitterOffset = d.config.TempJitterC
	}
	if d.jitterOffset < -d.config.TempJitterC {
		d.jitterOffset = -d.config.TempJitterC
	}

	return sourceTemp + d.jitterOffset
}

// roundToD2CTick rounds a temperature to the nearest D2C tick (1/16 °C).
func roundToD2CTick(temp float64) float64 {
	return math.Round(temp*16) / 16
}
