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
	"path"
	"path/filepath"
)

// isAbsPath reports whether p is an absolute POSIX or native path. This
// mirrors pkg/fs.IsAbs without importing that package name here, where it
// would collide with io/fs in sibling files.
func isAbsPath(p string) bool {
	return path.IsAbs(p) || filepath.IsAbs(p)
}

// validateMounts performs the purely static mount checks:
//
//   - both hostPath and containerPath must be absolute paths. Relative
//     paths are hard errors: historically the container runtime providers
//     resolved them against kind's own working directory at provision time,
//     which made the resulting mount depend on where kind was invoked.
//   - on the same node, multiple mounts sharing a containerPath are either an
//     exact duplicate declaration (all five mount fields equal) => warning,
//     or a genuine conflict (same destination, different source/options) =>
//     hard error, matching the container runtime's "duplicate mount point"
//     rejection.
//
// Nested ancestor/descendant container paths are legal: container runtimes
// support stacked bind mounts and configurations rely on them (E.G. a
// read-only subdirectory under a writable root). The same hostPath mounted at
// different container paths is legal, as is any relationship across
// different nodes (each node is an independent container). None of these
// produce a diagnostic.
//
// This function never touches the host filesystem; existence, type and
// permission checks live in the optional CheckHostPaths phase instead.
func (v *validator) validateMounts() {
	for ni := range v.cluster.Nodes {
		node := &v.cluster.Nodes[ni]
		for mi := range node.ExtraMounts {
			mount := &node.ExtraMounts[mi]

			hostPathField := mountField(ni, mi, "hostPath")
			if !isAbsPath(mount.HostPath) {
				v.addError(
					codeRelativeMountPath, ni, hostPathField, v.origin(hostPathField),
					fmt.Sprintf("hostPath %q must be an absolute path", mount.HostPath),
				)
			}

			containerPathField := mountField(ni, mi, "containerPath")
			if !isAbsPath(mount.ContainerPath) {
				v.addError(
					codeRelativeMountPath, ni, containerPathField, v.origin(containerPathField),
					fmt.Sprintf("containerPath %q must be an absolute path", mount.ContainerPath),
				)
			}
		}

		v.validateMountDestinations(ni, node.ExtraMounts)
	}
}

// validateMountDestinations groups a node's mounts by containerPath while
// preserving declaration order, then classifies each repeated destination.
func (v *validator) validateMountDestinations(ni int, mounts []Mount) {
	// group indices by containerPath, first occurrence stays first
	groups := map[string][]int{}
	order := []string{}
	for mi := range mounts {
		containerPath := mounts[mi].ContainerPath
		if _, seen := groups[containerPath]; !seen {
			order = append(order, containerPath)
		}
		groups[containerPath] = append(groups[containerPath], mi)
	}

	for _, containerPath := range order {
		indices := groups[containerPath]
		if len(indices) < 2 {
			continue
		}
		first := mounts[indices[0]]
		allIdentical := true
		for _, mi := range indices[1:] {
			if !mountsEqual(first, mounts[mi]) {
				allIdentical = false
				break
			}
		}

		if allIdentical {
			// one warning per duplicate group, attached to the second
			// declaration; further identical declarations stay silent
			secondField := mountField(ni, indices[1], "containerPath")
			v.addWarning(
				codeDuplicateMount, ni, secondField, v.origin(secondField),
				fmt.Sprintf("duplicate mount declaration at containerPath %q", containerPath),
			)
			continue
		}

		// every declaration deviating from the first one is a hard conflict
		baselineField := mountField(ni, indices[0], "containerPath")
		for _, mi := range indices[1:] {
			if mountsEqual(first, mounts[mi]) {
				continue
			}
			field := mountField(ni, mi, "containerPath")
			v.addError(
				codeMountConflict, ni, field, v.origin(field),
				fmt.Sprintf(
					"containerPath %q is mounted more than once with differing hostPath or options, conflicting with %s",
					containerPath, baselineField,
				),
			)
		}
	}
}

// mountsEqual reports whether two mounts describe the exact same bind mount,
// including read-only, SELinux relabeling and propagation options.
func mountsEqual(a, b Mount) bool {
	return a.ContainerPath == b.ContainerPath &&
		a.HostPath == b.HostPath &&
		a.Readonly == b.Readonly &&
		a.SelinuxRelabel == b.SelinuxRelabel &&
		a.Propagation == b.Propagation
}
