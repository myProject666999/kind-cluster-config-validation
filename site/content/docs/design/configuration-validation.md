# Configuration Validation

Status: design reference for the pre-creation validation available via
`kind validate config` and applied automatically during `kind create cluster`.

## Goal

Invalid multi-node configurations must be rejected **before** any container
runtime is invoked. For example, when two nodes map host port 8443 on
overlapping listen scopes, kind now reports a single aggregated error at
validation time instead of failing partway through node provisioning and
leaving a half-created cluster behind.

Validation has two phases with deliberately different guarantees:

1. **Static validation** — pure configuration analysis. It never contacts the
   container runtime, host network or host filesystem. Validating the same
   configuration twice produces byte-identical output, so results can be used
   in snapshot comparisons and CI caches.
2. **Optional local path precheck** — explicitly enabled with
   `--check-host-paths`. It inspects the local filesystem through an
   injectable interface and is environment dependent, so its findings are
   reported in a separate section and never mixed into the static
   diagnostics.

## Entry points

- `kind validate config --config <file|-> [--output text|json]
  [--check-host-paths]` validates without creating anything. Exit code is
  non-zero only when hard errors exist; warnings alone succeed.
- `kind create cluster` runs the same static validator after defaulting and
  before provisioning. Warnings are written to the log so they remain
  searchable in CI; hard errors abort creation.
- Library callers can use `config.ValidateCluster(cluster, provenance)` and
  `config.CheckHostPaths(cluster, inspector)`.

## Diagnostics

Every diagnostic carries a severity (`error`/`warning`), a stable machine
readable code, a human message, the zero-based node index (-1 for cluster
level), a structured field path (for example
`nodes[2].extraPortMappings[0].hostPort`), and the field origin
(`explicit`, `defaulted`, or `unknown`). Diagnostics spanning two nodes
report the lower-indexed node as the primary location and the other node as
the secondary location; each pair is reported exactly once.

The full result is deterministically ordered by severity, node index, field
path, message and secondary location. Rendering is capped at
`MaxDiagnostics` (100): the first 99 diagnostics are kept and a synthetic
`TRUNCATED` warning carrying the total count is appended. Truncation only
affects rendering — the complete diagnostic list and the authoritative error
and warning counts are retained, so truncation can never turn a failing
configuration into a passing one.

## Static rules

### Port mappings

Conflicts are computed across **all nodes**, not just within a single node.
Two fixed host-port bindings conflict only when they share the address
family, protocol and non-zero host port and their listen scopes overlap:

- the same protocol and port on `0.0.0.0` and any specific IPv4 address
  conflict; two distinct specific IPv4 addresses do not;
- the same rule applies within IPv6, where `::` is the wildcard and only
  overlaps IPv6 addresses;
- IPv4 and IPv6 wildcard scopes are handled separately using the structural
  address family (`ip.To4()`), never `net.IP.Equal`. In Go `::` and
  `0.0.0.0` compare equal, which previously conflated the two families; a
  dual-stack configuration may now bind `0.0.0.0:80/TCP` and `[::]:80/TCP`
  together legally;
- different protocols (TCP vs UDP vs SCTP) never conflict;
- host port `0` (kind selects a free port) and `-1` (the runtime selects) are
  dynamic values and never take part in fixed-port conflicts;
- an empty `listenAddress` is normalized per cluster IP family (`0.0.0.0`
  for IPv4/dual-stack, `::` for IPv6) and an empty protocol defaults to TCP,
  matching the container runtime providers;
- mapping the same **container** port to several different **host** ports is
  legal: each binding installs an independent host-side proxy rule. This is
  intentional and covered by regression tests.

Identical worker declarations are therefore not conflicts by themselves —
only actually overlapping normalized `(family, address, protocol, non-zero
port)` bindings are reported.

### Mounts

- Both `hostPath` and `containerPath` must be absolute. Relative paths are a
  hard error. **This is a behavior change**: previously the docker, podman
  and nerdctl providers resolved relative host paths against kind's own
  working directory at provision time, so the resulting mount depended on
  where kind was invoked. Use absolute paths to keep the configuration
  self-contained.
- On the same node, repeated mounts at one `containerPath` are classified as:
  - an exact duplicate declaration (all five fields including `readOnly`,
    `selinuxRelabel` and `propagation` match) — one `DUPLICATE_MOUNT`
    warning per duplicate group;
  - a genuine conflict (same destination, different source or options) — a
    hard `MOUNT_CONFLICT` error, matching the runtime's "duplicate mount
    point" rejection.
- Nested mounts at ancestor and descendant container paths (for example
  `/data` and `/data/ro`) are legal and silent: runtimes support stacked
  bind mounts and configurations rely on them.
- The same `hostPath` mounted at different container paths is legal, and any
  relationship across different nodes is legal because each node is an
  independent container.

### Node roles

- At least one `control-plane` node is required; a control-plane-only cluster
  is legal.
- An unknown role string is a hard error.
- A positive even number of control-plane nodes produces a warning only. An
  even etcd voting membership cannot form a quorum when one member is down.
  Creation still succeeds because kind provisions an implicit external load
  balancer for every multi control-plane cluster; the warning stays
  consistent with that HA behavior.

### Defaults and provenance

Static defaults are applied before validation, in the same order as during
cluster creation. A YAML parse-time provenance walk records which field paths
were physically present, so diagnostics distinguish explicitly written
fields from default-generated ones. Runtime-dynamic values (random ports,
backend-selected ports) are never treated as static defaults. Configurations
constructed in memory without source YAML report origin `unknown`.

## Local path precheck (phase B)

`CheckHostPaths` accepts an injectable `HostPathInspector` (the default uses
`os.Stat`); tests use an in-memory fake. Only host paths that already passed
the static absolute-path check are inspected. The current `Mount` API has no
field declaring whether a path should be a file or a directory, so a
"wrong type" condition cannot be defined; all precheck findings are warnings:

- a missing path (the runtime would typically create it as a directory);
- an inaccessible path (permission error on `Stat`);
- a writable mount whose source has no owner write bit.

The interface retains `IsDir()` and `Mode()` so a future API revision that
declares expected path types can promote type mismatches to errors without
changing callers.

## Compatibility

### Always invalid, now detected earlier (hard errors)

These configurations were never usable; validation only moves the failure
earlier, before any node is created:

- overlapping fixed host-port bindings within or across nodes;
- unknown node role or port mapping protocol, invalid listen address or port;
- missing control-plane node;
- conflicting mounts at the same container destination;
- **relative mount paths** — the one case with a changed observable outcome.
  Historical configs relying on working-directory resolution must switch to
  absolute paths.

### Suspicious but historically accepted (warnings only)

- even control-plane node count;
- exactly duplicated mount declarations.

### Intentionally not decided statically

The following depend on the environment or on values chosen at runtime and
are therefore excluded from static validation so that results stay
deterministic and machine independent:

- whether a host port is already in use by another process (checked during
  creation);
- host-port conflicts between different kind clusters (creation time);
- host path existence, type and permissions (phase B, opt-in);
- whether a node image exists or can be pulled;
- provider-specific capabilities and differences;
- the actual values of dynamic ports (`0` / `-1`);
- collisions with the implicit API-server port mapping, which the providers
  append during provisioning and which is not part of the validated config.
