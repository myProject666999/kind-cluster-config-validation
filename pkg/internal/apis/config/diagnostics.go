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
	"sort"

	"sigs.k8s.io/kind/pkg/errors"
)

// Severity classifies a diagnostic.
type Severity string

const (
	// SeverityError identifies diagnostics that block cluster creation.
	SeverityError Severity = "error"
	// SeverityWarning identifies diagnostics that do not block creation but
	// are surfaced to the user (and kept in searchable CI logs).
	SeverityWarning Severity = "warning"
)

// Stable diagnostic codes, suitable for consumption by editors and scripts.
const (
	// cluster / networking level
	codeInvalidName           = "INVALID_NAME"
	codeInvalidAPIServerPort  = "INVALID_API_SERVER_PORT"
	codeInvalidIPFamily       = "INVALID_IP_FAMILY"
	codeInvalidSubnet         = "INVALID_SUBNET"
	codeInvalidKubeProxyMode  = "INVALID_KUBE_PROXY_MODE"
	codeMissingControlPlane   = "MISSING_CONTROL_PLANE"
	codeEvenControlPlaneCount = "EVEN_CONTROL_PLANE_COUNT"
	codeInvalidRole           = "INVALID_ROLE"
	codeImageRequired         = "IMAGE_REQUIRED"
	codeInvalidPort           = "INVALID_PORT"
	codeInvalidListenAddress  = "INVALID_LISTEN_ADDRESS"
	codeInvalidPortProtocol   = "INVALID_PORT_PROTOCOL"
	codePortConflict          = "PORT_CONFLICT"
	codeRelativeMountPath     = "RELATIVE_MOUNT_PATH"
	codeMountConflict         = "MOUNT_CONFLICT"
	codeDuplicateMount        = "DUPLICATE_MOUNT"
	codeTruncated             = "TRUNCATED"
	codeHostPathMissing       = "HOST_PATH_MISSING"
	codeHostPathPermission    = "HOST_PATH_PERMISSION"
	codeHostPathNotWritable   = "HOST_PATH_NOT_WRITABLE"
)

// Diagnostic is a single structured configuration finding.
//
// The JSON representation contains slices only (no maps), which makes the
// serialized output byte-identical across repeated validations of the same
// configuration and therefore suitable for snapshot comparison.
type Diagnostic struct {
	// Severity is "error" or "warning".
	Severity Severity `json:"severity"`
	// Code is a stable, machine readable identifier of the rule that produced
	// the diagnostic.
	Code string `json:"code"`
	// Message is the human readable explanation.
	Message string `json:"message"`
	// NodeIndex is the zero based index of the node the diagnostic refers to,
	// or -1 for cluster level diagnostics.
	NodeIndex int `json:"nodeIndex"`
	// Field is the structured field path, E.G.
	// "nodes[2].extraPortMappings[0].hostPort". It is empty for cluster level
	// diagnostics that are not tied to a single field.
	Field string `json:"field"`
	// Origin records whether the referenced value was explicitly set,
	// filled in by defaulting or cannot be determined.
	Origin Origin `json:"origin"`
	// SecondNodeIndex is the node index on the other side of a relationship
	// spanning two nodes (E.G. a port conflict). It is omitted otherwise.
	SecondNodeIndex int `json:"secondNodeIndex,omitempty"`
	// SecondField is the field path on the other side of a relationship
	// spanning two nodes. It is omitted otherwise.
	SecondField string `json:"secondField,omitempty"`
}

// Result holds the outcome of a validation run.
//
// Diagnostics is the full, deterministically ordered list of findings.
// ErrorCount and WarningCount always reflect the full result even when the
// diagnostic list is truncated for rendering: callers making decisions
// (process exit code, blocking cluster creation) must use the counts rather
// than the length of the rendered slice.
type Result struct {
	// Diagnostics is the full, ordered list of diagnostics (never truncated
	// in place; use Render to obtain a bounded view).
	Diagnostics []Diagnostic `json:"diagnostics"`
	// Truncated reports whether Render drops diagnostics.
	Truncated bool `json:"truncated"`
	// Total is the total number of diagnostics before truncation.
	Total int `json:"totalDiagnostics"`
	// ErrorCount is the authoritative number of error severity diagnostics.
	ErrorCount int `json:"errorCount"`
	// WarningCount is the authoritative number of warning severity
	// diagnostics, including the synthetic truncation diagnostic.
	WarningCount int `json:"warningCount"`
}

