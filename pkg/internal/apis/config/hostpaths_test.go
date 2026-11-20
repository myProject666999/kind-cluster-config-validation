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
	"io/fs"
	"os"
	"testing"
)

// fakeFileInfo is an in-memory FileInfo for host path precheck tests.
type fakeFileInfo struct {
	dir  bool
	mode os.FileMode
}

func (f fakeFileInfo) IsDir() bool       { return f.dir }
func (f fakeFileInfo) Mode() os.FileMode { return f.mode }

// fakeInspector never touches the real filesystem.
type fakeInspector struct {
	infos map[string]FileInfo
	errs  map[string]error
	calls []string
}

func (f *fakeInspector) Stat(path string) (FileInfo, error) {
	f.calls = append(f.calls, path)
	if err, ok := f.errs[path]; ok {
		return nil, err
	}
	if info, ok := f.infos[path]; ok {
		return info, nil
	}
	return nil, stderrors.New("unexpected path in fake inspector")
}

func TestCheckHostPaths(t *testing.T) {
	t.Parallel()
	cluster := &Cluster{
		Nodes: []Node{
			{
				Role:  WorkerRole,
				Image: "myImage:latest",
				ExtraMounts: []Mount{
					{HostPath: "/missing", ContainerPath: "/missing"},
					{HostPath: "/denied", ContainerPath: "/denied"},
					{HostPath: "/readonly", ContainerPath: "/readonly"},
					{HostPath: "/healthy", ContainerPath: "/healthy"},
					{HostPath: "relative/skipped", ContainerPath: "/skip"},
				},
			},
		},
	}
	inspector := &fakeInspector{
		infos: map[string]FileInfo{
			"/readonly": fakeFileInfo{mode: 0o555},
			"/healthy":  fakeFileInfo{mode: 0o755},
		},
		errs: map[string]error{
			"/missing": fs.ErrNotExist,
			"/denied":  fs.ErrPermission,
		},
	}

	result := CheckHostPaths(cluster, inspector)

	// relative paths are left entirely to the static hard-error diagnostics
	for _, call := range inspector.calls {
		if call == "relative/skipped" {
			t.Fatal("relative host paths must not be inspected")
		}
	}

	byCode := map[string]int{}
	for _, finding := range result.Findings {
		if finding.Severity != SeverityWarning {
			t.Fatalf("phase B findings are all warnings, got %+v", finding)
		}
		byCode[finding.Code]++
	}
	if byCode[codeHostPathMissing] != 1 {
		t.Errorf("expected one missing finding, got %d", byCode[codeHostPathMissing])
	}
	if byCode[codeHostPathPermission] != 1 {
		t.Errorf("expected one permission finding, got %d", byCode[codeHostPathPermission])
	}
	if byCode[codeHostPathNotWritable] != 1 {
		t.Errorf("expected one not-writable finding, got %d", byCode[codeHostPathNotWritable])
	}
	if len(result.Findings) != 3 {
		t.Fatalf("expected exactly three findings, got %+v", result.Findings)
	}
}

func TestCheckHostPathsReadonlyMountSkipsWritableCheck(t *testing.T) {
	t.Parallel()
	cluster := &Cluster{
		Nodes: []Node{
			{
				Role:  WorkerRole,
				Image: "myImage:latest",
				ExtraMounts: []Mount{
					{HostPath: "/readonly", ContainerPath: "/ro", Readonly: true},
				},
			},
		},
	}
	inspector := &fakeInspector{
		infos: map[string]FileInfo{"/readonly": fakeFileInfo{mode: 0o555}},
	}
	if result := CheckHostPaths(cluster, inspector); result.HasFindings() {
		t.Fatalf("read-only mount on a non-writable path must be clean, got %+v", result.Findings)
	}
}

func TestCheckHostPathsIsDeterministic(t *testing.T) {
	t.Parallel()
	cluster := &Cluster{
		Nodes: []Node{
			{
				Role:  WorkerRole,
				Image: "myImage:latest",
				ExtraMounts: []Mount{
					{HostPath: "/b", ContainerPath: "/b"},
					{HostPath: "/a", ContainerPath: "/a"},
				},
			},
		},
	}
	inspector := &fakeInspector{errs: map[string]error{
		"/a": fs.ErrNotExist,
		"/b": fs.ErrNotExist,
	}}
	first := CheckHostPaths(cluster, inspector)
	second := CheckHostPaths(cluster, inspector)
	// ordering follows the deterministic field path key (declaration order),
	// not the host path string
	if len(first.Findings) != 2 || first.Findings[0].HostPath != "/b" {
		t.Fatalf("findings must be deterministically ordered by field path, got %+v", first.Findings)
	}
	for i := range first.Findings {
		if first.Findings[i] != second.Findings[i] {
			t.Fatal("repeated host path checks differ")
		}
	}
}
