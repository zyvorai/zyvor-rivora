# AI review (rivora)

_Generated 2026-10-20 by claude-opus-5-5 from a snapshot of tracked sources; verify before acting._

# Code review: rivora

## Summary

Rivora is an XDP/TCX Layer-4 load balancer written in Go and eBPF. It runs in two modes:

- **Bare metal:** VIPs come from a static YAML file.
- **Kubernetes:** a `rivorad` DaemonSet runs on every node, alongside a leader-elected `rivora-controller` that does IPAM from `AddressPool`.

Around the dataplane it offers full-NAT, L2 DSR and L3 DSR forwarding, weighted Maglev, an ARP/NDP speaker, gobgp-based BGP/BFD, Gateway API L4 routes, and a React console. It is packaged as a Helm chart, with an embedded copy of that chart driven by the `rivora` CLI.

## Bugs and correctness risks

- **`autoAssign: false` is silently ignored** (`deploy/helm/rivora/templates/addresspools.yaml`, and the copy in `internal/installer/chartdata/...`).
  - The template uses `autoAssign: {{ .autoAssign | default true }}`.
  - Sprig's `default` treats `false` as empty, so every seeded pool renders `autoAssign: true`.
  - Use `{{ if hasKey . "autoAssign" }}{{ .autoAssign }}{{ else }}true{{ end }}`, or `ternary`.
- **`rivora upgrade` breaks the documented flow** (`internal/installer/install.go`).
  - `Upgrade` calls `RequireInterface(opts.Values)`, which checks only the *user overrides*.
  - The README example `rivora upgrade --set addressPools[1].name=v6 ...` therefore fails with "rivorad.interface is required".
  - `action.NewUpgrade` is also run without `ReuseValues`/`ResetThenReuseValues`. Even with `--set rivorad.interface=...`, previously set values (pools, BGP, keys) would revert to chart defaults.
  - Either reuse values and validate the merged result, or document that the full values set must be re-supplied.
- **The uninstall advisory is stale** (`internal/installer/install.go`, `crdAdvisory`).
  - It mentions only the AddressPool CRD. The chart now ships three CRDs (AddressPool, ServicePolicy, BGPPeer).
  - It links `#the-crd-is-not-managed-by-upgradeuninstall`, but the heading is now "The CRDs are not managed…", which gives `#the-crds-are-...`.
  - It tells users of a prebuilt binary to run `kubectl delete -f deploy/helm/rivora/...`, a path they won't have locally.
- **The Maglev size class ignores weights** (`internal/dataplane/maglevalloc.go`, `internal/maglev/maglev.go`).
  - `maglevClassFor` sizes the table from `backendCount * 100`.
  - `BuildTable` creates one virtual entry *per unit of weight* and errors when `m <= total weight`.
  - Weights go up to 1000 (`ServicePolicy.weights`, `MaxBackendWeight`). So 2 backends at weight 1000 gets a 1031 table and fails, and 66+ such backends exceed even 65537.
  - Distribution quality also degrades well before that limit. I couldn't see the caller, so verify whether it sizes by total weight.
  - Separately, `uint32(backendCount) * 100` can overflow for absurd counts.
- **The NDP responder has protocol gaps** (`internal/speaker/ndp.go`).
  - It replies to `from` unconditionally. A Neighbor Solicitation from the unspecified address `::` (Duplicate Address Detection, or DAD) must be answered to all-nodes multicast with `Solicited=false` (RFC 4861 §7.2.4). Replying to `::` will fail or be meaningless.
  - The control message is discarded (`msg, _, from, err`), so hop-limit 255 is never enforced (see Security concerns).
  - Solicited-node groups are joined but never left when a VIP is removed (acknowledged in a comment).
- **BGP `Snapshot` may report stale or racy peer data** (`internal/bgp/snapshot.go`).
  - It iterates `sp.cfg.Peers` without holding a lock.
  - Peers added at runtime via `SetPeers`/`BGPPeer` may not appear in `rivora_bgp_peer_up` if `cfg.Peers` isn't updated. I'm uncertain; it depends on code not shown.
  - Matching `p.State.NeighborAddress` against the configured string is not normalized. A non-canonical IPv6 address in config would show as `UNKNOWN`.
- **A failed range update can drop the existing range** (`internal/dataplane/portrange.go`).
  - On a partial failure, `rangeMapWrite` deletes the blocks it already wrote.
  - When updating an existing range VIP (`UpdateAny` overwrites existing keys), this removes blocks that were serving traffic before the update. A failed update can therefore drop part of a previously-working range.
