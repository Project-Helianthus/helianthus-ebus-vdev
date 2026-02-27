package thermostat

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/d3vi1/helianthus-ebusgo/emulation"
	"github.com/d3vi1/helianthus-ebusgo/protocol"
	"github.com/d3vi1/helianthus-ebusgo/types"
)

// mockSource implements datasource.ZoneSource for testing.
type mockSource struct {
	temp    float64
	tempErr error
}

func (m *mockSource) Temperature() (float64, error) {
	if m.tempErr != nil {
		return 0, m.tempErr
	}
	return m.temp, nil
}

func (m *mockSource) Humidity() (float64, error) {
	return 0, errors.New("not implemented")
}

const testSlaveAddr = byte(0x75)

func makeEvent(target, primary, secondary byte, data []byte) emulation.RequestEvent {
	return emulation.RequestEvent{
		At: 0,
		Frame: protocol.Frame{
			Source:    0x10,
			Target:    target,
			Primary:   primary,
			Secondary: secondary,
			Data:      data,
		},
	}
}

func TestVR90Device_Name(t *testing.T) {
	t.Parallel()

	dev := NewVR90Device(VR90Config{Name: "Zone3-VR90"}, &mockSource{temp: 21.0})
	if got := dev.Name(); got != "Zone3-VR90" {
		t.Fatalf("Name() = %q; want %q", got, "Zone3-VR90")
	}
}

func TestVR90Device_DefaultName(t *testing.T) {
	t.Parallel()

	dev := NewVR90Device(VR90Config{}, &mockSource{temp: 21.0})
	if got := dev.Name(); got != "VR90" {
		t.Fatalf("Name() = %q; want %q", got, "VR90")
	}
}

func TestVR90Device_CandidateAddresses(t *testing.T) {
	t.Parallel()

	dev := NewVR90Device(VR90Config{}, &mockSource{temp: 21.0})
	addrs := dev.CandidateAddresses()
	if len(addrs) != 7 {
		t.Fatalf("CandidateAddresses() len = %d; want 7", len(addrs))
	}
	// First should be 0x17, last should be 0xF7.
	if addrs[0] != 0x17 {
		t.Fatalf("CandidateAddresses()[0] = 0x%02x; want 0x17", addrs[0])
	}
	if addrs[6] != 0xF7 {
		t.Fatalf("CandidateAddresses()[6] = 0x%02x; want 0xF7", addrs[6])
	}
}

func TestVR90Device_Target_Identify(t *testing.T) {
	t.Parallel()

	dev := NewVR90Device(VR90Config{}, &mockSource{temp: 21.0})
	target, err := dev.Target(testSlaveAddr)
	if err != nil {
		t.Fatalf("Target() error: %v", err)
	}

	resp, err := target.Emulate(makeEvent(testSlaveAddr, 0x07, 0x04, nil))
	if err != nil {
		t.Fatalf("Emulate identify error: %v", err)
	}

	data := resp.Frame.Data
	// MF=0xB5, ID="RC C ", SW=0x0508, HW=0x6201 → 10 bytes
	if len(data) != 10 {
		t.Fatalf("identify payload len = %d; want 10", len(data))
	}
	if data[0] != 0xB5 {
		t.Fatalf("manufacturer = 0x%02x; want 0xB5", data[0])
	}
	wantID := "RC C "
	gotID := string(data[1:6])
	if gotID != wantID {
		t.Fatalf("device ID = %q; want %q", gotID, wantID)
	}
	// SW: big-endian 0x0508
	if data[6] != 0x05 || data[7] != 0x08 {
		t.Fatalf("software = 0x%02x%02x; want 0x0508", data[6], data[7])
	}
	// HW: big-endian 0x6201
	if data[8] != 0x62 || data[9] != 0x01 {
		t.Fatalf("hardware = 0x%02x%02x; want 0x6201", data[8], data[9])
	}
}

