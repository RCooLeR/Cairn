package portforward

import (
	"net"
	"strconv"
	"strings"

	"github.com/RCooLeR/Cairn/internal/models"
)

func forwardKey(protocol string, hostPort int) string {
	return protocol + "/" + strconv.Itoa(hostPort)
}

func isAllInterfaces(hostIP string) bool {
	switch strings.Trim(strings.TrimSpace(hostIP), "[]") {
	case "", "0.0.0.0", "::":
		return true
	default:
		return false
	}
}

func isLoopbackHost(hostIP string) bool {
	ip := net.ParseIP(strings.Trim(strings.TrimSpace(hostIP), "[]"))
	return ip != nil && ip.IsLoopback()
}

// bindAddrFor mirrors the container's published bind interface onto the Windows
// host: an all-interfaces publish (0.0.0.0/::) binds 0.0.0.0 so the port is
// LAN-reachable like Docker Desktop; a loopback publish stays on 127.0.0.1; any
// other concrete address is mirrored verbatim.
func bindAddrFor(hostIP string) string {
	if isAllInterfaces(hostIP) {
		return "0.0.0.0"
	}
	if isLoopbackHost(hostIP) {
		return "127.0.0.1"
	}
	trimmed := strings.Trim(strings.TrimSpace(hostIP), "[]")
	if ip := net.ParseIP(trimmed); ip != nil {
		return trimmed
	}
	return "0.0.0.0"
}

// bindRank ranks bind addresses by exposure so that when multiple containers
// publish the same host port on different interfaces the broadest binding wins,
// matching Docker's "widest publish wins" behavior.
func bindRank(bindAddr string) int {
	switch bindAddr {
	case "0.0.0.0":
		return 2
	case "127.0.0.1":
		return 0
	default:
		return 1
	}
}

func normalizeProtocol(proto string) string {
	switch strings.ToLower(strings.TrimSpace(proto)) {
	case "", protoTCP:
		return protoTCP
	case protoUDP:
		return protoUDP
	default:
		return ""
	}
}

// desiredForwards computes the set of host forwards implied by the running
// containers, keyed by protocol+host-port. Unpublished ports (no host port) are
// ignored; when the same host port is published by several bindings the most
// permissive interface is kept.
func desiredForwards(containers []models.ContainerSummary) map[string]spec {
	out := map[string]spec{}
	for _, container := range containers {
		for _, binding := range container.Ports {
			hostPort := strings.TrimSpace(binding.HostPort)
			if hostPort == "" {
				continue
			}
			port, err := strconv.Atoi(hostPort)
			if err != nil || port <= 0 || port > 65535 {
				continue
			}
			protocol := normalizeProtocol(binding.Protocol)
			if protocol == "" {
				continue
			}
			candidate := spec{
				protocol:      protocol,
				hostPort:      port,
				bindAddr:      bindAddrFor(binding.HostIP),
				containerID:   container.ID,
				containerName: container.Name,
			}
			key := forwardKey(protocol, port)
			if existing, ok := out[key]; ok && bindRank(candidate.bindAddr) <= bindRank(existing.bindAddr) {
				continue
			}
			out[key] = candidate
		}
	}
	return out
}