// MaxDiagnostics bounds the number of diagnostics in a rendered result.
// When the bound is exceeded, the first MaxDiagnostics-1 diagnostics are
// kept (after the deterministic ordering) and a single synthetic truncation
// diagnostic carrying the total count is appended. The tail is always the
// part that is dropped.
const MaxDiagnostics = 100

// severityRank orders errors before warnings so that blocking problems come
// first in every rendered view.
func severityRank(s Severity) int {
	if s == SeverityError {
		return 0
	}
	return 1
}

// sortDiagnostics applies the total, deterministic ordering:
//  1. severity (errors before warnings)
//  2. node index (-1 / cluster level first, then ascending)
//  3. field path (lexicographic)
//  4. message (lexicographic)
//  5. second node index and second field path
//
// Every component is positional or derived from fixed strings, so shuffling
// the node order yields identical diagnostics apart from node indices.
func sortDiagnostics(diags []Diagnostic) {
	sort.SliceStable(diags, func(i, j int) bool {
		a, b := diags[i], diags[j]
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra < rb
		}
		if a.NodeIndex != b.NodeIndex {
			return a.NodeIndex < b.NodeIndex
		}
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		if a.Message != b.Message {
			return a.Message < b.Message
		}
		if a.SecondNodeIndex != b.SecondNodeIndex {
			return a.SecondNodeIndex < b.SecondNodeIndex
		}
		return a.SecondField < b.SecondField
	})
}

// finalize sorts the diagnostics and computes the authoritative counts. It
// does not mutate the configuration under validation.
func (r *Result) finalize() *Result {
	sortDiagnostics(r.Diagnostics)
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityError {
			r.ErrorCount++
		} else {
			r.WarningCount++
		}
	}
	r.Total = len(r.Diagnostics)
	r.Truncated = false
	return r
}

// Render returns a bounded copy of the result suitable for display or
// serialization. Truncation is a rendering only operation: the receiver
// keeps the full diagnostic list and authoritative counts.
func (r *Result) Render() *Result {
	out := &Result{
		Diagnostics:  append([]Diagnostic(nil), r.Diagnostics...),
		Truncated:    r.Truncated,
		Total:        r.Total,
		ErrorCount:   r.ErrorCount,
		WarningCount: r.WarningCount,
	}
	if len(out.Diagnostics) <= MaxDiagnostics {
		return out
	}
	out.Truncated = true
	kept := MaxDiagnostics - 1
	out.Diagnostics = append(
		out.Diagnostics[:kept:kept],
		Diagnostic{
			Severity:  SeverityWarning,
			Code:      codeTruncated,
			Message:   fmt.Sprintf("additional diagnostics omitted: %d of %d diagnostics shown", MaxDiagnostics, r.Total),
			NodeIndex: -1,
			Origin:    OriginUnknown,
		},
	)
	return out
}

// HasErrors reports whether the full result contains hard errors.
func (r *Result) HasErrors() bool {
	return r.ErrorCount > 0
}

// Warnings returns the warning severity diagnostics of the full result, in
// deterministic order.
func (r *Result) Warnings() []Diagnostic {
	out := make([]Diagnostic, 0, r.WarningCount)
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityWarning {
			out = append(out, d)
		}
	}
	return out
}

// HardErrorsAggregate returns an aggregate of the error severity
// diagnostics, or nil when there are none. It preserves the historical
// error-returning validation API while warnings stay non-blocking.
func (r *Result) HardErrorsAggregate() error {
	errs := make([]error, 0, r.ErrorCount)
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityError {
			errs = append(errs, errors.New(d.Message))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.NewAggregate(errs)
}

// Field path builders. The grammar is shared with the YAML provenance
// walker so that diagnostics and origin lookups always address the same
// paths:
//
//	cluster level: networking.apiServerPort, name, nodes
//	node level:    nodes[2].role
//	               nodes[0].extraPortMappings[1].hostPort
//	               nodes[0].extraMounts[2].containerPath
func nodeField(nodeIndex int, field string) string {
	return fmt.Sprintf("nodes[%d].%s", nodeIndex, field)
}

func portMappingField(nodeIndex, mappingIndex int, field string) string {
	return fmt.Sprintf("nodes[%d].extraPortMappings[%d].%s", nodeIndex, mappingIndex, field)
}

func mountField(nodeIndex, mountIndex int, field string) string {
	return fmt.Sprintf("nodes[%d].extraMounts[%d].%s", nodeIndex, mountIndex, field)
}
