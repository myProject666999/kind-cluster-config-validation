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
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
	"testing"
)

func relativeMountNode() Node {
	return Node{
		Role:  WorkerRole,
		Image: "myImage:latest",
		ExtraMounts: []Mount{
			{HostPath: "relative/path", ContainerPath: "/data"},
		},
	}
}

func TestRenderTruncationKeepsAuthoritativeCounts(t *testing.T) {
	t.Parallel()
	// one control-plane plus 199 workers, each node carrying a relative
	// hostPath => exactly 200 hard errors and no role warnings
	nodes := []Node{{
		Role:  ControlPlaneRole,
		Image: "myImage:latest",
		ExtraMounts: []Mount{
			{HostPath: "relative/path", ContainerPath: "/data"},
		},
	}}
	for i := 0; i < 199; i++ {
		nodes = append(nodes, relativeMountNode())
	}
	if len(nodes) != 200 {
		t.Fatalf("test setup must produce 200 nodes, got %d", len(nodes))
	}
	cluster := defaultedCluster(IPv4Family, nodes)
	result := ValidateCluster(cluster, nil)

	if result.Total != 200 || result.ErrorCount != 200 || result.WarningCount != 0 {
		t.Fatalf("expected 200 error diagnostics, got total=%d errors=%d warnings=%d",
			result.Total, result.ErrorCount, result.WarningCount)
	}
	if result.Truncated {
		t.Fatal("the full result must never be marked truncated")
	}

	rendered := result.Render()
	if !rendered.Truncated {
		t.Fatal("rendered result must be marked truncated")
	}
	if len(rendered.Diagnostics) != MaxDiagnostics {
		t.Fatalf("expected exactly %d rendered diagnostics, got %d", MaxDiagnostics, len(rendered.Diagnostics))
	}
	// the last entry is the synthetic truncation diagnostic
	last := rendered.Diagnostics[MaxDiagnostics-1]
	if last.Code != codeTruncated || last.Severity != SeverityWarning {
		t.Fatalf("expected synthetic truncation warning last, got %+v", last)
	}
	// counts on the rendered view stay authoritative
	if rendered.ErrorCount != 200 || rendered.Total != 200 {
		t.Fatalf("counts must survive truncation, got total=%d errors=%d", rendered.Total, rendered.ErrorCount)
	}
	// the head is identical to the untruncated head, the tail is what is dropped
	for i := 0; i < MaxDiagnostics-1; i++ {
		if rendered.Diagnostics[i] != result.Diagnostics[i] {
			t.Fatalf("rendered head diverged at index %d", i)
		}
	}
	// hard-error decisions use the full result, so creation is still blocked
	if !result.HasErrors() || result.HardErrorsAggregate() == nil {
		t.Fatal("truncation must not hide hard errors")
	}
}

func TestRepeatedValidationIsByteIdentical(t *testing.T) {
	t.Parallel()
	cluster := defaultedCluster(IPv4Family, []Node{
		portNode(ControlPlaneRole, pm(80, 8443, "0.0.0.0", "TCP")),
		portNode(ControlPlaneRole, pm(80, 8443, "0.0.0.0", "TCP")),
		mountNode(Mount{HostPath: "rel", ContainerPath: "/x"}),
	})

	first, err := json.Marshal(ValidateCluster(cluster, nil).Render())
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(ValidateCluster(cluster, nil).Render())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("repeated validation differs:\n%s\nvs\n%s", first, second)
	}
}

var indexRE = regexp.MustCompile(`\[\d+\]`)

// indexAgnosticKey strips positional indices so diagnostics can be compared
// across node order shuffles.
func indexAgnosticKey(d Diagnostic) string {
	return string(d.Severity) + "|" + d.Code + "|" +
		indexRE.ReplaceAllString(d.Field, "[]") + "|" +
		indexRE.ReplaceAllString(d.SecondField, "[]") + "|" + string(d.Origin)
}

func sortedKeys(diags []Diagnostic) []string {
	keys := make([]string, len(diags))
	for i, d := range diags {
		keys[i] = indexAgnosticKey(d)
	}
	sort.Strings(keys)
	return keys
}

func TestShuffledNodeOrderGivesEquivalentDiagnostics(t *testing.T) {
	t.Parallel()
	original := defaultedCluster(IPv4Family, []Node{
		portNode(WorkerRole, pm(80, 8443, "0.0.0.0", "TCP")),
		portNode(WorkerRole, pm(80, 8443, "127.0.0.1", "TCP")),
		mountNode(Mount{HostPath: "rel", ContainerPath: "/x"}),
	})
	shuffled := defaultedCluster(IPv4Family, []Node{
		original.Nodes[2], original.Nodes[0], original.Nodes[1],
	})

	a := ValidateCluster(original, nil)
	b := ValidateCluster(shuffled, nil)

	if a.ErrorCount != b.ErrorCount || a.WarningCount != b.WarningCount {
		t.Fatalf("counts differ after shuffle: %+v vs %+v", a, b)
	}
	ka, kb := sortedKeys(a.Diagnostics), sortedKeys(b.Diagnostics)
	if !equalStrings(ka, kb) {
		t.Fatalf("diagnostics differ after shuffle apart from node indices:\n%v\nvs\n%v", ka, kb)
	}

	// the relative-mount node moved from index 2 to index 0; its diagnostic
	// must follow it with the updated node index
	found := false
	for _, d := range b.Diagnostics {
		if d.Code == codeRelativeMountPath {
			if d.NodeIndex != 0 || d.Field != "nodes[0].extraMounts[0].hostPath" {
				t.Fatalf("shuffled diagnostic does not track the node: %+v", d)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("relative mount diagnostic missing after shuffle")
	}
}

func TestDiagnosticOrderingIsDeterministic(t *testing.T) {
	t.Parallel()
	cluster := defaultedCluster(IPv4Family, []Node{
		roleNode(ControlPlaneRole),
		roleNode(ControlPlaneRole), // even control-plane warning
		mountNode(Mount{HostPath: "rel", ContainerPath: "/x"}),
	})
	result := ValidateCluster(cluster, nil)

	// errors must all precede warnings, and node indices must be ascending
	lastSeverityRank := -1
	lastNodeIndex := -100
	for _, d := range result.Diagnostics {
		rank := severityRank(d.Severity)
		if rank < lastSeverityRank {
			t.Fatalf("severity ordering violated: %+v", result.Diagnostics)
		}
		if rank == lastSeverityRank && d.NodeIndex < lastNodeIndex {
			t.Fatalf("node index ordering violated: %+v", result.Diagnostics)
		}
		lastSeverityRank = rank
		lastNodeIndex = d.NodeIndex
	}
}
