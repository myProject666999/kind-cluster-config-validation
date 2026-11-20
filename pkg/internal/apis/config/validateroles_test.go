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

import "testing"

func roleNode(role NodeRole) Node {
	return Node{Role: role, Image: "myImage:latest"}
}

func TestRoleValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		nodes          []Node
		expectErrors   int
		expectWarnings int
		expectCodes    []string
	}{
		{
			name:           "control plane only cluster is legal",
			nodes:          []Node{roleNode(ControlPlaneRole)},
			expectErrors:   0,
			expectWarnings: 0,
		},
		{
			name:           "one control plane and workers are legal",
			nodes:          []Node{roleNode(ControlPlaneRole), roleNode(WorkerRole), roleNode(WorkerRole)},
			expectErrors:   0,
			expectWarnings: 0,
		},
		{
			name:           "missing control plane is a hard error",
			nodes:          []Node{roleNode(WorkerRole), roleNode(WorkerRole)},
			expectErrors:   1,
			expectWarnings: 0,
			expectCodes:    []string{codeMissingControlPlane},
		},
		{
			name:           "two control planes warn but do not error",
			nodes:          []Node{roleNode(ControlPlaneRole), roleNode(ControlPlaneRole)},
			expectErrors:   0,
			expectWarnings: 1,
			expectCodes:    []string{codeEvenControlPlaneCount},
		},
		{
			name:           "four control planes warn",
			nodes:          []Node{roleNode(ControlPlaneRole), roleNode(ControlPlaneRole), roleNode(ControlPlaneRole), roleNode(ControlPlaneRole)},
			expectErrors:   0,
			expectWarnings: 1,
			expectCodes:    []string{codeEvenControlPlaneCount},
		},
		{
			name:           "three control planes are quiet",
			nodes:          []Node{roleNode(ControlPlaneRole), roleNode(ControlPlaneRole), roleNode(ControlPlaneRole)},
			expectErrors:   0,
			expectWarnings: 0,
		},
		{
			name:           "unknown role is a hard error with the node field path",
			nodes:          []Node{roleNode(ControlPlaneRole), {Role: "bogus", Image: "myImage:latest"}},
			expectErrors:   1,
			expectWarnings: 0,
			expectCodes:    []string{codeInvalidRole},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster := defaultedCluster(IPv4Family, tc.nodes)
			result := ValidateCluster(cluster, nil)

			if result.ErrorCount != tc.expectErrors {
				t.Errorf("expected %d errors, got %d: %+v", tc.expectErrors, result.ErrorCount, result.Diagnostics)
			}
			if result.WarningCount != tc.expectWarnings {
				t.Errorf("expected %d warnings, got %d: %+v", tc.expectWarnings, result.WarningCount, result.Diagnostics)
			}
			var codes []string
			for _, d := range result.Diagnostics {
				codes = append(codes, d.Code)
			}
			if tc.expectCodes != nil && !equalStrings(codes, tc.expectCodes) {
				t.Errorf("expected codes %v, got %v", tc.expectCodes, codes)
			}
		})
	}
}

func TestEvenControlPlaneWarningIsClusterLevel(t *testing.T) {
	t.Parallel()
	cluster := defaultedCluster(IPv4Family, []Node{roleNode(ControlPlaneRole), roleNode(ControlPlaneRole)})
	result := ValidateCluster(cluster, nil)
	if result.WarningCount != 1 {
		t.Fatalf("expected one warning, got %+v", result.Diagnostics)
	}
	d := result.Diagnostics[0]
	if d.NodeIndex != -1 || d.Field != "nodes" || d.Code != codeEvenControlPlaneCount {
		t.Fatalf("warning must be cluster level on nodes, got %+v", d)
	}
}
