package bus

import (
	"context"
	"errors"
	"testing"
	"time"

	ebuserrors "github.com/d3vi1/helianthus-ebusgo/errors"
	"github.com/d3vi1/helianthus-ebusgo/emulation"
	"github.com/d3vi1/helianthus-ebusgo/protocol"
	"github.com/d3vi1/helianthus-ebusgo/transport"
)

const testSlaveAddr = byte(0x75)

func buildTestTarget() *emulation.Target {
	return &emulation.Target{
		Name:    "test-target",
		Address: testSlaveAddr,
		DefaultTiming: emulation.TimingConstraints{
			MinResponseDelay: 0,
			MaxResponseDelay: 50 * time.Millisecond,
		},
		Rules: []emulation.Rule{
			{
				Name:    "identify",
				Matcher: emulation.MatchPrimarySecondary(0x07, 0x04),
				Builder: emulation.BuildFunc(func(_ protocol.Frame) (emulation.ResponsePlan, error) {
					return emulation.ResponsePlan{
						Delay: 0,
						Data:  []byte{0xB5, 'T', 'E', 'S', 'T', ' ', 0x01, 0x00, 0x01, 0x00},
					}, nil
				}),
			},
		},
	}
}

// buildFrameBytes constructs escaped [SRC, DST, PB, SB, NN, DATA..., CRC] + SYN.
func buildFrameBytes(src, dst, pb, sb byte, data []byte) []byte {
	frame := []byte{src, dst, pb, sb, byte(len(data))}
	frame = append(frame, data...)
	crc := protocol.CRC(frame)
	frame = append(frame, crc)
	escaped := protocol.EscapeBytes(frame)
	return append(escaped, protocol.SymbolSyn)
}

func TestSlaveResponder_IdentifyResponse(t *testing.T) {
	t.Parallel()

	master, slave := newDuplexPipe()
	target := buildTestTarget()
	targets := map[byte]*emulation.Target{testSlaveAddr: target}
	sr := NewSlaveResponder(slave, targets, false)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- sr.Run(ctx) }()

	// Master sends identify frame.
	frame := buildFrameBytes(0x10, testSlaveAddr, 0x07, 0x04, nil)
	master.Write(frame)

	// Read slave ACK.
	ack, err := master.read.ReadByte()
	if err != nil {
		t.Fatalf("read ACK error: %v", err)
	}
	if ack != protocol.SymbolAck {
		t.Fatalf("ACK = 0x%02x; want 0x%02x", ack, protocol.SymbolAck)
	}

	// Read slave response bytes (NN + escaped data + CRC).
	var respBytes []byte
	for i := 0; i < 50; i++ {
		b, err := master.read.ReadByte()
		if err != nil {
			break
		}
		respBytes = append(respBytes, b)
		// We expect NN=10 data bytes + CRC, all possibly escaped.
		// Stop reading after we have enough.
		if len(respBytes) >= 12 {
			break
		}
	}

	if len(respBytes) == 0 {
		t.Fatal("no response bytes received")
	}

	// Send master ACK.
	master.Write([]byte{protocol.SymbolAck})

	// Close transport to unblock the responder's next ReadByte.
	slave.Close()
	<-done
}

func TestSlaveResponder_AddressMismatch_Silence(t *testing.T) {
	t.Parallel()

	lb := transport.NewLoopback()
	target := buildTestTarget()
	targets := map[byte]*emulation.Target{testSlaveAddr: target}
	sr := NewSlaveResponder(lb, targets, false)

	// Frame addressed to 0x35 — not our address.
	frame := buildFrameBytes(0x10, 0x35, 0x07, 0x04, nil)
	go func() {
		lb.Write(frame)
		time.Sleep(50 * time.Millisecond)
		lb.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := sr.Run(ctx)
	if err != nil && !errors.Is(err, ebuserrors.ErrTransportClosed) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() unexpected error: %v", err)
	}
}

func TestSlaveResponder_CRCError_Silence(t *testing.T) {
	t.Parallel()

	lb := transport.NewLoopback()
	target := buildTestTarget()
	targets := map[byte]*emulation.Target{testSlaveAddr: target}
	sr := NewSlaveResponder(lb, targets, false)

	// Frame with wrong CRC.
	frame := []byte{0x10, testSlaveAddr, 0x07, 0x04, 0x00, 0xFF}
	frame = append(frame, protocol.SymbolSyn)
	go func() {
		lb.Write(frame)
		time.Sleep(50 * time.Millisecond)
		lb.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := sr.Run(ctx)
	if err != nil && !errors.Is(err, ebuserrors.ErrTransportClosed) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() unexpected error: %v", err)
	}
}

