---
status: accepted
date: 2026-08-24
---

# 0029. Publish a Ready condition and the instance type catalogue on ProviderConfig status

## Context

Nothing wrote `ProviderConfig.status`. The condition type existed on the API and had exactly one reader, the web layer, which rendered a chip that could never fill.

The catalogue refresher already built a token-backed lister for every provider config and listed every region once an hour, then dropped the outcome into counters and a metric. A bad token failed there and the object that was actually wrong said nothing, so a misconfigured provider was diagnosed from a lease's conditions instead.

The instance type table had a second problem. Both serving modes hand the web layer an absent catalogue, and the real cache is built only inside the controller process, so the table rendered its empty state in every shipping configuration. [0027](0027-mutating-web-interface-behind-a-typed-writer-and-a-cross-origin-guard.md) recorded that making the route work needs the catalogue published somewhere a reader outside the controller can reach, and named it as separate work. This record is that work.

## Decision

The refresher keeps one fetch loop and hands each per-config outcome to a publisher through a narrow interface. The publisher owns provider config health: it resolves every referenced Secret, checks the teardown guarantee, builds a provider from what it resolved, reports the catalogue fetch, and writes a `Ready` condition, a `CataloguePublished` condition and the resolved instance types.

### Readiness runs the build the first lease runs

A condition that only proves the token authenticates is a half-truth. A cloud-init secret holding valid YAML with no pool-label assignment resolves cleanly, reports ready, and then fails every lease at admission. So readiness performs the same provider construction the create path performs, over the values it has already resolved. It adds no further reads.

### An answer carrying nothing is not a catalogue

A provider that answers successfully with no instance type in any region is treated as a failed fetch rather than an empty result. Without that, an empty answer wiped the in-memory cache while status kept the last good list, and the two disagreed: the interface reads status and would show the stale-good table, while lease admission reads the cache and refuses the lease because the region offers nothing.

### The write is gated on content, not on leadership

The refresher deliberately runs on every replica so each keeps a warm cache. Rather than gate the status write on leader election, the publisher writes only when the content changes and retries on conflict. The condition and the catalogue are deterministic given the probe result, so two replicas racing write identical content and the loser observes no change and skips.

The refresh time is the one field that cannot be content-gated, because it moves on every fetch by definition. It is rewritten at most twice per refresh interval, measured against the value already in status rather than against anything the replica remembers, which bounds the write rate without letting the interface present a stale catalogue as a current one.

## Options considered

- A publisher behind a narrow interface, chosen.
- A separate ProviderConfig controller. Rejected. It would repeat a provider fetch that already runs and double the calls to the provider API.
- Growing the status write inside the refresher. Rejected. The refresher would then own both a fetch loop and provider config health.
- Gating the write on leader election. Rejected. The refresher runs on every replica by design, and a change-gated write is simpler and equally correct.
- Publishing only the result of the API token check. Rejected. A config with a good token and a broken cloud-init still fails at its first lease, so the condition would promise more than it checked.
- Publishing a refresh timestamp on every fetch. Rejected. It would undo the change gate entirely.

## Consequences

The status list is capped at 512 entries with a condition when truncation bites, so an oversized catalogue is visible rather than a rejected write. A fetch that fails keeps the last good catalogue instead of wiping it.

Status can oscillate. The refresher is deliberately not leader-elected so that every replica keeps a warm cache, and each replica runs its own fetch, so in a multi-replica deployment one replica reaching the provider while another does not will flip `Ready` between true and false. The interval bounds this to churn rather than a hot loop, but it presents as an unexplained condition flap, and the chart shipping one replica is what keeps it out of sight rather than anything in this design.

Failure precedence changed for one two-fault case. Secrets are now resolved together before the cloud-init template is inspected, so a config that both leaves a sentinel unsupplied and points another reference at an unreadable Secret reports the unreadable Secret, where it used to report the unsupplied sentinel. Both name a real fault, and the rule is now the simpler one: the first broken input in resolution order.

Status converges on provider config creation and on every spec edit through the generation-change watch, and otherwise within the refresh interval, so repairing a broken Secret is reflected at the next refresh rather than immediately.