func TestVR90Device_Target_RoomTempRead(t *testing.T) {
	t.Parallel()

	sourceTemp := 21.5
	source := &mockSource{temp: sourceTemp}
	dev := NewVR90Device(VR90Config{}, source)
	target, err := dev.Target(testSlaveAddr)
	if err != nil {
		t.Fatalf("Target() error: %v", err)
	}

	// B509 RoomTemp read: [0x0D, 0x00, 0x00]
	resp, err := target.Emulate(makeEvent(testSlaveAddr, 0xB5, 0x09, []byte{0x0D, 0x00, 0x00}))
	if err != nil {
		t.Fatalf("Emulate RoomTemp error: %v", err)
	}

	data := resp.Frame.Data
	if len(data) != 3 {
		t.Fatalf("RoomTemp response len = %d; want 3", len(data))
	}

	// Decode D2C to verify temperature is near source (within jitter bound + 1 tick).
	val, decErr := types.DATA2c{}.Decode(data[:2])
	if decErr != nil {
		t.Fatalf("decode D2C error: %v", decErr)
	}
	if !val.Valid {
		t.Fatal("decoded D2C value is not valid")
	}
	diff := math.Abs(val.Value.(float64) - sourceTemp)
	maxDiff := defaultTempJitterC + d2cTick + 0.001
	if diff > maxDiff {
		t.Fatalf("decoded temp = %f; deviates from source %f by %f (max %f)",
			val.Value.(float64), sourceTemp, diff, maxDiff)
	}
	// Sensor status OK.
	if data[2] != sensorOK {
		t.Fatalf("sensor status = 0x%02x; want 0x%02x", data[2], sensorOK)
	}
}

func TestVR90Device_Target_RoomTempRead_SensorError(t *testing.T) {
	t.Parallel()

	source := &mockSource{tempErr: errors.New("sensor offline")}
	dev := NewVR90Device(VR90Config{}, source)
	target, err := dev.Target(testSlaveAddr)
	if err != nil {
		t.Fatalf("Target() error: %v", err)
	}

	resp, err := target.Emulate(makeEvent(testSlaveAddr, 0xB5, 0x09, []byte{0x0D, 0x00, 0x00}))
	if err != nil {
		t.Fatalf("Emulate RoomTemp error: %v", err)
	}

	data := resp.Frame.Data
	if len(data) != 3 {
		t.Fatalf("RoomTemp response len = %d; want 3", len(data))
	}
	// Zero temp + sensor fault.
	if data[0] != 0x00 || data[1] != 0x00 {
		t.Fatalf("temp bytes = [0x%02x, 0x%02x]; want [0x00, 0x00]", data[0], data[1])
	}
	if data[2] != sensorFault {
		t.Fatalf("sensor status = 0x%02x; want 0x%02x (fault)", data[2], sensorFault)
	}
}

func TestVR90Device_Target_JitterBounded(t *testing.T) {
	t.Parallel()

	sourceTemp := 21.5
	jitterBound := 0.5
	source := &mockSource{temp: sourceTemp}
	dev := NewVR90Device(VR90Config{TempJitterC: jitterBound}, source)
	target, err := dev.Target(testSlaveAddr)
	if err != nil {
		t.Fatalf("Target() error: %v", err)
	}

	for i := 0; i < 1000; i++ {
		resp, err := target.Emulate(makeEvent(testSlaveAddr, 0xB5, 0x09, []byte{0x0D, 0x00, 0x00}))
		if err != nil {
			t.Fatalf("iteration %d: Emulate error: %v", i, err)
		}

		val, decErr := types.DATA2c{}.Decode(resp.Frame.Data[:2])
		if decErr != nil {
			t.Fatalf("iteration %d: decode error: %v", i, decErr)
		}
		if !val.Valid {
			t.Fatalf("iteration %d: invalid D2C value", i)
		}

		diff := math.Abs(val.Value.(float64) - sourceTemp)
		// Allow jitterBound + 1 tick tolerance for D2C quantization.
		maxDiff := jitterBound + d2cTick + 0.001
		if diff > maxDiff {
			t.Fatalf("iteration %d: temp %f deviates from source %f by %f (max %f)",
				i, val.Value.(float64), sourceTemp, diff, maxDiff)
		}
	}
}

