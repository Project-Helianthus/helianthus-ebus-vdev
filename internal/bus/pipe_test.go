package bus

import (
	"sync"

	ebuserrors "github.com/Project-Helianthus/helianthus-ebusgo/errors"
)

// pipeTransport is a unidirectional test transport: one side writes, the other reads.
// Use newPipe() to create a bidirectional pair.
type pipeTransport struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	pos    int
	closed bool
}

func newPipeTransport() *pipeTransport {
	p := &pipeTransport{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *pipeTransport) ReadByte() (byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.pos >= len(p.buf) && !p.closed {
		p.cond.Wait()
	}
	if p.pos >= len(p.buf) {
		return 0, ebuserrors.ErrTransportClosed
	}
	b := p.buf[p.pos]
	p.pos++
	if p.pos == len(p.buf) {
		p.buf = p.buf[:0]
		p.pos = 0
	}
	return b, nil
}

func (p *pipeTransport) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, ebuserrors.ErrTransportClosed
	}
	p.buf = append(p.buf, data...)
	p.cond.Broadcast()
	return len(data), nil
}

func (p *pipeTransport) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cond.Broadcast()
	return nil
}

// duplexPipe connects two endpoints: what side A writes, side B reads, and vice versa.
type duplexPipe struct {
	aToB *pipeTransport // A writes → B reads
	bToA *pipeTransport // B writes → A reads
}

type pipeEnd struct {
	read  *pipeTransport
	write *pipeTransport
}

func (e *pipeEnd) ReadByte() (byte, error) { return e.read.ReadByte() }
func (e *pipeEnd) Write(d []byte) (int, error) { return e.write.Write(d) }
func (e *pipeEnd) Close() error {
	e.read.Close()
	e.write.Close()
	return nil
}

// newDuplexPipe creates a bidirectional pipe. Returns (master side, slave side).
func newDuplexPipe() (*pipeEnd, *pipeEnd) {
	aToB := newPipeTransport()
	bToA := newPipeTransport()
	return &pipeEnd{read: bToA, write: aToB}, &pipeEnd{read: aToB, write: bToA}
}