func TestSlaveResponder_UnserviceableFrame_NACK(t *testing.T) {
	t.Parallel()

	master, slave := newDuplexPipe()
	target := buildTestTarget()
	targets := map[byte]*emulation.Target{testSlaveAddr: target}
	sr := NewSlaveResponder(slave, targets, false)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- sr.Run(ctx) }()

	// Send a frame with unknown PB/SB — no matching rule.
	frame := buildFrameBytes(0x10, testSlaveAddr, 0xFF, 0xFF, nil)
	master.Write(frame)

	// Read the NACK.
	nack, err := master.read.ReadByte()
	if err != nil {
		t.Fatalf("read NACK error: %v", err)
	}
	if nack != protocol.SymbolNack {
		t.Fatalf("response = 0x%02x; want NACK 0x%02x", nack, protocol.SymbolNack)
	}

	// Close transport to unblock the responder.
	slave.Close()
	<-done
}

func TestSlaveResponder_CollisionDetection_Degrade(t *testing.T) {
	t.Parallel()

	lb := transport.NewLoopback()
	target := buildTestTarget()
	targets := map[byte]*emulation.Target{testSlaveAddr: target}
	sr := NewSlaveResponder(lb, targets, false)

	// Frame FROM our slave address — collision.
	frame := buildFrameBytes(testSlaveAddr, 0x10, 0x07, 0x04, nil)
	go func() {
		lb.Write(frame)
		time.Sleep(50 * time.Millisecond)
		lb.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	sr.Run(ctx)

	if !sr.Degraded() {
		t.Fatal("expected degraded=true after collision")
	}
}

func TestSlaveResponder_CollisionDetection_ForceOverride(t *testing.T) {
	t.Parallel()

	lb := transport.NewLoopback()
	target := buildTestTarget()
	targets := map[byte]*emulation.Target{testSlaveAddr: target}
	sr := NewSlaveResponder(lb, targets, true)

	frame := buildFrameBytes(testSlaveAddr, 0x10, 0x07, 0x04, nil)
	go func() {
		lb.Write(frame)
		time.Sleep(50 * time.Millisecond)
		lb.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	sr.Run(ctx)

	if sr.Degraded() {
		t.Fatal("expected degraded=false with force override")
	}
}

func TestSlaveResponder_LateDropped(t *testing.T) {
	t.Parallel()

	sr := &SlaveResponder{}
	if sr.LateDropped() != 0 {
		t.Fatalf("LateDropped() = %d; want 0", sr.LateDropped())
	}
	sr.lateDropped.Add(3)
	if sr.LateDropped() != 3 {
		t.Fatalf("LateDropped() = %d; want 3", sr.LateDropped())
	}
}

func TestSlaveResponder_TransportClosed(t *testing.T) {
	t.Parallel()

	lb := transport.NewLoopback()
	target := buildTestTarget()
	targets := map[byte]*emulation.Target{testSlaveAddr: target}
	sr := NewSlaveResponder(lb, targets, false)

	lb.Close()

	err := sr.Run(context.Background())
	if !errors.Is(err, ebuserrors.ErrTransportClosed) {
		t.Fatalf("Run() = %v; want ErrTransportClosed", err)
	}
}

func TestFrameReader_EscapeHandling(t *testing.T) {
	t.Parallel()

	lb := transport.NewLoopback()
	reader := NewFrameReader(lb)

	// Frame with data = [0xA9] (SymbolEscape).
	raw := []byte{0x10, testSlaveAddr, 0x07, 0x04, 0x01, protocol.SymbolEscape}
	crc := protocol.CRC(raw)
	raw = append(raw, crc)
	escaped := protocol.EscapeBytes(raw)
	escaped = append(escaped, protocol.SymbolSyn)

	go func() {
		lb.Write(escaped)
		time.Sleep(50 * time.Millisecond)
		lb.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	parsed, err := reader.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame() error: %v", err)
	}
	if !parsed.CRCValid {
		t.Fatal("CRC should be valid")
	}
	if len(parsed.Frame.Data) != 1 || parsed.Frame.Data[0] != protocol.SymbolEscape {
		t.Fatalf("data = %v; want [0x%02x]", parsed.Frame.Data, protocol.SymbolEscape)
	}
}

func TestFrameReader_MalformedFrame_Skipped(t *testing.T) {
	t.Parallel()

	lb := transport.NewLoopback()
	reader := NewFrameReader(lb)

	malformed := []byte{0x10, 0x20, 0x30, protocol.SymbolSyn}
	valid := buildFrameBytes(0x10, testSlaveAddr, 0x07, 0x04, nil)

	go func() {
		lb.Write(malformed)
		lb.Write(valid)
		time.Sleep(50 * time.Millisecond)
		lb.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	parsed, err := reader.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame() error: %v", err)
	}

	if parsed.Frame.Source != 0x10 || parsed.Frame.Target != testSlaveAddr {
		t.Fatalf("got src=0x%02x dst=0x%02x; want src=0x10 dst=0x%02x",
			parsed.Frame.Source, parsed.Frame.Target, testSlaveAddr)
	}
}
