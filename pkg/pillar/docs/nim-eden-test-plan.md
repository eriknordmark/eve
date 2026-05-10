# Eden test plan: NIM and supporting packages

This document plans new Eden integration tests aimed at improving functional
*and* statement coverage of `pkg/pillar/cmd/nim` and the packages it composes
(`dpcmanager`, `dpcreconciler`, `conntester`, `netmonitor`,
`controllerconn`, parts of `cipher` and `controllerconn` reachable from NIM).

It complements — and explicitly does not duplicate — the unit tests added in
PR #5901, which cover item-level reconciliation
(`dpcreconciler/{generic,linux}items`, `nireconciler/{generic,linux}items`)
and parser-level helpers in `netmonitor`.

Phase 1 of the plan below is being implemented in lf-edge/eden#1165 (open,
draft as of this writing). Eden script names in that PR may differ slightly
from the names suggested here; once it merges the per-test entries should be
revisited and marked accordingly.

## How this plan was scoped

Two surfaces drove the gap analysis:

1. **What unit tests already cover** (so the eden plan does not duplicate
   them):
   * `pkg/pillar/dpcmanager/dpcmanager_test.go` — DPC fallback,
     multi-eth, DNS, wireless (mocked), AddDPC during verify, assigned
     interfaces, deletion, released/renamed interface, vlans/bonds (mocked),
     transient DNS error, old DPC, RemoteTemporaryFailure, DPC list
     compression, no-DPC-for-bootstrap, time limit, lastresort updates, IPv6,
     DHCP overrides (gateway/IPs/DNS/NTP), LPS merge logic, route metrics, and
     PNAC DHCP reacquire variants.
   * `pkg/pillar/dpcreconciler/linux_test.go` — empty args, single eth,
     multiple eths same subnet, wireless config rendering, vlans/bonds,
     kube ACE/service rules, IPv6 single eth.
   * PR #5901 additions in `dpcreconciler/{generic,linux}items`,
     `nireconciler/{generic,linux}items`, `netmonitor`.
   * `pkg/pillar/netmonitor/linux_test.go` — DHCPv4/v6 lease parsing.

2. **What `cmd/nim` and `dpcmanager` actually do at runtime** that *cannot*
   be covered by unit tests because it depends on:
   * real pubsub interactions across multiple agents (zedagent, monitor,
     mmagent, vaultmgr, scepclient, zedkube, domainmgr);
   * the `/config` partition layout, `bootstrap-config.pb` and
     `/persist/checkpoint/lastconfig`;
   * a real Linux netlink/`/proc` stack, real DHCP clients, real wpa_supplicant,
     real iptables;
   * real reboots, including with persisted `DevicePortConfigList`;
   * Eden-SDN's ability to emulate broken/asymmetric uplinks for connectivity
     tester behaviour.

## Existing eden coverage of NIM

Before adding tests we acknowledge what already runs:

* `tests/eclient/testdata/radio_silence.txt` — radio silence message exchange
  via local profile server (no actual radio off because no wireless adapter in
  CI).
* `tests/eclient/testdata/network_local_changes.txt` — LPS local network
  changes (DNS override on eth0, MTU on eth1, permission gating via
  `AllowLocalModifications`).
* `tests/eclient/testdata/publish_location.txt` — wwan-published location flow
  (static fixture-driven; not a full LTE flow).
* `tests/eclient/testdata/host-only.txt`,
  `tests/eclient/testdata/air-gapped-switch.txt`,
  `tests/eclient/testdata/port_switch.txt`,
  `tests/eclient/testdata/nw_switch.txt` — all *NetworkInstance* level
  scenarios using SDN forwarders; they touch NIM only incidentally (as the
  uplink owner).
* `tests/network/testdata/switch_net_vlans.txt` — VLANs at the *NI* level
  inside the device, not on uplinks.
* `tests/network/testdata/vlans_and_bonds.txt` — currently `skip`'d with TODO
  to rewrite for SDN; not running.

The plan below treats the SDN-rewrite of `vlans_and_bonds.txt` and a sibling
PNAC test as in-scope, since they are needed to cover `cmd/nim`'s
`publishBondMetrics` and `publishPNACMetrics` end-to-end.

## Cross-cutting fixtures

Several scenarios share common setup; defining them once keeps individual
scripts terse.

* **`eclient` image** with `ssh`, `jq`, `curl`, `dig`, `iproute2`. Already
  used by existing `eclient` tests.
* **SDN model variants** under
  `tests/network/testdata/<scenario>/sdn-model.json` — provide controlled
  uplink topologies (single eth, two eths, eth+eth-with-no-internet,
  eth+8021x, eth-bond, eth-bond-with-vlan, ipv6 only, dual-stack).
* **`wait-for-network-info.sh`** — reuse the pattern from
  `network_local_changes.txt`: an LPS-provided JSON status file is polled
  with a `jq` selector until the expected value is observed.
