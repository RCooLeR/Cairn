package portforward

import (
	"net"
	"strconv"
	"strings"

	"github.com/RCooLeR/Cairn/internal/models"
)

func forwardKey(protocol string, bindAddr string, hostPort int) string {
	return protocol + "/" + bindAddr + "/" + strconv.Itoa(hostPort)
}

func unsupportedForwardReason(hostIP string) (string, bool) {
	trimmed := strings.Trim(strings.TrimSpace(hostIP), "[]")
	// The relay currently dials the backend's IPv4 address. Fail closed for an
	// IPv6-only publish rather than broadening it to an IPv4 Windows listener.
	if trimmed == "" || trimmed == "0.0.0.0" {
		return "", false
	}
	ip := net.ParseIP(trimmed)
	if ip == nil {
		return "Published bind address is invalid; Cairn did not create a Windows listener.", true
	}
	if ip.IsLoopback() {
		return "Loopback-only published binds cannot be preserved by the WSL relay; Cairn did not create a Windows listener.", true
	}
	if ip.To4() == nil || strings.Contains(trimmed, ":") {
		return "IPv6 published binds are not supported; Cairn did not create a Windows listener.", true
	}
	return "", false
}

// bindAddrFor mirrors a supported published bind interface onto the Windows
// host: an IPv4 all-interfaces publish binds 0.0.0.0 so the port is
// LAN-reachable like Docker Desktop; concrete addresses retain their family
// and value. desiredForwards classifies loopback and IPv6-specific publishes
// as visible errors because the WSL relay cannot preserve those semantics.
func bindAddrFor(hostIP string) string {
	trimmed := strings.Trim(strings.TrimSpace(hostIP), "[]")
	if trimmed == "" || trimmed == "0.0.0.0" {
		return "0.0.0.0"
	}
	if ip := net.ParseIP(trimmed); ip != nil {
		if ip.IsLoopback() && ip.To4() != nil {
			return "127.0.0.1"
		}
		return ip.String()
	}
	return trimmed
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
// containers, keyed by protocol+normalized-bind+host-port. Unpublished ports
// are ignored. Exact target collisions and wildcard/concrete listener
// collisions become visible failed forwards; Cairn never guesses a target.
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
			if reason, unsupported := unsupportedForwardReason(binding.HostIP); unsupported {
				candidate.blockedReason = reason
			}
			key := forwardKey(protocol, candidate.bindAddr, port)
			if existing, ok := out[key]; ok {
				if (existing.containerID == candidate.containerID && existing.containerName == candidate.containerName) || (existing.containerID == "" && existing.blockedReason != "") {
					continue
				}
				out[key] = conflictSpec(protocol, candidate.bindAddr, port)
				continue
			}
			out[key] = candidate
		}
	}

	// A wildcard listener overlaps every concrete IPv4 listener for the same
	// protocol and port. Refuse the entire ambiguous group before OS-dependent
	// bind order can choose a target.
	for key, candidate := range out {
		if candidate.bindAddr != "0.0.0.0" || candidate.blockedReason != "" {
			continue
		}
		for otherKey, other := range out {
			if otherKey == key || other.blockedReason != "" || other.protocol != candidate.protocol || other.hostPort != candidate.hostPort {
				continue
			}
			out[key] = conflictSpec(candidate.protocol, candidate.bindAddr, candidate.hostPort)
			out[otherKey] = conflictSpec(other.protocol, other.bindAddr, other.hostPort)
		}
	}
	return out
}

func conflictSpec(protocol string, bindAddr string, hostPort int) spec {
	return spec{
		protocol:      protocol,
		hostPort:      hostPort,
		bindAddr:      bindAddr,
		blockedReason: "Multiple containers publish overlapping host listeners; Cairn will not choose a forwarding target.",
	}
}
