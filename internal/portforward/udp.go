package portforward

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"
	"time"
)

// udpSession holds the backend datagram connection for one client source
// address. UDP has no connection lifecycle, so sessions are keyed by source and
// reaped after udpIdleTimeout of inactivity.
type udpSession struct {
	backend  net.Conn
	lastSeen time.Time
}

// serveUDP relays datagrams between a host packet conn and the backend. Each
// distinct client source gets its own backend connection so replies can be
// routed back to the right peer; idle sessions are reaped by a janitor.
func (m *Manager) serveUDP(ctx context.Context, fwd *forward, host net.PacketConn) {
	defer fwd.wg.Done()

	var mu sync.Mutex
	sessions := map[string]*udpSession{}
	evict := func(key string, expected *udpSession) {
		mu.Lock()
		if sessions[key] != expected {
			mu.Unlock()
			return
		}
		delete(sessions, key)
		mu.Unlock()

		// Remove the connection from the forward before closing it. The
		// identity check above prevents a delayed pump from evicting a newer
		// session that reused the same client address.
		fwd.untrack(expected.backend)
		_ = expected.backend.Close()
	}

	fwd.wg.Add(1)
	go func() {
		defer fwd.wg.Done()
		ticker := time.NewTicker(udpIdleTimeout / 2)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cutoff := m.now().Add(-udpIdleTimeout)
				var expired []struct {
					key     string
					session *udpSession
				}
				mu.Lock()
				for key, session := range sessions {
					if session.lastSeen.Before(cutoff) {
						expired = append(expired, struct {
							key     string
							session *udpSession
						}{key: key, session: session})
					}
				}
				mu.Unlock()
				for _, entry := range expired {
					evict(entry.key, entry.session)
				}
			}
		}
	}()

	buffer := make([]byte, udpBufferSize)
	for {
		n, src, err := host.ReadFrom(buffer)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			if isConnReset(err) {
				continue
			}
			fwd.fail(err)
			m.publishChanged()
			return
		}
		key := src.String()

		mu.Lock()
		session := sessions[key]
		if session == nil {
			backend, derr := m.dialer.DialPacket(ctx, fwd.spec.hostPort)
			if derr != nil || backend == nil {
				mu.Unlock()
				continue
			}
			session = &udpSession{backend: backend}
			sessions[key] = session
			fwd.track(backend)
			fwd.wg.Add(1)
			go m.pumpUDPReplies(fwd, host, session, src, func() {
				evict(key, session)
			})
		}
		session.lastSeen = m.now()
		backend := session.backend
		mu.Unlock()

		payload := make([]byte, n)
		copy(payload, buffer[:n])
		if written, writeErr := backend.Write(payload); writeErr != nil || written != len(payload) {
			evict(key, session)
		}
	}
}

// pumpUDPReplies forwards datagrams coming back from the backend to the
// originating client source. It exits when the backend connection closes (on
// idle reap or forward shutdown).
func (m *Manager) pumpUDPReplies(
	fwd *forward,
	host net.PacketConn,
	session *udpSession,
	dst net.Addr,
	evict func(),
) {
	defer fwd.wg.Done()
	defer evict()
	buffer := make([]byte, udpBufferSize)
	for {
		n, err := session.backend.Read(buffer)
		if err != nil {
			return
		}
		if _, werr := host.WriteTo(buffer[:n], dst); werr != nil {
			if isConnReset(werr) {
				continue
			}
			return
		}
	}
}

func isConnReset(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == syscall.Errno(10054)
}
