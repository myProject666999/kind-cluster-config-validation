/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package config

import (
	"fmt"
	"net"
	"sort"
)

// ipFamilyKind is the structural address family of a listen address.
// It must never be derived from net.IP.Equal: in Go the unspecified
// addresses "::" and "0.0.0.0" compare equal, which would conflate the
// IPv6 and IPv4 wildcard scopes. See https://github.com/golang/go/issues/48723
type ipFamilyKind int

const (
	familyIPv4 ipFamilyKind = iota
	familyIPv6
)

// hostBinding is a normalized, conflict-checking view of a port mapping.
// Normalization happens on this local copy only; the validated cluster is
// never mutated, so it can be validated repeatedly with identical results.
type hostBinding struct {
	family    ipFamilyKind
	address   string // canonical net.IP.String(), family specific
	wildcard  bool   // unspecified address within the family
	hostPort  int32
	protocol  PortMappingProtocol
	nodeIndex int
	mapIndex  int
	field     string
}

// validatePortMappings performs port range, address and protocol checks and
// detects overlapping fixed host port bindings both within and across nodes.
//
// Conflict rule: two bindings conflict only when they share the address
// family, protocol and non-zero host port and their listen scopes overlap.
// A wildcard address (0.0.0.0 for IPv4, :: for IPv6) overlaps every address
// of its own family and no address of the other family. Two distinct
// specific addresses never overlap.
//
// Host ports 0 (kind picks a free port) and -1 (the container runtime picks)
// are runtime-dynamic and never take part in fixed port conflicts.
func (v *validator) validatePortMappings() {
	// buckets: family -> host port -> protocol -> bindings in declaration order
	buckets := map[ipFamilyKind]map[int32]map[PortMappingProtocol][]hostBinding{}

	for ni := range v.cluster.Nodes {
		node := &v.cluster.Nodes[ni]
		for mi := range node.ExtraPortMappings {
			pm := &node.ExtraPortMappings[mi]
			hostPortField := portMappingField(ni, mi, "hostPort")
			containerPortField := portMappingField(ni, mi, "containerPort")

			if err := validatePort(pm.HostPort); err != nil {
				v.addError(codeInvalidPort, ni, hostPortField, v.origin(hostPortField), err.Error())
			}
			if err := validatePort(pm.ContainerPort); err != nil {
				v.addError(codeInvalidPort, ni, containerPortField, v.origin(containerPortField), err.Error())
			}

			// protocol: empty means TCP, matching the container runtime
			// defaulting in each provider's generatePortMappings.
			protocolField := portMappingField(ni, mi, "protocol")
			protocol := pm.Protocol
			protocolOrigin := v.origin(protocolField)
			if protocol == "" {
				protocol = PortMappingProtocolTCP
				protocolOrigin = OriginDefaulted
			}
			switch protocol {
			case PortMappingProtocolTCP, PortMappingProtocolUDP, PortMappingProtocolSCTP:
			default:
				v.addError(
					codeInvalidPortProtocol, ni, protocolField, protocolOrigin,
					fmt.Sprintf("unknown port mapping protocol: %v", pm.Protocol),
				)
				continue
			}

			// dynamic host ports cannot conflict statically
			if pm.HostPort == 0 || pm.HostPort == -1 {
				continue
			}

			binding, ok := v.normalizeBinding(pm, ni, mi, protocol)
			if !ok {
				continue
			}

			if _, ok := buckets[binding.family]; !ok {
				buckets[binding.family] = map[int32]map[PortMappingProtocol][]hostBinding{}
			}
			if _, ok := buckets[binding.family][binding.hostPort]; !ok {
				buckets[binding.family][binding.hostPort] = map[PortMappingProtocol][]hostBinding{}
			}
			key := buckets[binding.family][binding.hostPort]
			key[protocol] = append(key[protocol], binding)
		}
	}

	v.detectPortConflicts(buckets)
}

// normalizeBinding canonicalizes the listen address of a fixed port
// mapping. The second return value is false when the binding cannot be
// normalized (an invalid address or an unknown IP family); a diagnostic is
// already emitted in that case.
func (v *validator) normalizeBinding(pm *PortMapping, ni, mi int, protocol PortMappingProtocol) (hostBinding, bool) {
	addressField := portMappingField(ni, mi, "listenAddress")
	rawAddress := pm.ListenAddress
	addressOrigin := v.origin(addressField)

	if rawAddress == "" {
		// mirror the provider level defaulting, which depends on the cluster
		// IP family; the family itself is defaulted before validation runs
		switch v.cluster.Networking.IPFamily {
		case IPv4Family, DualStackFamily:
			rawAddress = "0.0.0.0"
		case IPv6Family:
			rawAddress = "::"
		default:
			// the invalid ipFamily is reported by the networking checks;
			// without a known family the wildcard scope is undefined, so the
			// binding is skipped rather than guessed.
			return hostBinding{}, false
		}
		addressOrigin = OriginDefaulted
	}

	ip := net.ParseIP(rawAddress)
	if ip == nil {
		v.addError(
			codeInvalidListenAddress, ni, addressField, addressOrigin,
			fmt.Sprintf("invalid listen address: %s", pm.ListenAddress),
		)
		return hostBinding{}, false
	}

	family := familyIPv6
	if ip.To4() != nil {
		family = familyIPv4
	}

	return hostBinding{
		family:    family,
		address:   ip.String(),
		wildcard:  ip.IsUnspecified(),
		hostPort:  pm.HostPort,
		protocol:  protocol,
		nodeIndex: ni,
		mapIndex:  mi,
		field:     addressField,
	}, true
}

// detectPortConflicts walks the buckets in a deterministic order and reports
// each conflicting pair exactly once. Bindings keep their declaration order,
// and pairs are generated with the lower ordinal first, so a conflict
// between two nodes is never reported both as "A vs B" and "B vs A".
func (v *validator) detectPortConflicts(buckets map[ipFamilyKind]map[int32]map[PortMappingProtocol][]hostBinding) {
	families := make([]ipFamilyKind, 0, len(buckets))
	for family := range buckets {
		families = append(families, family)
	}
	sort.Slice(families, func(i, j int) bool { return families[i] < families[j] })

	for _, family := range families {
		ports := make([]int32, 0, len(buckets[family]))
		for port := range buckets[family] {
			ports = append(ports, port)
		}
		sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })

		for _, port := range ports {
			protocolMap := buckets[family][port]
			protocols := make([]PortMappingProtocol, 0, len(protocolMap))
			for protocol := range protocolMap {
				protocols = append(protocols, protocol)
			}
			sort.Slice(protocols, func(i, j int) bool { return protocols[i] < protocols[j] })

			for _, protocol := range protocols {
				bindings := protocolMap[protocol]
				for i := 0; i < len(bindings); i++ {
					for j := i + 1; j < len(bindings); j++ {
						first, second := bindings[i], bindings[j]
						if first.address != second.address && !first.wildcard && !second.wildcard {
							continue
						}
						v.addPortConflict(first, second)
					}
				}
			}
		}
	}
}

func (v *validator) addPortConflict(first, second hostBinding) {
	message := fmt.Sprintf(
		"host port %d/%s is bound by both %s (%s) and %s (%s)",
		first.hostPort, first.protocol,
		first.field, first.address,
		second.field, second.address,
	)
	v.diags = append(v.diags, Diagnostic{
		Severity:        SeverityError,
		Code:            codePortConflict,
		Message:         message,
		NodeIndex:       first.nodeIndex,
		Field:           first.field,
		Origin:          v.origin(first.field),
		SecondNodeIndex: second.nodeIndex,
		SecondField:     second.field,
	})
}
