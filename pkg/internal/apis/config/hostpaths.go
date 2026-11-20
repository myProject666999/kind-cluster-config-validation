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
	stderrors "errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

// FileInfo is the subset of os.FileInfo used by the host path precheck.
// IsDir and Mode are kept even though current findings are all warnings:
// a future configuration API that declares the expected path type can turn
// type mismatches into errors without changing this interface.
type FileInfo interface {
	IsDir() bool
	Mode() os.FileMode
}

// HostPathInspector abstracts filesystem access for the optional local path
// precheck so that tests can inject an in-memory implementation. Production
// code uses OSHostPathInspector backed by os.Stat.
type HostPathInspector interface {
	Stat(path string) (FileInfo, error)
}

// OSHostPathInspector inspects the real local filesystem.
type OSHostPathInspector struct{}

// Stat implements HostPathInspector via os.Stat (symlinks are followed,
// matching how container runtimes resolve bind mount sources).
func (OSHostPathInspector) Stat(path string) (FileInfo, error) {
	return os.Stat(path)
}

// HostPathFinding is a single environment-dependent finding from the local
// host path precheck. These are kept separate from the static diagnostics
// because their outcome depends on the machine and the moment of the check,
// so they must never participate in snapshot comparisons.
//
// The current Mount API does not declare whether a path should be a file or
// a directory, so every finding is a warning: a missing source would be
// auto-created as a directory by the container runtime, permission failures
// may still work under elevated privileges, and writability is advisory.
type HostPathFinding struct {
	Severity  Severity `json:"severity"`
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	NodeIndex int      `json:"nodeIndex"`
	Field     string   `json:"field"`
	HostPath  string   `json:"hostPath"`
}

// HostPathResult contains only local precheck findings.
type HostPathResult struct {
	Findings []HostPathFinding `json:"hostPathFindings"`
}

// HasFindings reports whether any local finding was produced.
func (r *HostPathResult) HasFindings() bool {
	return len(r.Findings) > 0
}

// CheckHostPaths runs the optional, explicitly enabled local precheck using
// the injected inspector. Only host paths that passed the static absolute
// path validation are inspected; relative paths are left to the static
// hard-error diagnostics. The function performs no validation itself beyond
// filesystem inspection and never blocks creation on its own.
func CheckHostPaths(c *Cluster, inspector HostPathInspector) *HostPathResult {
	result := &HostPathResult{Findings: []HostPathFinding{}}
	if inspector == nil {
		inspector = OSHostPathInspector{}
	}

	for ni := range c.Nodes {
		node := &c.Nodes[ni]
		for mi := range node.ExtraMounts {
			mount := &node.ExtraMounts[mi]
			// only inspect statically valid absolute paths
			if mount.HostPath == "" || !isAbsPath(mount.HostPath) {
				continue
			}
			field := mountField(ni, mi, "hostPath")
			info, err := inspector.Stat(mount.HostPath)
			switch {
			case err == nil:
				if !mount.Readonly && info.Mode().Perm()&0o200 == 0 {
					result.add(HostPathFinding{
						Severity:  SeverityWarning,
						Code:      codeHostPathNotWritable,
						Message:   fmt.Sprintf("hostPath %q is not writable by its owner but the mount is not read-only", mount.HostPath),
						NodeIndex: ni,
						Field:     field,
						HostPath:  mount.HostPath,
					})
				}
			case stderrors.Is(err, fs.ErrNotExist):
				result.add(HostPathFinding{
					Severity:  SeverityWarning,
					Code:      codeHostPathMissing,
					Message:   fmt.Sprintf("hostPath %q does not exist; the container runtime may create it as a directory", mount.HostPath),
					NodeIndex: ni,
					Field:     field,
					HostPath:  mount.HostPath,
				})
			case stderrors.Is(err, fs.ErrPermission):
				result.add(HostPathFinding{
					Severity:  SeverityWarning,
					Code:      codeHostPathPermission,
					Message:   fmt.Sprintf("hostPath %q is not accessible: %v", mount.HostPath, err),
					NodeIndex: ni,
					Field:     field,
					HostPath:  mount.HostPath,
				})
			default:
				result.add(HostPathFinding{
					Severity:  SeverityWarning,
					Code:      codeHostPathPermission,
					Message:   fmt.Sprintf("failed to inspect hostPath %q: %v", mount.HostPath, err),
					NodeIndex: ni,
					Field:     field,
					HostPath:  mount.HostPath,
				})
			}
		}
	}

	sortHostPathFindings(result.Findings)
	return result
}

func (r *HostPathResult) add(finding HostPathFinding) {
	r.Findings = append(r.Findings, finding)
}

// sortHostPathFindings keeps local findings deterministic as well.
func sortHostPathFindings(findings []HostPathFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.NodeIndex != b.NodeIndex {
			return a.NodeIndex < b.NodeIndex
		}
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		return a.Message < b.Message
	})
}
