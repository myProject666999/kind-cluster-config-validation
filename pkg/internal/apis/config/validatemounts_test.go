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

func mountNode(mounts ...Mount) Node {
	// control-plane role keeps the mount tests independent of role rules
	return Node{
		Role:        ControlPlaneRole,
		Image:       "myImage:latest",
		ExtraMounts: mounts,
	}
}

func TestMountValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		mounts         []Mount
		expectErrors   int
		expectWarnings int
		expectCodes    []string
		expectFields   []string
	}{
		{
			name: "absolute paths are valid",
			mounts: []Mount{
				{HostPath: "/host/data", ContainerPath: "/data"},
			},
			expectErrors: 0,
		},
		{
			name: "relative hostPath is a hard error",
			mounts: []Mount{
				{HostPath: "relative/data", ContainerPath: "/data"},
			},
			expectErrors: 1,
			expectCodes:  []string{codeRelativeMountPath},
			expectFields: []string{"nodes[0].extraMounts[0].hostPath"},
		},
		{
			name: "relative containerPath is a hard error",
			mounts: []Mount{
				{HostPath: "/host/data", ContainerPath: "relative/data"},
			},
			expectErrors: 1,
			expectCodes:  []string{codeRelativeMountPath},
			expectFields: []string{"nodes[0].extraMounts[0].containerPath"},
		},
		{
			name: "both relative paths produce two hard errors with distinct fields",
			mounts: []Mount{
				{HostPath: "host", ContainerPath: "container"},
			},
			expectErrors: 2,
			expectCodes:  []string{codeRelativeMountPath, codeRelativeMountPath},
			expectFields: []string{
				"nodes[0].extraMounts[0].containerPath",
				"nodes[0].extraMounts[0].hostPath",
			},
		},
		{
			name: "exact duplicate mount declaration is a single warning",
			mounts: []Mount{
				{HostPath: "/host/data", ContainerPath: "/data", Readonly: true},
				{HostPath: "/host/data", ContainerPath: "/data", Readonly: true},
			},
			expectErrors:   0,
			expectWarnings: 1,
			expectCodes:    []string{codeDuplicateMount},
			expectFields:   []string{"nodes[0].extraMounts[1].containerPath"},
		},
		{
			name: "same destination with differing hostPath is a hard conflict",
			mounts: []Mount{
				{HostPath: "/host/a", ContainerPath: "/data"},
				{HostPath: "/host/b", ContainerPath: "/data"},
			},
			expectErrors: 1,
			expectCodes:  []string{codeMountConflict},
			expectFields: []string{"nodes[0].extraMounts[1].containerPath"},
		},
		{
			name: "same destination with differing readOnly option is a hard conflict",
			mounts: []Mount{
				{HostPath: "/host/a", ContainerPath: "/data", Readonly: false},
				{HostPath: "/host/a", ContainerPath: "/data", Readonly: true},
			},
			expectErrors: 1,
			expectCodes:  []string{codeMountConflict},
		},
		{
			name: "same destination with differing propagation is a hard conflict",
			mounts: []Mount{
				{HostPath: "/host/a", ContainerPath: "/data", Propagation: MountPropagationNone},
				{HostPath: "/host/a", ContainerPath: "/data", Propagation: MountPropagationHostToContainer},
			},
			expectErrors: 1,
			expectCodes:  []string{codeMountConflict},
		},
		{
			name: "nested ancestor and descendant container paths are legal",
			mounts: []Mount{
				{HostPath: "/host/root", ContainerPath: "/data"},
				{HostPath: "/host/sub", ContainerPath: "/data/sub"},
			},
			expectErrors: 0,
		},
		{
			name: "same hostPath at different container paths is legal",
			mounts: []Mount{
				{HostPath: "/host/shared", ContainerPath: "/a"},
				{HostPath: "/host/shared", ContainerPath: "/b"},
			},
			expectErrors: 0,
		},
		{
			name: "three identical mounts still warn only once",
			mounts: []Mount{
				{HostPath: "/host/a", ContainerPath: "/data"},
				{HostPath: "/host/a", ContainerPath: "/data"},
				{HostPath: "/host/a", ContainerPath: "/data"},
			},
			expectErrors:   0,
			expectWarnings: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cluster := defaultedCluster(IPv4Family, []Node{mountNode(tc.mounts...)})
			result := ValidateCluster(cluster, nil)

			if result.ErrorCount != tc.expectErrors {
				t.Errorf("expected %d errors, got %d: %+v", tc.expectErrors, result.ErrorCount, result.Diagnostics)
			}
			if result.WarningCount != tc.expectWarnings {
				t.Errorf("expected %d warnings, got %d: %+v", tc.expectWarnings, result.WarningCount, result.Diagnostics)
			}
			var codes, fields []string
			for _, d := range result.Diagnostics {
				codes = append(codes, d.Code)
				fields = append(fields, d.Field)
			}
			if tc.expectCodes != nil && !equalStrings(codes, tc.expectCodes) {
				t.Errorf("expected codes %v, got %v", tc.expectCodes, codes)
			}
			if tc.expectFields != nil && !equalStrings(fields, tc.expectFields) {
				t.Errorf("expected fields %v, got %v", tc.expectFields, fields)
			}
		})
	}
}

func TestMountConflictsAreScopedToNode(t *testing.T) {
	t.Parallel()
	// the same container path on different nodes is always legal because
	// each node is an independent container
	cluster := defaultedCluster(IPv4Family, []Node{
		mountNode(Mount{HostPath: "/host/a", ContainerPath: "/data"}),
		{
			Role:        WorkerRole,
			Image:       "myImage:latest",
			ExtraMounts: []Mount{{HostPath: "/host/b", ContainerPath: "/data"}},
		},
	})
	result := ValidateCluster(cluster, nil)
	if result.ErrorCount != 0 || result.WarningCount != 0 {
		t.Fatalf("cross-node mount overlap must be legal, got %+v", result.Diagnostics)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
