package bus

import (
	"context"
	"errors"
	"time"

	ebuserrors "github.com/d3vi1/helianthus-ebusgo/errors"
	"github.com/d3vi1/helianthus-ebusgo/protocol"
	"github.com/d3vi1/helianthus-ebusgo/transport"
)

// ParsedFrame holds a parsed eBUS frame along with the time it was fully received.
type ParsedFrame struct {
	Frame    protocol.Frame
	RecvTime time.Time
	CRCValid bool
}

// FrameReader reads SYN-delimited eBUS frames from a transport with escape handling.
type FrameReader struct {
	transport transport.RawTransport
	escape    bool
	buffer    []byte
}

// NewFrameReader creates a frame reader for the given transport.
func NewFrameReader(tr transport.RawTransport) *FrameReader {
	return &FrameReader{transport: tr}
}

// ReadFrame blocks until a complete SYN-delimited frame is available.
// Returns the parsed frame, or an error on transport failure or context cancellation.
func (r *FrameReader) ReadFrame(ctx context.Context) (ParsedFrame, error) {
	for {
		if ctx.Err() != nil {
			return ParsedFrame{}, ctx.Err()
		}

		symbol, err := r.transport.ReadByte()
		if err != nil {
			if errors.Is(err, ebuserrors.ErrTimeout) {
				r.escape = false
				continue
			}
			return ParsedFrame{}, err
		}

		if r.escape {
			r.escape = false
			switch symbol {
			case 0x00:
				r.buffer = append(r.buffer, protocol.SymbolEscape)
			case 0x01:
				r.buffer = append(r.buffer, protocol.SymbolSyn)
			default:
				// Invalid escape sequence — discard buffer.
				r.buffer = r.buffer[:0]
			}
			continue
		}

		switch symbol {
		case protocol.SymbolEscape:
			r.escape = true
		case protocol.SymbolSyn:
			if len(r.buffer) == 0 {
				continue
			}
			frame, crcValid := parseRawFrame(r.buffer)
			r.buffer = r.buffer[:0]
			if frame.Source == 0 && frame.Target == 0 {
				// Malformed — too short or wrong length.
				continue
			}
			return ParsedFrame{
				Frame:    frame,
				RecvTime: time.Now(),
				CRCValid: crcValid,
			}, nil
		default:
			r.buffer = append(r.buffer, symbol)
		}
	}
}

// parseRawFrame parses [SRC, DST, PB, SB, NN, DATA..., CRC] from unescaped bytes.
// Returns the frame and whether the CRC is valid. If the buffer is too short or
// the length doesn't match NN, returns a zero frame.
func parseRawFrame(raw []byte) (protocol.Frame, bool) {
	if len(raw) < 6 {
		return protocol.Frame{}, false
	}
	nn := int(raw[4])
	expected := 6 + nn
	if len(raw) != expected {
		return protocol.Frame{}, false
	}
	crc := protocol.CRC(raw[:len(raw)-1])
	data := make([]byte, nn)
	copy(data, raw[5:5+nn])
	return protocol.Frame{
		Source:    raw[0],
		Target:    raw[1],
		Primary:   raw[2],
		Secondary: raw[3],
		Data:      data,
	}, crc == raw[len(raw)-1]
}
