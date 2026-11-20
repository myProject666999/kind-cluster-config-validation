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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/kind/pkg/cmd"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
	"sigs.k8s.io/kind/pkg/log"
)

const validConfigYAML = `
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
`

const portConflictYAML = `
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 80
    hostPort: 8443
    listenAddress: 0.0.0.0
    protocol: TCP
- role: worker
  extraPortMappings:
  - containerPort: 80
    hostPort: 8443
    listenAddress: 0.0.0.0
    protocol: TCP
`

const evenControlPlaneYAML = `
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: control-plane
`

const relativeMountYAML = `
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraMounts:
  - hostPath: relative/path
    containerPath: /data
`

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return path
}

func runCommand(t *testing.T, args []string, stdin string) (string, string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	streams := cmd.IOStreams{
		In:     strings.NewReader(stdin),
		Out:    out,
		ErrOut: errOut,
	}
	command := NewCommand(log.NoopLogger{}, streams)
	command.SetArgs(args)
	command.SetOut(out)
	command.SetErr(errOut)
	err := command.Execute()
	return out.String(), errOut.String(), err
}

func TestValidateConfigTextValid(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, validConfigYAML)
	out, _, err := runCommand(t, []string{"--config", path}, "")
	if err != nil {
		t.Fatalf("valid config must not error, got %v", err)
	}
	if !strings.Contains(out, "summary: 0 error(s)") {
		t.Fatalf("unexpected text output: %s", out)
	}
}

func TestValidateConfigPortConflictFailsWithStructuredFields(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, portConflictYAML)
	out, _, err := runCommand(t, []string{"--config", path, "--output", "json"}, "")
	if err == nil {
		t.Fatal("a hard port conflict must fail the command")
	}
	var payload config.Result
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output must be valid JSON: %v\n%s", err, out)
	}
	if payload.ErrorCount != 1 || payload.Total != 1 {
		t.Fatalf("expected one conflict, got %+v", payload)
	}
	d := payload.Diagnostics[0]
	if d.Code != "PORT_CONFLICT" || d.NodeIndex != 0 || d.SecondNodeIndex != 1 {
		t.Fatalf("unexpected diagnostic: %+v", d)
	}
	if d.Origin != config.OriginExplicit {
		t.Fatalf("explicit YAML field must be marked explicit, got %s", d.Origin)
	}
}

func TestValidateConfigWarningsDoNotFail(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, evenControlPlaneYAML)
	out, _, err := runCommand(t, []string{"--config", path, "--output", "json"}, "")
	if err != nil {
		t.Fatalf("warnings alone must not fail, got %v", err)
	}
	var payload config.Result
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.WarningCount != 1 || payload.ErrorCount != 0 {
		t.Fatalf("expected one warning and no errors, got %+v", payload)
	}
	if payload.Diagnostics[0].Code != "EVEN_CONTROL_PLANE_COUNT" {
		t.Fatalf("unexpected diagnostic: %+v", payload.Diagnostics[0])
	}
}

func TestValidateConfigRelativePathHardError(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, relativeMountYAML)
	out, _, err := runCommand(t, []string{"--config", path}, "")
	if err == nil {
		t.Fatal("a relative mount path must fail validation")
	}
	if !strings.Contains(out, "RELATIVE_MOUNT_PATH") && !strings.Contains(out, "must be an absolute path") {
		t.Fatalf("output must explain the relative path error, got %s", out)
	}
}

func TestValidateConfigReadsStdin(t *testing.T) {
	t.Parallel()
	out, _, err := runCommand(t, []string{"--config", "-", "--output", "json"}, validConfigYAML)
	if err != nil {
		t.Fatalf("stdin config must validate, got %v", err)
	}
	if !strings.Contains(out, "\"totalDiagnostics\": 0") {
		t.Fatalf("unexpected stdin output: %s", out)
	}
}

func TestValidateConfigHostPathPrecheckIsSeparate(t *testing.T) {
	t.Parallel()
	// a valid static config referencing a nonexistent host path
	yaml := `
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraMounts:
  - hostPath: /definitely/not/here/kind-validation
    containerPath: /data
`
	path := writeTempConfig(t, yaml)

	// without the flag, no host path findings and success
	out, _, err := runCommand(t, []string{"--config", path, "--output", "json"}, "")
	if err != nil {
		t.Fatalf("static validation must pass, got %v", err)
	}
	if strings.Contains(out, "hostPathFindings") {
		t.Fatalf("host path findings must be absent without the flag: %s", out)
	}

	// with the flag, a separate warning section appears but still exits zero
	out, _, err = runCommand(t, []string{"--config", path, "--output", "json", "--check-host-paths"}, "")
	if err != nil {
		t.Fatalf("a missing-path warning must not fail the command, got %v", err)
	}
	if !strings.Contains(out, "HOST_PATH_MISSING") || !strings.Contains(out, "hostPathFindings") {
		t.Fatalf("expected a separate host path finding, got %s", out)
	}
}

func TestValidateConfigRejectsUnknownOutputFormat(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, validConfigYAML)
	if _, _, err := runCommand(t, []string{"--config", path, "--output", "xml"}, ""); err == nil {
		t.Fatal("an unsupported output format must error")
	}
}

func TestValidateConfigRequiresConfigFlag(t *testing.T) {
	t.Parallel()
	if _, _, err := runCommand(t, nil, ""); err == nil {
		t.Fatal("missing --config must error")
	}
}
