package net

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/dtls"
)

type connData struct {
	conn net.Conn
	err  error
}

// DTLSListener is a DTLS listener that provides accept with context.
type DTLSListener struct {
	listener  *dtls.Listener
	heartBeat time.Duration
	wg        sync.WaitGroup
	doneCh    chan struct{}
	connCh    chan connData

	deadline atomic.Value
}

func (l *DTLSListener) acceptLoop() {
	defer l.wg.Done()
	for {
		conn, err := l.listener.Accept()
		select {
		case l.connCh <- connData{conn: conn, err: err}:
			if err != nil {
				return
			}
		case <-l.doneCh:
			return
		}
	}
}

// NewDTLSListener creates dtls listener.
// Known networks are "udp", "udp4" (IPv4-only), "udp6" (IPv6-only).
func NewDTLSListener(network string, addr string, cfg *dtls.Config, heartBeat time.Duration) (*DTLSListener, error) {
	a, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve address: %v", err)
	}
	listener, err := dtls.Listen(network, a, cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot create new dtls listener: %v", err)
	}
	l := DTLSListener{
		listener:  listener,
		heartBeat: heartBeat,
		doneCh:    make(chan struct{}),
		connCh:    make(chan connData),
	}
	l.wg.Add(1)

	go l.acceptLoop()

	return &l, nil
}

// AcceptWithContext waits with context for a generic Conn.
func (l *DTLSListener) AcceptWithContext(ctx context.Context) (net.Conn, error) {
	for {
		select {
		case <-ctx.Done():
			if ctx.Err() != nil {
				return nil, fmt.Errorf("cannot accept connections: %v", ctx.Err())
			}
			return nil, nil
		default:
		}
		err := l.SetDeadline(time.Now().Add(l.heartBeat))
		if err != nil {
			return nil, fmt.Errorf("cannot accept connections: %v", err)
		}
		rw, err := l.Accept()
		if err != nil {
			if isTemporary(err) {
				continue
			}
			return nil, fmt.Errorf("cannot accept connections: %v", err)
		}
		return rw, nil
	}
}

// SetDeadline sets deadline for accept operation.
func (l *DTLSListener) SetDeadline(t time.Time) error {
	l.deadline.Store(t)
	return nil
}

// Accept waits for a generic Conn.
func (l *DTLSListener) Accept() (net.Conn, error) {
	var deadline time.Time
	v := l.deadline.Load()
	if v != nil {
		deadline = v.(time.Time)
	}

	if deadline.IsZero() {
		select {
		case d := <-l.connCh:
			if d.err != nil {
				return nil, d.err
			}
			return NewConnDTLS(d.conn), nil
		}
	}

	select {
	case d := <-l.connCh:
		if d.err != nil {
			return nil, d.err
		}
		return NewConnDTLS(d.conn), nil
	case <-time.After(deadline.Sub(time.Now())):
		return nil, fmt.Errorf(ioTimeout)
	}
}

// Close closes the connection.
func (l *DTLSListener) Close() error {
	err := l.listener.Close(time.Millisecond * 100)
	close(l.doneCh)
	l.wg.Wait()
	return err
}

// Addr represents a network end point address.
func (l *DTLSListener) Addr() net.Addr {
	return l.listener.Addr()
}
