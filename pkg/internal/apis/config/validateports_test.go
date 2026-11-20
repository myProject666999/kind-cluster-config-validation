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
	"reflect"
	"testing"
)

// portNode builds a defaulted node with the given port mappings.
func portNode(role NodeRole, mappings ...PortMapping) Node {
	n := Node{Role: role, Image: "myImage:latest", ExtraPortMappings: mappings}
	SetDefaultsNode(&n)
	return n
}

func pm(containerPort, hostPort int32, address, protocol string) PortMapping {
	return PortMapping{
		ContainerPort: containerPort,
		HostPort:      hostPort,
		ListenAddress: address,
		Protocol:      PortMappingProtocol(protocol),
	}
}

// defaultedCluster builds a fully defaulted cluster so that validation only
// reports the rule under test, not the missing cluster wide defaults.
func defaultedCluster(ipFamily ClusterIPFamily, nodes []Node) *Cluster {
	cluster := &Cluster{Name: "kind", Nodes: nodes}
	cluster.Networking.IPFamily = ipFamily
	SetDefaultsCluster(cluster)
	return cluster
}

func TestPortConflictValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		ipFamily      ClusterIPFamily
		nodes         []Node
		expectErrors  int
		expectWarning int
		expectCodes   []string
	}{
		{
			name:     "two nodes same host port both on 0.0.0.0 conflict once",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "0.0.0.0", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "0.0.0.0", "TCP")),
			},
			expectErrors: 1,
			expectCodes:  []string{codePortConflict},
		},
		{
			name:     "same port TCP and UDP do not conflict",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "0.0.0.0", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "0.0.0.0", "UDP")),
			},
			expectErrors: 0,
			expectCodes:  nil,
		},
		{
			name:     "0.0.0.0 conflicts with specific 127.0.0.1",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "0.0.0.0", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "127.0.0.1", "TCP")),
			},
			expectErrors: 1,
			expectCodes:  []string{codePortConflict},
		},
		{
			name:     "specific 127.0.0.1 conflicts with later 0.0.0.0 regardless of order",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "127.0.0.1", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "0.0.0.0", "TCP")),
			},
			expectErrors: 1,
			expectCodes:  []string{codePortConflict},
		},
		{
			name:     "two distinct specific IPv4 addresses do not conflict",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "127.0.0.1", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "192.168.1.2", "TCP")),
			},
			expectErrors: 0,
		},
		{
			name:     "IPv4 and IPv6 wildcards coexist on the same port",
			ipFamily: DualStackFamily,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "0.0.0.0", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "::", "TCP")),
			},
			expectErrors: 0,
		},
		{
			name:     "IPv6 wildcard conflicts with specific IPv6 address",
			ipFamily: IPv6Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "::", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "::1", "TCP")),
			},
			expectErrors: 1,
			expectCodes:  []string{codePortConflict},
		},
		{
			name:     "IPv6 wildcard does not conflict with IPv4 specific address",
			ipFamily: DualStackFamily,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "::", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "127.0.0.1", "TCP")),
			},
			expectErrors: 0,
		},
		{
			name:     "multiple port zero mappings never conflict",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 0, "0.0.0.0", "TCP"), pm(443, 0, "0.0.0.0", "TCP")),
				portNode(WorkerRole, pm(80, 0, "0.0.0.0", "TCP"), pm(443, 0, "0.0.0.0", "TCP")),
			},
			expectErrors: 0,
		},
		{
			name:     "minus one host ports are dynamic and never conflict",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, -1, "0.0.0.0", "TCP")),
				portNode(WorkerRole, pm(80, -1, "0.0.0.0", "TCP")),
			},
			expectErrors: 0,
		},
		{
			name:     "identical worker declarations without fixed ports are legal",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 0, "", "TCP")),
				portNode(WorkerRole, pm(80, 0, "", "TCP")),
			},
			expectErrors: 0,
		},
		{
			name:     "same container port on different host ports is legal",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8080, "0.0.0.0", "TCP"), pm(80, 9090, "0.0.0.0", "TCP")),
			},
			expectErrors: 0,
		},
		{
			name:     "empty listen address defaults to IPv4 wildcard and conflicts",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "127.0.0.1", "TCP")),
			},
			expectErrors: 1,
			expectCodes:  []string{codePortConflict},
		},
		{
			name:     "empty listen address in IPv6 family defaults to IPv6 wildcard",
			ipFamily: IPv6Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "::1", "TCP")),
			},
			expectErrors: 1,
			expectCodes:  []string{codePortConflict},
		},
		{
			name:     "intra node duplicate binding is reported",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "127.0.0.1", "TCP"), pm(81, 8443, "127.0.0.1", "TCP")),
			},
			expectErrors: 1,
			expectCodes:  []string{codePortConflict},
		},
		{
			name:     "invalid listen address is a hard error, not a conflict",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "not-an-ip", "TCP")),
				portNode(WorkerRole, pm(80, 8443, "not-an-ip", "TCP")),
			},
			expectErrors: 2,
			expectCodes:  []string{codeInvalidListenAddress, codeInvalidListenAddress},
		},
		{
			name:     "unknown protocol is a hard error",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 8443, "0.0.0.0", "FOO")),
			},
			expectErrors: 1,
			expectCodes:  []string{codeInvalidPortProtocol},
		},
		{
			name:     "out of range host port is a hard error",
			ipFamily: IPv4Family,
			nodes: []Node{
				portNode(ControlPlaneRole, pm(80, 70000, "0.0.0.0", "TCP")),
			},
			expectErrors: 1,
			expectCodes:  []string{codeInvalidPort},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster := defaultedCluster(tc.ipFamily, tc.nodes)
			result := ValidateCluster(cluster, nil)

			if result.ErrorCount != tc.expectErrors {
				t.Errorf("expected %d errors, got %d: %+v", tc.expectErrors, result.ErrorCount, result.Diagnostics)
			}
			if result.WarningCount != tc.expectWarning {
				t.Errorf("expected %d warnings, got %d: %+v", tc.expectWarning, result.WarningCount, result.Diagnostics)
			}
			var codes []string
			for _, d := range result.Diagnostics {
				if d.Severity == SeverityError {
					codes = append(codes, d.Code)
				}
			}
			if tc.expectCodes != nil && !reflect.DeepEqual(codes, tc.expectCodes) {
				t.Errorf("expected error codes %v, got %v", tc.expectCodes, codes)
			}
		})
	}
}

func TestPortConflictReportedOnceWithLowerNodeFirst(t *testing.T) {
	t.Parallel()
	cluster := defaultedCluster(IPv4Family, []Node{
		portNode(ControlPlaneRole, pm(80, 8443, "0.0.0.0", "TCP")),
		portNode(WorkerRole, pm(80, 8443, "0.0.0.0", "TCP")),
	})
	result := ValidateCluster(cluster, nil)
	if result.ErrorCount != 1 {
		t.Fatalf("expected exactly one conflict diagnostic, got %d: %+v", result.ErrorCount, result.Diagnostics)
	}
	d := result.Diagnostics[0]
	if d.Code != codePortConflict || d.NodeIndex != 0 || d.SecondNodeIndex != 1 {
		t.Fatalf("conflict must reference the lower node first, got %+v", d)
	}
	if d.Field != "nodes[0].extraPortMappings[0].listenAddress" ||
		d.SecondField != "nodes[1].extraPortMappings[0].listenAddress" {
		t.Fatalf("unexpected field paths: %s / %s", d.Field, d.SecondField)
	}
}