func TestVR90Device_Target_B509WriteAccept(t *testing.T) {
	t.Parallel()

	dev := NewVR90Device(VR90Config{}, &mockSource{temp: 21.0})
	target, err := dev.Target(testSlaveAddr)
	if err != nil {
		t.Fatalf("Target() error: %v", err)
	}

	// B509 write: [0x0E, 0x1F, 0x00, 0x00]
	resp, err := target.Emulate(makeEvent(testSlaveAddr, 0xB5, 0x09, []byte{0x0E, 0x1F, 0x00, 0x00}))
	if err != nil {
		t.Fatalf("Emulate B509 write error: %v", err)
	}

	if resp.Rule == "" {
		t.Fatal("no rule matched for B509 write")
	}
}

func TestVR90Device_Target_OtherRegisterRead(t *testing.T) {
	t.Parallel()

	dev := NewVR90Device(VR90Config{}, &mockSource{temp: 21.0})
	target, err := dev.Target(testSlaveAddr)
	if err != nil {
		t.Fatalf("Target() error: %v", err)
	}

	// B509 read register 0x1F, 0x00 (RoomTempOffset) — should return defaults.
	resp, err := target.Emulate(makeEvent(testSlaveAddr, 0xB5, 0x09, []byte{0x0D, 0x1F, 0x00}))
	if err != nil {
		t.Fatalf("Emulate other register read error: %v", err)
	}

	if resp.Rule != "b509-register-read-default" {
		t.Fatalf("matched rule = %q; want %q", resp.Rule, "b509-register-read-default")
	}
}

func TestVR90Device_D2CRoundTrip_KnownValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		temp float64
	}{
		{"21.5", 21.5},
		{"0.0", 0.0},
		{"-5.0", -5.0},
		{"22.0625", 22.0625}, // 22 + 1/16
		{"30.9375", 30.9375}, // 30 + 15/16
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			quantized := roundToD2CTick(tc.temp)
			encoded, err := types.DATA2c{}.Encode(quantized)
			if err != nil {
				t.Fatalf("encode %f error: %v", tc.temp, err)
			}
			decoded, err := types.DATA2c{}.Decode(encoded)
			if err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if !decoded.Valid {
				t.Fatalf("decoded invalid for %f", tc.temp)
			}
			if math.Abs(decoded.Value.(float64)-tc.temp) > 0.0001 {
				t.Fatalf("round-trip %f → %f", tc.temp, decoded.Value.(float64))
			}
		})
	}
}

func TestVR90Device_D2CRounding(t *testing.T) {
	t.Parallel()

	// 21.53 is not an exact D2C tick. Should round to 21.5 (21 + 8/16).
	quantized := roundToD2CTick(21.53)
	if math.Abs(quantized-21.5) > 0.0001 {
		t.Fatalf("roundToD2CTick(21.53) = %f; want 21.5", quantized)
	}

	// 21.56 should round to 21.5625 (21 + 9/16).
	quantized2 := roundToD2CTick(21.56)
	if math.Abs(quantized2-21.5625) > 0.0001 {
		t.Fatalf("roundToD2CTick(21.56) = %f; want 21.5625", quantized2)
	}
}

func TestVR90Device_ResponseDelay(t *testing.T) {
	t.Parallel()

	delay := 10 * time.Millisecond
	dev := NewVR90Device(VR90Config{ResponseDelay: delay}, &mockSource{temp: 21.0})
	target, err := dev.Target(testSlaveAddr)
	if err != nil {
		t.Fatalf("Target() error: %v", err)
	}

	resp, err := target.Emulate(makeEvent(testSlaveAddr, 0x07, 0x04, nil))
	if err != nil {
		t.Fatalf("Emulate error: %v", err)
	}

	if resp.RespondAt != delay {
		t.Fatalf("RespondAt = %v; want %v", resp.RespondAt, delay)
	}
}
