// Package portforward relays published container ports on the Windows host into
// the WSL backend, so a container's `-p` publish behaves like Docker Desktop's
// host networking even though Docker Engine runs inside a WSL distro.
//
// For each running container port with a host binding it opens a host listener
// on the mirrored interface (0.0.0.0 stays LAN-reachable, 127.0.0.1 stays
// loopback) and relays traffic into the distro: TCP over a stdio socat tunnel
// (robust, no WSL IP needed) and UDP over a direct datagram dial to the distro
// IP (a stdio stream cannot preserve datagram boundaries).
package portforward

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	defaultReconcileInterval = 5 * time.Second
	udpIdleTimeout           = 60 * time.Second
	udpBufferSize            = 64 * 1024

	statusActive = "active"
	statusError  = "error"

	protoTCP = "tcp"
	protoUDP = "udp"
)

// DockerLister lists the containers whose published ports should be forwarded.
type DockerLister interface {
	ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error)
}

// Dialer establishes relay connections into the backend (the WSL distro).
// DialStream returns a stream connection used for TCP relays; DialPacket returns
// a datagram-preserving connection used for UDP relays.
type Dialer interface {
	DialStream(ctx context.Context, port int) (net.Conn, error)
	DialPacket(ctx context.Context, port int) (net.Conn, error)
}

// listenFunc and listenPacketFunc abstract host listeners so tests can inject
// in-memory transports instead of binding real OS ports.
type listenFunc func(ctx context.Context, network, address string) (net.Listener, error)
type listenPacketFunc func(ctx context.Context, network, address string) (net.PacketConn, error)

// Options configures a Manager. Zero values fall back to sensible defaults.
type Options struct {
	ReconcileInterval time.Duration
	Enabled           bool
	Now               func() time.Time
	Listen            listenFunc
	ListenPacket      listenPacketFunc
}

// Manager watches running containers and keeps host port forwards in sync with
// their published ports.
type Manager struct {
	docker DockerLister
	dialer Dialer
	events bus.Bus

	reconcileInterval time.Duration
	now               func() time.Time
	listen            listenFunc
	listenPacket      listenPacketFunc

	reconcileMu sync.Mutex

	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
	enabled  bool
	forwards map[string]*forward

	wg sync.WaitGroup
}

// spec is the immutable description of one host forward. Two specs are equal
// (and so a forward is left untouched across reconciles) only when every field
// matches, so a changed bind interface or owning container forces a rebind.
type spec struct {
	protocol      string
	hostPort      int
	bindAddr      string
	containerID   string
	containerName string
}

// forward owns the OS resources for one host port: a listener (or packet conn)
// and the live relay connections, all closed together on stop.
type forward struct {
	spec   spec
	cancel context.CancelFunc
	status string
	reason string

	mu      sync.Mutex
	closers map[interface{ Close() error }]struct{}
	wg      sync.WaitGroup
}
