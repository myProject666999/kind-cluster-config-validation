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

// Origin classifies where a configuration value came from.
type Origin string

const (
	// OriginExplicit means the field was physically present in the source.
	OriginExplicit Origin = "explicit"
	// OriginDefaulted means the field was absent from the source and received
	// its value from static defaulting.
	OriginDefaulted Origin = "defaulted"
	// OriginUnknown means no source information is available, for instance
	// when the cluster was constructed in memory as a Go struct.
	OriginUnknown Origin = "unknown"
)

// FieldPresence records the field paths physically present in the source
// configuration, E.G. "nodes[0].extraPortMappings[1].hostPort".
type FieldPresence map[string]struct{}

// Provenance stores source locations captured while parsing the
// configuration. A nil *Provenance is valid and reports OriginUnknown for
// every field.
//
// Runtime-dynamic values (E.G. a host port of 0 or -1) are never treated as
// static defaults: provenance only describes what was written in the
// configuration before defaulting.
type Provenance struct {
	present FieldPresence
}

// NewProvenance builds a Provenance from the given field presence set.
func NewProvenance(present FieldPresence) *Provenance {
	if present == nil {
		return nil
	}
	return &Provenance{present: present}
}

// Has reports whether the field path was physically present in the source.
func (p *Provenance) Has(field string) bool {
	if p == nil {
		return false
	}
	_, ok := p.present[field]
	return ok
}

// Origin resolves the origin of a field. Without provenance information the
// origin cannot be determined; with provenance, an absent field is one that
// received a statically defaulted value.
func (p *Provenance) Origin(field string) Origin {
	if p == nil {
		return OriginUnknown
	}
	if p.Has(field) {
		return OriginExplicit
	}
	return OriginDefaulted
}