* **`peek-pubsub.sh`** — small helper invoked over `eden eve ssh` that
  `cat`s a `/run/<agent>/<topic>/<key>.json` and pipes through `jq`. Many
  assertions are most reliably made against pubsub publications.
* **`peek-persist.sh`** — same idea against `/persist/status/nim/...`.
* **`force-reboot.sh`** — `eden eve reboot` followed by SDN re-attachment
  and `wait-for-controller-reachable`.
* **`override-json.sh`** — drop a generated DPC into `/config/DevicePortConfig/`
  *before* boot using SDN-side mounts (or `eden eve ssh` for online tests
  that probe the runtime directory `/run/global/DevicePortConfig/`).

Each per-area section below points at which fixtures it reuses.

## Coverage targets

The plan specifically targets these (currently uncovered or
poorly-covered) code paths. Each test references back to a target so the
mapping is explicit.

| ID  | Code path / observable behaviour                                                                                             | Today's coverage              |
| --- | ----------------------------------------------------------------------------------------------------------------------------- | ----------------------------- |
| C1  | `cmd/nim/nim.go` `ingestDevicePortConfig*` (override.json/usb.json copy-from-`/config`)                                       | None                          |
| C2  | `cmd/nim/nim.go` `hasPersistLastconfig` + bootstrap-vs-lastconfig branching (the full startup matrix R1–R6 above)             | None                          |
| C2b | `cmd/nim/nim.go` `expectBootstrapDPCs` wait-loop and `dpcmanager.Run(ctx, expectBootstrapDPCs=true)` against a real bootstrap pb | None                          |
| C3  | `cmd/nim/nim.go` `runResolverCacheForController` and `resolveAndCacheIP` (DNS caching behaviour)                              | None                          |
| C4  | `cmd/nim/nim.go` `publishPNACMetrics` (real ifindex lookup + PNAC counters)                                                   | Items only (PR #5901)         |
| C5  | `cmd/nim/nim.go` `publishBondMetrics` + `getBondIfIndex` (bridge-renamed bond resolution)                                     | Items only (PR #5901)         |
| C6  | `cmd/nim/nim.go` handle{Vault,EnrolledCert,EdgeNodeCluster,KubeUserServices}* — full pubsub-driven update path                 | None                          |
| C7  | `dpcmanager.UpdateVaultReadiness` / `UpdateEnrolledCerts` -> `dpcreconciler` PhysicalIfs reconcile after vault unlock          | None                          |
| C8  | `dpcreconciler/linux.go` cluster-static-IP assignment driven by `EdgeNodeClusterStatus`                                       | Mocked unit only               |
| C9  | `dpcreconciler/linux.go` ACLs with `KubeUserServices` (real iptables rules from real services)                                | Mocked unit only               |
| C10 | `dpcmanager/lps.go` end-to-end with multiple ports + `AllowLocalModifications` flipping mid-run + permission errors            | Partial (current LPS test)    |
| C11 | `dpcmanager/verify.go` real fallback to lower-priority working DPC with real connectivity loss (SDN-induced) and recovery     | Mocked unit only               |
| C12 | `dpcmanager/lastresort.go` last-resort kicking in when controller DPC is broken; lastresort gating by GCP                     | Mocked unit only               |
| C13 | `dpcmanager` persistence: persisted `DevicePortConfigList` reapplied after reboot                                             | Mocked unit only               |
| C14 | `conntester/controller.go` real success/failure + `RemoteTemporaryFailure` from a deliberately-misbehaving controller         | None                          |
| C15 | `conntester/controller.go` `DiagRemoteEndpoints` driven by GCP                                                                | None                          |
| C16 | `cipher` decryption of WiFi PSK / 802.1x credentials inside DPC, with `ControllerCert`/`EdgeNodeCert` flow                    | Mocked unit only               |
| C17 | `dpcreconciler` real PNAC (802.1x) authentication via Eden-SDN authenticator + scepclient certs                                | None                          |
| C18 | Manual DPC from monitor TUI taking effect, surviving reboot, and being overridden by a newer controller DPC                   | None                          |
| C19 | NetworkInstance flowlog-enable -> ACL subgraph reconcile (conntrack logging actually enabled in iptables)                     | None                          |
| C20 | LOC URL update -> next conntester cycle adds it as probe target                                                              | None                          |

## Test plan, grouped by functional area

Each section names the proposed file path under
`tests/eclient/testdata/` (or `tests/network/testdata/` for SDN-heavy ones)
and a short scenario list. Names are suggestions — pick whichever convention
the maintainers prefer.

### 1. DPC source & priority ordering — startup matrix

**Goal:** exercise C1, C2, C13, C18, plus the explicit *startup matrix* below.

NIM's startup ingest decisions in `cmd/nim/nim.go` `run()` are driven by the
combination of three on-disk artefacts on the `/config` partition (or
`/persist`). Every combination needs explicit coverage so that no precedence
branch is silently regressed.

| Row | `/persist/checkpoint/lastconfig` | `/config/bootstrap-config.pb` | `/config/DevicePortConfig/*.json` | Expected NIM behaviour                                                                                                                                                                                  | Test                                          |
| --- | -------------------------------- | ----------------------------- | --------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------- |
| R1  | absent                           | absent                        | present                           | `ingestDevicePortConfigFile` copies each file to `/run/global/DevicePortConfig/`; `expectBootstrapDPCs=true`; the file-based DPC is the active config until controller publishes a higher-priority one. | `override_then_controller.txt`, `usb_json.txt` |
| R2  | absent                           | present                       | absent                            | NIM does not ingest legacy JSONs (none); zedagent decodes `bootstrap-config.pb` and republishes a DPC under key `zedagent`; `expectBootstrapDPCs=true` so DpcManager waits for it.                       | `bootstrap_only.txt` (new)                    |
| R3  | absent                           | present                       | present                           | Bootstrap takes precedence: NIM logs `"Not ingesting DPC jsons … bootstrap config is present"` (skip path at `nim.go:990`); legacy JSONs do *not* appear in `/run/global/DevicePortConfig/`.             | `bootstrap_supersedes_override.txt` (new)     |
| R4  | present                          | absent                        | absent                            | `hasPersistLastconfig()=true`; persisted `DevicePortConfigList` is reapplied; `expectBootstrapDPCs=false`.                                                                                               | `dpcl_reapplied_after_reboot.txt` (§3)         |
| R5  | present                          | absent                        | present                           | NIM logs `"Not ingesting DPC jsons … /persist/checkpoint/lastconfig is present"` and skips the legacy-json copy.                                                                                         | `lastconfig_blocks_ingest.txt`                 |
| R6  | present                          | present                       | (any)                             | NIM logs `"Not ingesting bootstrap config from config partition: /persist/checkpoint/lastconfig is present"`; persisted DPCL wins; `expectBootstrapDPCs=false`.                                          | `lastconfig_blocks_bootstrap.txt` (new)        |

The combinations not in the table (e.g. all three absent — onboarding via cloud
only) are covered implicitly by every other eden test that boots a fresh
device, so they need no dedicated scenario.

#### `tests/network/testdata/nim_dpc_sources/override_then_controller.txt`

* Pre-boot: drop a syntactically-valid DPC at `/config/DevicePortConfig/override.json`
  (DHCP on eth0).
* Boot device with no controller DPC (`eden eve reset` + delay before
  publishing config).
* Assert `DevicePortConfigList` contains an entry with key `override` and that
  it's the active DPC. (`peek-persist.sh
  /persist/status/nim/DevicePortConfigList/global.json`.)
* Assert override DPC was copied into `/run/global/DevicePortConfig/override.json`
  (covers `cmd/nim` `ingestDevicePortConfigFile`).
* Now publish a controller DPC; assert that within
  `network.test.better.interval`, key `zedagent` becomes the active DPC and
  `override` drops to fallback.
* **Coverage gained:** `cmd/nim` `ingestDevicePortConfig`,
  `ingestDevicePortConfigFile` (file copy + sanitize + JSON write),
  `listPublishedDPCs`, plus DpcManager priority-ordering against a real
  `expectBootstrapDPCs=true` boot.

#### `tests/network/testdata/nim_dpc_sources/usb_json.txt`

Same as above but the file is named `usb.json` to verify the
`origin = NETWORK_CONFIG_ORIGIN_OVERRIDE` and the `key=usb` derivation. Adds
one statement-level branch (suffix matching) but mostly increases path
confidence.

#### `tests/network/testdata/nim_dpc_sources/lastconfig_blocks_ingest.txt`

* Onboard a device normally so that `/persist/checkpoint/lastconfig` exists.
* While powered down, drop `override.json` into `/config/DevicePortConfig/`
  via SDN-side mount.
* Boot.
* Assert `cmd/nim` log line: `"Not ingesting DPC jsons … : /persist/checkpoint/lastconfig is present"`.
* Assert there is *no* entry with key `override` in `DevicePortConfigList`.
* **Coverage gained:** `cmd/nim` `hasPersistLastconfig`, the `ignoreBootstraps`
  branch.

#### `tests/network/testdata/nim_dpc_sources/bootstrap_only.txt` *(R2)*

* Build a device-config protobuf with the eden CLI (already supported —
  `eden/pkg/eden/eden.go:708` writes `bootstrap-config.pb` into the `/config`
  partition at install time).
* Install the EVE image with this `/config` partition; ensure
  `/persist/checkpoint/lastconfig` and `/config/DevicePortConfig/*.json` are
  *both absent*. (A fresh-installed device satisfies both.)
* Configure the controller to *not* publish any device config yet (or hold it
  back via SDN), so the bootstrap path is the only DPC source on first boot.
* Assertions:
  * `cmd/nim` logs `"Starting nim"` followed by DpcManager log indicating it
    is waiting on `expectBootstrapDPCs=true`.
  * `peek-pubsub.sh /run/zedagent/DevicePortConfig/zedagent.json` shows the
    DPC derived from `bootstrap-config.pb` (port set, IP config, etc.,
    matching what was packed into the protobuf).
  * `peek-persist.sh /persist/status/nim/DevicePortConfigList/global.json`
    contains exactly one entry with key `zedagent` and `CurrentIndex=0`.
  * `/run/global/DevicePortConfig/` is empty (no legacy file ingest).
  * Controller becomes reachable using only the bootstrap DPC.
* **Coverage gained:** `cmd/nim`'s `haveBootstrapConf=true` branch in `run()`,
  the DpcManager wait-for-bootstrap path, and the bootstrap → zedagent →
  `subDevicePortConfigA` flow end-to-end.

#### `tests/network/testdata/nim_dpc_sources/bootstrap_supersedes_override.txt` *(R3)*

* Build a `/config` partition that contains *both* `bootstrap-config.pb` (with
  port "eth0 DHCP") *and* `/config/DevicePortConfig/override.json` (with
  port "eth0 static 192.0.2.10"). Both can be created via existing eden
  facilities; the override.json is just a normal file copied into the
  partition image.
* Boot a freshly-installed device (no `lastconfig`).
* Assertions:
  * `cmd/nim` logs `"Not ingesting DPC jsons (override.json) from config partition: bootstrap config is present"`.
  * `/run/global/DevicePortConfig/` does *not* contain `override.json`.
  * `DevicePortConfigList` shows only the bootstrap DPC (key `zedagent`),
    no entry with key `override`.
  * eth0 obtains its address via DHCP, *not* the static address from
    `override.json`.
* **Coverage gained:** the `nim.go:990` early-return branch in
  `ingestDevicePortConfig` and the precedence guarantee.

#### `tests/network/testdata/nim_dpc_sources/lastconfig_blocks_bootstrap.txt` *(R6)*

* Onboard the device normally so that `/persist/checkpoint/lastconfig` is
  populated.
* While powered down, drop a `bootstrap-config.pb` into `/config/` (e.g. via
  the SDN-side mount eden uses to repopulate `/config` for offline tests).
  Make this bootstrap pb describe a *different* DPC from the persisted one
  (e.g. eth1 management instead of eth0).
* Boot.
* Assertions:
  * `cmd/nim` logs `"Not ingesting bootstrap config from config partition: /persist/checkpoint/lastconfig is present"`.
  * `expectBootstrapDPCs` is reset to `false` (the device proceeds without
    waiting for an installer DPC).
  * `DevicePortConfigList`'s active DPC matches the persisted one (eth0),
    not the bootstrap pb (eth1).
  * Bootstrap pb file remains on `/config/` (NIM does not delete it).
* **Coverage gained:** the `expectBootstrapDPCs && ignoreBootstraps`
  reset branch in `cmd/nim/nim.go:214`.

#### `tests/network/testdata/nim_dpc_sources/manual_tui_dpc.txt`

* Use the `monitor` agent's IPC (or write directly to its persistent pubsub
  topic under `/persist/status/monitor/DevicePortConfig/manual.json`) to
  inject a `key=manual` DPC that disables eth1.
* Assert the manual DPC immediately takes effect (eth1 gone from
  `DeviceNetworkStatus`) and key `manual` is at the top of the list.
* Reboot. Assert manual DPC survives reboot (persistent subscription) and is
  reapplied before any controller DPC arrives.
* Then publish a *newer* controller DPC. Assert that `manual`'s
  `TimePriority` is recomputed from the time it was created so that the new
  controller DPC overrides only after passing verification.
* **Coverage gained:** `cmd/nim` manual-DPC subscription handler with
  `SanitizeTimePriority: false` for `LpsDPCKey` (and verifying that *manual*
  *does* get sanitized), C13.

### 2. DPC verification, fallback and recovery (real connectivity)

**Goal:** exercise C11, C12, C14, C15.

These scenarios depend on Eden-SDN to flip controller reachability without
restarting EVE.

#### `tests/network/testdata/nim_verify/fallback_to_lower_priority.txt`

* Start with a known-good DPC (single uplink, controller reachable).
* Submit a *new* DPC that points eth0 at a non-routable subnet (e.g. via SDN
  reroute or by removing the default-route gateway). NIM should mark this
  highest-priority DPC as failing verification.
* Assert `DPC verify` log shows `DPC_FAIL_WITH_IPANDDNS` then `DPC verify:
  Working with DPC configuration found at index 1`.
* Assert `DeviceNetworkStatus.CurrentIndex == 1`.
* Restore SDN routing. Within `network.test.better.interval`, NIM should
  re-attempt index 0 and mark it succeeding; assert `CurrentIndex == 0`.
* **Coverage gained:** real `dpcmanager/verify.go`
  `verifyAndUpdateDPC` paths under both DPC_FAIL and DPC_SUCCESS, real
  `conntester/controller.go` HTTPS probes, real `flextimer` ticking.

#### `tests/network/testdata/nim_verify/remote_temporary_failure.txt`

* Configure SDN to inject HTTP 5xx into the controller's `ping` endpoint
  (Eden-SDN goproxy already has an inject-error feature).
* Assert `conntester` returns `RemoteTemporaryFailure`, that DpcManager keeps
  the DPC marked valid (`DPC_REMOTE_WAIT`), and that no fallback occurs.
* Stop injecting; assert state returns to `DPC_SUCCESS`.
* **Coverage gained:** the `RemoteTemporaryFailure` branch of
  `controller.go` and corresponding state transitions in
  `dpcmanager/verify.go`.

#### `tests/network/testdata/nim_verify/diag_remote_endpoints.txt`

* Update GCP `network.test.diag.remote.endpoints` to a list of two URLs
  (one served by SDN, one always-failing).
* Assert `nim` agent metrics report probes against both endpoints (covers
  `cmd/nim.applyGlobalConfig` updating `connTester.DiagRemoteEndpoints`).
* **Coverage gained:** GCP-driven diagnostic endpoint propagation.

#### `tests/network/testdata/nim_verify/lastresort_kicks_in.txt`

* Boot with a *broken* controller DPC (no IP / no DNS) and `network.fallback.any.eth=enabled`.
* Assert NIM publishes a `lastresort`-keyed DPC, transitions to it, and
  recovers controller reachability through the lastresort uplinks.
* Disable lastresort via GCP; assert it gets removed from the DPC list and is
  not used.
* **Coverage gained:** `dpcmanager/lastresort.go` enable/disable paths under a
  real DHCP environment.

### 3. Persistence and reboot

**Goal:** exercise C13.

#### `tests/network/testdata/nim_persist/dpcl_reapplied_after_reboot.txt`

* Establish two DPCs in the list (one fallback, one current).
* `eden eve ssh sync` then `eden eve reboot`.
* Before zedagent has a chance to publish a new DPC (use SDN to delay
  controller responses), assert NIM has reapplied the persisted DPC and that
  controller reachability is achieved purely from the persisted state.
* **Coverage gained:** persistent-pubsub reapply path in `cmd/nim` and
  `dpcmanager/dpcmanager.go` `Init`/`Run`.

### 4. LPS local port overrides

**Goal:** exercise C10. The existing `network_local_changes.txt` covers the
single-port case; we need the multi-port and edge-case behaviours.

#### `tests/eclient/testdata/lps_all_mgmt_ports_overridden.txt`

* Two management uplinks. LPS overrides DNS on both, both with
  `AllowLocalModifications=true`.
* Verify NIM does *not* fall back to a lower-priority DPC even after the LPS
  change deliberately breaks reachability for a brief window (covers the
  "all mgmt ports using LPS" suppression in `dpcmanager/lps.go`
  `areAllMgmtPortUsingLpsConfig`).
* Then revert one port to controller config; assert fallback becomes
  re-eligible.

#### `tests/eclient/testdata/lps_wireless_type_mismatch.txt`

* Controller DPC has eth0 as Ethernet. LPS sends a DPC for eth0 marked
  `WirelessCfg.WType=WirelessTypeWifi`.
* Assert LPS port is rejected with the wireless-type-mismatch error
  (`peek-pubsub.sh` on `nim/DeviceNetworkStatus` `Ports[].LastError`).
* **Coverage gained:** `mergeWithLpsConfig` wireless-mismatch branch.

### 5. Resolver cache for controller hostname

**Goal:** exercise C3.

#### `tests/eclient/testdata/nim_resolver_cache.txt`

* Configure a controller hostname (not an IP) in `/config/server`.
* Stand up a fake DNS server in SDN that returns a 30-second TTL on the
  first response and 5-second TTL on subsequent responses.
* Assert `pubCachedResolvedIPs` publishes one entry within ~30s, and that the
  publication's `ValidUntil` matches the returned TTL plus the
  `refetchDelay`.
* Make the DNS server temporarily fail (NXDOMAIN); assert
  `resolveAndCacheIP` falls back to a 30-second retry as per `defaultRefetchPeriod`.
* Replace `/config/server` with an IP literal; assert the goroutine exits and
  no further DNS queries are issued.
* **Coverage gained:** all branches of `cmd/nim/resolvercache.go`.

### 6. PNAC (802.1x) authentication and metrics

**Goal:** exercise C4, C7, C16, C17. Requires Eden-SDN to host a 802.1x
authenticator.

#### `tests/network/testdata/nim_pnac/authenticated_uplink.txt`

* SDN model: eth0 wired to a hostapd-based 802.1x authenticator with an
  internal RADIUS server (already feasible via Eden-SDN's general endpoint
  mechanism — extend if needed).
* Controller DPC for eth0 includes `PNAC.Enabled=true` and references a
  SCEP enrollment profile.
* Mock `scepclient` to publish an `EnrolledCertificateStatus` for that
  profile.
* Assert that:
  * Until vault status is "ready" the PNAC port shows
    `LastError: "vault not ready"` (or whatever the actual gating is — confirm
    against `dpcreconciler/linux.go` `getIntendedPhysicalIfs`).
  * Once vault is ready, `wpa_supplicant` is started, port authenticates,
    DHCP runs, controller becomes reachable.
  * `pubPNACMetrics` publishes a non-zero counter list for eth0 within one
    metric interval.
  * Forcing a re-auth (toggle authenticator) increments `pubPNACMetrics`
    `eapolFramesRx`.
* **Coverage gained:** real flow through `cmd/nim/publishPNACMetrics`
  (including ifindex lookup, GetPNACMetrics in netmonitor), real PNAC
  reconciliation (which relies on `dpcreconciler/genericitems/wpa_supplicant.go`
  whose unit tests came in PR #5901), full vault/scep gating.

#### `tests/network/testdata/nim_pnac/dhcp_reacquire.txt`

* Same authenticator, but force the authenticator to drop the port for ~5s.
* Verify NIM bumps the DHCP-reacquire counter and runs a fresh DHCPDISCOVER
  on link-up (covers `dpcreconciler` PNAC DHCP reacquire end-to-end after the
  state-machine variant in unit tests).
* Vary GCP `network.pnac.dhcp.reacquire.maxretries` to 0 to cover the
  disabled branch.

### 7. Bond and VLAN reconciliation (rewrite of skipped test)

**Goal:** exercise C5, plus statement coverage of
`dpcreconciler/linuxitems/bond.go` and `vlan.go` against a real kernel.

#### `tests/network/testdata/nim_bond_vlan/bond_with_vlan_uplink.txt`

* Replace the current `tests/network/testdata/vlans_and_bonds.txt` (skipped)
  with an SDN-driven variant:
  * SDN exposes 4 ports to EVE, where two pairs are interconnected by a
    crossover so that bonds form correctly.
  * EVE DPC defines `bond0 = eth1+eth2`, `bond0.100` and `bond0.200` as
    management uplinks.
* Assert link-up, IP allocation per VLAN, controller reachable.
* Assert `pubBondMetrics` reports both members and that the
  `LogicalLabel` reverse-lookup for member ports works (covers
  `cmd/nim.publishBondMetrics`'s `dns.LookupPortByIfName` path).
* Disconnect one member port via SDN. Assert `BondMetricsList`'s member-state
  flips and that the bond stays up if mode is fail-over (round-robin will
  drop traffic; choose mode accordingly).
* **Coverage gained:** C5; real `getBondIfIndex` "bridge-with-bond" path
  exercised when EVE wraps the bond into a NI bridge as well.

#### `tests/network/testdata/nim_bond_vlan/vlan_only_uplink.txt`

* Single-eth uplink with VLAN 100 management uplink and VLAN 200 app uplink.
* Verify reconciliation creates `eth0.100` and `eth0.200` and that swapping
  the two roles via a new DPC reconciles correctly without flapping the
  parent eth0.

### 8. WLAN with cipher decryption

**Goal:** exercise C16. Hard to do hardware-free; possible via SDN if a
software AP is hosted in the SDN VM.

#### `tests/network/testdata/nim_wlan/encrypted_psk.txt`

* SDN model includes a `wifi`-flavored port backed by hostapd inside the
  SDN VM.
* Controller DPC contains an encrypted PSK referencing a controller cert.
* Assert NIM consumes both `ControllerCert` and `EdgeNodeCert`, decrypts the
  PSK (`pubCipherBlockStatus.Status=BLOCK_STATUS_OK`), and `wpa_supplicant`
  associates with the SSID.
* Provide a wrong-cert variant; assert `BLOCK_STATUS_DECRYPTION_FAILED` and
  the port stays down.
* **Coverage gained:** `cmd/nim`'s wiring of cipher into DpcReconciler, real
  decryption metrics counters, plus `dpcreconciler/genericitems/wpa_supplicant.go`
  cipher branches.

### 9. Edge-node cluster IP and Kube user services

**Goal:** exercise C8, C9. Tightly coupled to kubevirt mode and zedkube.

#### `tests/kubevirt/testdata/nim_cluster_static_ip.txt`

* Kube cluster mode. Have zedkube publish `EdgeNodeClusterStatus` with a
  cluster interface and cluster IP `10.0.0.42`.
* Assert `ip addr show eth0` includes `10.0.0.42` *in addition to* the DPC's
  configured IP.
* Trigger a cluster-status delete; assert the static IP goes away.

#### `tests/kubevirt/testdata/nim_kube_user_services_acls.txt`

* Publish `KubeUserServices` listing one NodePort service on TCP/30080 and
  one Ingress on TCP/443.
* Assert iptables rules permitting the corresponding inbound traffic on
  management uplinks (`eden eve ssh iptables -L INPUT -n -v`).
* Connect from SDN to those ports and verify connectivity.
* Remove a service; assert rule removal within one reconcile cycle.

### 10. Vault and SCEP gating of PNAC

Already covered jointly inside §6 (`nim_pnac/authenticated_uplink.txt`). No
separate test.

### 11. Radio silence — strengthen existing test

`radio_silence.txt` exists but only covers TUI-driven changes. Add:

#### `tests/eclient/testdata/radio_silence_persistence.txt`

* Set radio silence via LPS. Reboot.
* Assert state is restored on the next boot from the persisted
  `ZedAgentStatus.RadioSilence` after zedagent restarts (this covers the
  `subZedAgentStatus` path through `cmd/nim.handleZedAgentStatusImpl` after a
  fresh boot).

### 12. Connectivity tester / LOC URL

**Goal:** exercise C20.

#### `tests/eclient/testdata/nim_loc_probe.txt`

* Have zedagent publish a `ZedAgentStatus.LOCUrl` pointing at an SDN-hosted
  HTTP server.
* Assert the server's access log shows a probe from the device within the
  next test cycle.
* Take the LOC URL down; assert connectivity testing still passes against
  the controller (LOC failure should not flip a working DPC).

### 13. Flowlog dynamic enable

**Goal:** exercise C19.

#### `tests/eclient/testdata/nim_flowlog_acl_reconcile.txt`

* Create a `NetworkInstance` with `EnableFlowlog=false`. Assert that the
  conntrack-logging iptables target is *not* installed.
* Modify the NI to `EnableFlowlog=true`. Assert that on the next reconcile
  the corresponding rule appears under the ACLs subgraph.
* Delete the NI. Assert the rule goes away.

## Per-test coverage matrix

| Test file                                                        | C1 | C2 | C2b | C3 | C4 | C5 | C6 | C7 | C8 | C9 | C10 | C11 | C12 | C13 | C14 | C15 | C16 | C17 | C18 | C19 | C20 |
|------------------------------------------------------------------|----|----|-----|----|----|----|----|----|----|----|-----|-----|-----|-----|-----|-----|-----|-----|-----|-----|-----|
| nim_dpc_sources/override_then_controller.txt                     | x  | x  | x   |    |    |    |    |    |    |    |     |     |     |     |     |     |     |     |     |     |     |
| nim_dpc_sources/usb_json.txt                                     | x  |    |     |    |    |    |    |    |    |    |     |     |     |     |     |     |     |     |     |     |     |
| nim_dpc_sources/lastconfig_blocks_ingest.txt                     |    | x  |     |    |    |    |    |    |    |    |     |     |     |     |     |     |     |     |     |     |     |
| nim_dpc_sources/bootstrap_only.txt                               |    | x  | x   |    |    |    |    |    |    |    |     |     |     |     |     |     |     |     |     |     |     |
| nim_dpc_sources/bootstrap_supersedes_override.txt                |    | x  |     |    |    |    |    |    |    |    |     |     |     |     |     |     |     |     |     |     |     |
| nim_dpc_sources/lastconfig_blocks_bootstrap.txt                  |    | x  |     |    |    |    |    |    |    |    |     |     |     |     |     |     |     |     |     |     |     |
| nim_dpc_sources/manual_tui_dpc.txt                               |    |    |     |    |    |    |    |    |    |    |     |     |     | x   |     |     |     |     | x   |     |     |
| nim_verify/fallback_to_lower_priority.txt                        |    |    |     |    |    |    |    |    |    |    |     | x   |     |     | x   |     |     |     |     |     |     |
| nim_verify/remote_temporary_failure.txt                          |    |    |     |    |    |    |    |    |    |    |     |     |     |     | x   |     |     |     |     |     |     |
| nim_verify/diag_remote_endpoints.txt                             |    |    |     |    |    |    |    |    |    |    |     |     |     |     |     | x   |     |     |     |     |     |
| nim_verify/lastresort_kicks_in.txt                               |    |    |     |    |    |    |    |    |    |    |     |     | x   |     |     |     |     |     |     |     |     |
| nim_persist/dpcl_reapplied_after_reboot.txt                      |    | x  |     |    |    |    |    |    |    |    |     |     |     | x   |     |     |     |     |     |     |     |
| lps_all_mgmt_ports_overridden.txt                                |    |    |     |    |    |    |    |    |    |    | x   |     |     |     |     |     |     |     |     |     |     |
| lps_wireless_type_mismatch.txt                                   |    |    |     |    |    |    |    |    |    |    | x   |     |     |     |     |     |     |     |     |     |     |
| nim_resolver_cache.txt                                           |    |    |     | x  |    |    |    |    |    |    |     |     |     |     |     |     |     |     |     |     |     |
| nim_pnac/authenticated_uplink.txt                                |    |    |     |    | x  |    | x  | x  |    |    |     |     |     |     |     |     | x   | x   |     |     |     |
| nim_pnac/dhcp_reacquire.txt                                      |    |    |     |    | x  |    |    |    |    |    |     |     |     |     |     |     |     | x   |     |     |     |
| nim_bond_vlan/bond_with_vlan_uplink.txt                          |    |    |     |    |    | x  |    |    |    |    |     |     |     |     |     |     |     |     |     |     |     |
| nim_bond_vlan/vlan_only_uplink.txt                               |    |    |     |    |    |    |    |    |    |    |     |     |     |     |     |     |     |     |     |     |     |
| nim_wlan/encrypted_psk.txt                                       |    |    |     |    |    |    |    |    |    |    |     |     |     |     |     |     | x   |     |     |     |     |
| nim_cluster_static_ip.txt                                        |    |    |     |    |    |    | x  |    | x  |    |     |     |     |     |     |     |     |     |     |     |     |
| nim_kube_user_services_acls.txt                                  |    |    |     |    |    |    | x  |    |    | x  |     |     |     |     |     |     |     |     |     |     |     |
| radio_silence_persistence.txt                                    |    |    |     |    |    |    |    |    |    |    |     |     |     | x   |     |     |     |     |     |     |     |
| nim_loc_probe.txt                                                |    |    |     |    |    |    |    |    |    |    |     |     |     |     |     |     |     |     |     |     | x   |
| nim_flowlog_acl_reconcile.txt                                    |    |    |     |    |    |    |    |    |    |    |     |     |     |     |     |     |     |     |     | x   |     |

## Suggested implementation order

The order below front-loads scenarios that need only existing fixtures so a
first batch of tests can be opened for review while the more invasive SDN
extensions (PNAC authenticator, hostapd-in-SDN, fake DNS server) are being
developed.

1. **Phase 1 (no new SDN features needed; in flight in lf-edge/eden#1165):**
   `nim_dpc_sources/override_then_controller.txt`,
   `nim_dpc_sources/usb_json.txt`,
   `nim_dpc_sources/lastconfig_blocks_ingest.txt`,
   `nim_dpc_sources/bootstrap_only.txt`,
   `nim_dpc_sources/bootstrap_supersedes_override.txt`,
   `nim_dpc_sources/lastconfig_blocks_bootstrap.txt`,
   `nim_persist/dpcl_reapplied_after_reboot.txt`,
   `lps_all_mgmt_ports_overridden.txt`,
   `lps_wireless_type_mismatch.txt`,
   `radio_silence_persistence.txt`,
   `nim_flowlog_acl_reconcile.txt`,
   `nim_verify/diag_remote_endpoints.txt`.
2. **Phase 2 (require SDN-based controller misbehaviour injection):**
   `nim_verify/fallback_to_lower_priority.txt`,
   `nim_verify/remote_temporary_failure.txt`,
   `nim_verify/lastresort_kicks_in.txt`,
   `nim_resolver_cache.txt`,
   `nim_loc_probe.txt`.
3. **Phase 3 (require SDN extensions for L2 emulation):**
   `nim_bond_vlan/bond_with_vlan_uplink.txt`,
   `nim_bond_vlan/vlan_only_uplink.txt`.
4. **Phase 4 (require SDN extensions: hostapd, RADIUS, fake DNS):**
   `nim_pnac/authenticated_uplink.txt`,
   `nim_pnac/dhcp_reacquire.txt`,
   `nim_wlan/encrypted_psk.txt`.
5. **Phase 5 (kubevirt-only):**
   `nim_cluster_static_ip.txt`,
   `nim_kube_user_services_acls.txt`,
   `nim_dpc_sources/manual_tui_dpc.txt` (depends on a `monitor`-injection
   helper that does not exist yet — may need a small Eden-side helper).

## Out of scope

The following are intentionally excluded from this plan:

* Cellular (LTE) connectivity beyond the existing `publish_location.txt`
  fixture-driven flow. Real LTE testing requires hardware not available in
  CI; mmagent/wwan internals are best covered by `pkg/wwan` unit tests and
  hardware tests on real boards.
* Replacing or rewriting the existing `tests/eclient/testdata/network_local_changes.txt`,
  which already covers the single-port LPS happy-path adequately.
* Item-level reconciliation tested by PR #5901 — running these eden tests
  *will* exercise those items in passing, but the unit tests remain the
  primary coverage source for them.

## Notes on reading coverage

To verify that the eden tests actually move the statement-coverage needle
on `pkg/pillar/cmd/nim` and friends:

* Build a coverage-instrumented EVE image (see `eden-test-authoring` skill
  for the standard procedure: build pillar with `-cover`, capture coverage
  data on shutdown via the existing pillar coverage hook, and aggregate
  with `go tool covdata`).
* Run the planned eden tests against that image.
* Compare `go tool covdata textfmt -i ... -o coverage.out` against the
  baseline produced by Go unit tests alone for these packages, focusing on
  files listed under "Coverage targets". The interesting deltas are in
  `cmd/nim/nim.go`, `cmd/nim/resolvercache.go`, `dpcmanager/lps.go`,
  `dpcmanager/lastresort.go`, `dpcmanager/verify.go` (verify-state
  transitions), `dpcmanager/wwan.go`, and the `dpcreconciler/linux.go`
  reconcile-trigger branches that depend on real subscription updates.
