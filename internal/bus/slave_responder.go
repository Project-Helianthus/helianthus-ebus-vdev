package bus

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	ebuserrors "github.com/Project-Helianthus/helianthus-ebusgo/errors"
	"github.com/Project-Helianthus/helianthus-ebusgo/emulation"
	"github.com/Project-Helianthus/helianthus-ebusgo/protocol"
	"github.com/Project-Helianthus/helianthus-ebusgo/transport"
)

const (
	// maxResponseDeadline is the hard deadline for a slave to respond after
	// receiving a complete frame. Per eBUS spec, the slave must respond
	// within ~50ms of frame completion.
	maxResponseDeadline = 50 * time.Millisecond

	// masterAckTimeout is how long to wait for the master's ACK after
	// sending a slave response.
	masterAckTimeout = 50 * time.Millisecond

	// maxSlaveRetries is the number of times to retry a slave response
	// after receiving a master NACK.
	maxSlaveRetries = 1
)

// SlaveResponder listens for eBUS frames on a transport and responds as a
// slave device when addressed. It implements the full slave wire FSM.
type SlaveResponder struct {
	transport transport.RawTransport
	reader    *FrameReader
	targets   map[byte]*emulation.Target

	// Collision detection.
	mu                   sync.RWMutex
	degraded             bool
	forceAddressConflict bool

	// Metrics.
	lateDropped atomic.Int64
}

// NewSlaveResponder creates a slave responder for the given transport and targets.
// The targets map is keyed by slave address.
func NewSlaveResponder(tr transport.RawTransport, targets map[byte]*emulation.Target, forceConflict bool) *SlaveResponder {
	return &SlaveResponder{
		transport:            tr,
		reader:               NewFrameReader(tr),
		targets:              targets,
		forceAddressConflict: forceConflict,
	}
}

// LateDropped returns the number of frames that were dropped because the
// response deadline was exceeded.
func (sr *SlaveResponder) LateDropped() int64 {
	return sr.lateDropped.Load()
}

// Degraded reports whether the responder has degraded due to an address collision.
func (sr *SlaveResponder) Degraded() bool {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	return sr.degraded
}

// Run starts the slave responder loop. It blocks until the context is canceled
// or a fatal transport error occurs.
func (sr *SlaveResponder) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		parsed, err := sr.reader.ReadFrame(ctx)
		if err != nil {
			if errors.Is(err, ebuserrors.ErrTransportClosed) || ctx.Err() != nil {
				return err
			}
			continue
		}

		// Collision detection: if a frame's source is one of our slave addresses,
		// another device is using our address.
		if _, isOurs := sr.targets[parsed.Frame.Source]; isOurs {
			sr.handleCollision(parsed.Frame.Source)
			continue
		}

		// Only process frames addressed to one of our slave addresses.
		target, ok := sr.targets[parsed.Frame.Target]
		if !ok {
			// Not addressed to us — silence.
			continue
		}

		// CRC error — silence.
		if !parsed.CRCValid {
			continue
		}

		// If degraded, don't respond.
		if sr.Degraded() {
			continue
		}

		sr.handleFrame(ctx, parsed, target)
	}
}

func (sr *SlaveResponder) handleCollision(addr byte) {
	if sr.forceAddressConflict {
		log.Printf("CRITICAL: address collision on 0x%02x (forced override active)", addr)
		return
	}
	log.Printf("CRITICAL: address collision on 0x%02x — degrading (stop responding)", addr)
	sr.mu.Lock()
	sr.degraded = true
	sr.mu.Unlock()
}

func (sr *SlaveResponder) handleFrame(ctx context.Context, parsed ParsedFrame, target *emulation.Target) {
	deadline := parsed.RecvTime.Add(maxResponseDeadline)

	// Check if we can service this frame.
	event := emulation.RequestEvent{
		Frame: parsed.Frame,
	}

	resp, err := target.Emulate(event)
	if err != nil {
		// Addressed to us but unserviceable — send NACK.
		sr.sendByte(protocol.SymbolNack)
		return
	}

	// Check deadline before ACK.
	if time.Now().After(deadline) {
		sr.lateDropped.Add(1)
		// Too late — do not ACK late. Silence.
		return
	}

	// Send ACK.
	if err := sr.sendByte(protocol.SymbolAck); err != nil {
		return
	}

	// Apply response delay.
	if resp.RespondAt > 0 {
		select {
		case <-time.After(time.Duration(resp.RespondAt)):
		case <-ctx.Done():
			return
		}
	}

	// Check deadline again after delay.
	if time.Now().After(deadline) {
		sr.lateDropped.Add(1)
		return
	}

	// Encode and send response.
	sr.sendResponse(ctx, resp.Frame.Data)
}

func (sr *SlaveResponder) sendResponse(ctx context.Context, data []byte) {
	encoded, err := protocol.EncodeSlaveResponse(data)
	if err != nil {
		return
	}

	for attempt := 0; attempt <= maxSlaveRetries; attempt++ {
		if ctx.Err() != nil {
			return
		}

		if _, err := sr.transport.Write(encoded); err != nil {
			return
		}

		// Read master's ACK/NACK (best-effort).
		ack, err := sr.readByteWithTimeout()
		if err != nil {
			// Timeout or error reading ACK — assume success.
			return
		}

		if ack == protocol.SymbolAck {
			return
		}

		if ack == protocol.SymbolNack && attempt < maxSlaveRetries {
			// Master NACK — retry.
			continue
		}

		// Unknown byte or final NACK — give up.
		return
	}
}

func (sr *SlaveResponder) sendByte(b byte) error {
	_, err := sr.transport.Write([]byte{b})
	return err
}

func (sr *SlaveResponder) readByteWithTimeout() (byte, error) {
	// The transport.ReadByte() may block indefinitely on loopback.
	// For real transports, the adapter provides timeouts.
	// We use a goroutine + channel approach for safety.
	type result struct {
		b   byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := sr.transport.ReadByte()
		ch <- result{b, err}
	}()

	select {
	case r := <-ch:
		return r.b, r.err
	case <-time.After(masterAckTimeout):
		return 0, ebuserrors.ErrTimeout
	}
}