- **The rivorad Secret always sets every env var, even when empty** (`templates/rivorad-secret.yaml`).
  - `RIVORA_API_KEY`, `RIVORA_TLS_CERT` and the rest are emitted as `""` even when unset.
  - The README says rivorad refuses "a setting holding no usable key". If presence-based checks exist anywhere, empty-but-set vars could trip them. Worth a test.
- **The console fetch helper assumes a JSON body** (`web/src/api.ts`).
  - `r.json()` runs on every OK response, so any 204/empty-body endpoint would throw.
- **The vite dev proxy breaks on plain HTTP** (`web/vite.config.ts`). It hardcodes an `https://` target, so `npm run dev` fails against a plain-HTTP rivorad, which is the default.

## Security concerns

- **The NDP path is cheap to flood** (`speaker/ndp.go`).
  - `owns6` calls `source.Statuses()` (a full BPF-map read) for *every* received Neighbor Solicitation targeting a joined group. This is a cheap CPU-amplification vector from anyone on the L2 segment. Cache the VIP set and refresh it on change instead.
  - Hop-limit 255 is not validated, so off-link spoofed NDP is accepted.
- **The self-signed certificate is a CA and doesn't match remote use** (`internal/tlsutil/selfsigned.go`).
  - The serving certificate has `IsCA: true` and `KeyUsageCertSign`. A leaf serving cert should not be a CA.
  - The SANs cover only localhost and loopback, yet `config/examples/remote-api.yaml` and `scripts/install-systemd.sh` expose the API on `0.0.0.0` with this cert.
  - The script then tells operators to "accept the browser warning, or curl -k". That trains users to ignore MITM warnings on an admin API that can drain backends.
  - Recommend real certs, or at least printing the fingerprint for pinning.
- **API keys pass through plain Helm values.** `rivorad.apiKey` and `apiReadOnlyKey` end up in the release record and in `helm get values`. Support an `existingSecret` option.
- **`install-systemd.sh` opens the API to the world.** It opens `9870/tcp` in ufw for every source.
- **The controller ClusterRole is broad.** It grants `update`/`patch` on all `services` cluster-wide (needed for finalizers). The rivorad Secrets Role grants `get` on *all* Secrets in the release namespace. That is acceptable as documented, but scope it with `resourceNames` if feasible.
- **Other notes:**
  - `SECURITY.md` acknowledges that there is no fuzzing of packet, config or BGP parsing.
  - It also acknowledges an accepted gobgp DoS (GO-2026-4736) that is reachable from configured peers.

## Test and CI gaps

- **Chart templates have no render tests.** The `autoAssign` bug would be caught by a `helm template`-plus-assert or helm-unittest case. BGP flag rendering (`-bgp-peers=` when empty, `configSecret` branches) is also untested.
- **`internal/installer/chart_test.go` is too loose.** It asserts only `len(CRDObjects()) > 0`, and its comment still refers to a single CRD. Assert all three by name.
- **The `Upgrade`/values flow is untested.** No test covers it, including the `RequireInterface`-on-overrides behaviour. The README's own `rivora upgrade` example would fail.
- **Maglev sizing under weights is untested.** No test combines `maglevClassFor` with weighted backends at realistic `MaxBackendWeight`.
- **NDP edge cases are untested.** There is no test for a DAD solicitation (`::` source), a bad hop limit, or a non-owned target.
- **`internal/config/examples_test.go` is weak.** It only checks that `Interface` is non-empty after `Load`. Assert that `Validate()` explicitly passes, or that the expected VIP counts are present.
- **`ratelimit_test.go` is thin.** It doesn't verify the per-CPU division it claims.
- **The console has no tests** (`web/`), and nothing checks that the committed `internal/api/ui` bundle matches the source. A CI check is mentioned in the docs, but I can't verify it from the files shown.
- **No fuzz targets exist**, matching the gap noted in `SECURITY.md`. Config parsing and port-range tiling are easy first candidates.

## Documentation gaps

- **The chart README misstates Helm convention.** It claims `crds/` is "not `templates/crds/`, which is Helm's documented convention". In fact `crds/` *is* Helm's documented convention. This appears in both copies.
- **`crds.md` and the CRD files disagree.** `crds.md` lists no short name for AddressPool, but the CRD defines `rap`. Its `avoidBuggyIPs` wording ("IPv4 CIDR's .0/.255") also differs from the CRD description ("a /24 subnet's").
- **The documented upgrade flow conflicts with the CLI.** The top-level README's `rivora upgrade` example contradicts current CLI behaviour (see Bugs).
- **The single-VIP restriction is out of date.** `multi-vip.yaml` says `rivoractl status` works only with exactly
