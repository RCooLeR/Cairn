package portforward

import (
	"context"
	"net"
	"sync"
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
				mu.Lock()
				for key, session := range sessions {
					if session.lastSeen.Before(cutoff) {
						fwd.untrack(session.backend)
						_ = session.backend.Close()
						delete(sessions, key)
					}
				}
				mu.Unlock()
			}
		}
	}()

	buffer := make([]byte, udpBufferSize)
	for {
		n, src, err := host.ReadFrom(buffer)
		if err != nil {
			return
		}
		key := src.String()

		mu.Lock()
		session := sessions[key]
		if session == nil {
			backend, derr := m.dialer.DialPacket(ctx, fwd.spec.hostPort)
			if derr != nil {
				mu.Unlock()
				continue
			}
			session = &udpSession{backend: backend}
			sessions[key] = session
			fwd.track(backend)
			fwd.wg.Add(1)
			go m.pumpUDPReplies(fwd, host, backend, src)
		}
		session.lastSeen = m.now()
		backend := session.backend
		mu.Unlock()

		payload := make([]byte, n)
		copy(payload, buffer[:n])
		_, _ = backend.Write(payload)
	}
}

// pumpUDPReplies forwards datagrams coming back from the backend to the
// originating client source. It exits when the backend connection closes (on
// idle reap or forward shutdown).
func (m *Manager) pumpUDPReplies(fwd *forward, host net.PacketConn, backend net.Conn, dst net.Addr) {
	defer fwd.wg.Done()
	buffer := make([]byte, udpBufferSize)
	for {
		n, err := backend.Read(buffer)
		if err != nil {
			return
		}
		if _, werr := host.WriteTo(buffer[:n], dst); werr != nil {
			return
		}
	}
}
