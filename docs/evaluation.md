# Evaluation

This document reports what was measured about horizon, how it was measured, and what the
measurements do and do not support. It covers four measurements: time to ready (M1),
teardown by enforcing path and per injected failure (M2), cost per burst against
theoretical (M3), and two requirement-based sizing policies against a pinned baseline
(M4). Every number below comes from a named artefact produced by a run, and the artefact
is named beside the number so any claim can be checked.

The measurement protocol is specified in `spec-2-design.md` sections 5.4 to 5.8, in the
bedrock design directory `.superpowers/sdd/2026-08-07-horizon-spec-2-instrumentation/`.
Where the results depart from what that document expected, the departure is stated rather
than smoothed over.

## 1. What is claimed, and what is not

The thesis under test: ephemeral leased capacity can guarantee its own teardown on two
independent clocks, and choosing the machine by requirement rather than by name is worth
the mechanism.

Time to ready is measured in three Hetzner locations. That is a latency measurement of one
product in several of its own locations. It is not a claim of multi-region support. The
cost model reports what a burst cost after the fact; it does not forecast spend, model a
budget, or recommend when to burst.

Everything measured here ran against one provider (Hetzner Cloud), one cluster (three
bare-metal K3s nodes reconciled by Flux), and one node image family. No result below
generalises to another provider without re-measurement.

## 2. Method

### 2.1 Two harnesses, both out of band

Half the interesting measurements require killing the operator. An operator cannot time its
own death, and Prometheus cannot record from a process that is not running. Measurement
therefore runs from outside the cluster, driven from a workstation over the tailnet, and
lives in bedrock rather than in horizon because it holds a Hetzner token and is an estate
tool rather than a product feature.

`scripts/measure-burst.sh` drives M1, M2 and M3. Per run it applies a `CapacityLease`,
polls the Hetzner API every 5 seconds recording every server state transition and the exact
instant a server disappears, polls lease status for `acceptedAt`, `readyAt`, `expiresAt` and
`releasedAt`, polls the `Node` for its Ready condition and its `horizon.dev/watchdog-armed`
annotation, performs a scripted fault injection at a fixed offset after readiness, and
writes one CSV row per observed event.

`scripts/measure-policy.sh` drives M4. It adds the sizing arms, runs a fixed synthetic
workload on the leased nodes, and computes cost from live Hetzner pricing.

The Hetzner API poll is the authoritative clock for teardown, because it observes
disappearance directly rather than inferring it from a controller that may be dead. The
operator's own `releasedAt` is reported against it.

### 2.2 Every run exports its own artefacts

The Prometheus instance in this cluster retains 7 days. The measurement runs completed on
20 and 21 August; this document and the posts derived from it outlive that window. Metrics
from the runs are gone before they are written about, so per-run artefact export is part of
the harness rather than a habit. Nothing in this document depends on a live Prometheus.

Each burst run writes `harness.csv`, `lease-final.json`, `run-params.json`, `operator.log`,
`hetzner-pricing.json` and a `prometheus/` directory. Each policy run additionally writes
`summary.txt`, `cost.json`, `hetzner-server-types.json` and a `<run>-q/results.jsonl`
carrying one JSON line per completed work unit.

Two honest caveats about that export:

- **The Prometheus dumps are empty.** All 30 burst runs record
  `export-prometheus,0-series-0-failed` in `harness.csv`, and every `prometheus/` directory
  contains zero files. The operator's `horizon_` metric families were not yet wired when
  those runs executed; the metric surface landed on 21 August, after the burst campaign.
  The design intended to split time to ready using `provider_request_duration_seconds`.
  That split is unavailable, and the harness timeline is the only source for it.
- **The artefacts are not committed.** `bedrock/.gitignore` excludes
  `/var/measure-burst-runs/` and `/var/measure-policy-runs/`. Paths cited in this document
  are local to the machine that ran the campaign.

### 2.3 What the instruments can and cannot resolve

Three resolution limits bound everything below and are stated once here.

| Instrument | Resolution | Consequence |
| --- | --- | --- |
| Hetzner API poll (`POLL_INTERVAL_S=5`) | 5 s | Teardown instants are accurate to 5 s, no better |
| Lease `readyAt`, as the controller wrote it during the campaign | 30 s | Time to ready is quantised, see section 3 |
| Quantum elapsed time, measured in-process | 1 ms | Workload timing is exact |

While the campaign ran, the lease controller watched only `CapacityLease` objects and
requeued every 30 seconds while it waited for nodes, so node readiness reached lease status
only at a tick after acceptance. This is a property of the operator, not of the harness, and
it is the single most important thing to know when reading M1. The instrument has since been
fixed, as section 3.2 and ADR 0026 record: the controller wakes on `Node` events and sources
`readyAt` from the node's own `NodeReady` transition time. The published dataset predates
that fix and stays quantised exactly as reported here, and the fix does not retrospectively
improve any figure already reported.

### 2.4 Runs excluded from results

Six of the 19 policy runs completed no work unit and are excluded from all M4 results. They
are reported here because they cost machines and because two of them are themselves a
finding.

| Run | Machines | Outcome recorded in `cost.json` | Cause |
| --- | --- | --- | --- |
| `m4-policy-b-r1-w1` | 1 | `failed` | Node joined, then stopped reporting Ready |
| `m4-policy-a-r1-w2` | 1 | `not-run` | Instance stayed at phase `Created`, never joined |
| `m4-policy-b-r1-w2` | 1 | `not-run` | Instance stayed at phase `Created`, never joined |
| `m4-baseline-r3` | 3 | `not-run` | 1 of 3 instances joined |
| `m4-diag-a` | 1 | `failed` | Diagnosis run, see section 7 |
| `m4-diag-b` | 1 | `failed` | Diagnosis run, see section 7 |

No burst run was excluded. All 30 produced a complete timeline.

## 3. M1, time to ready

Fifteen boots were planned: three locations by three boots at one pinned type, plus three
types in one location by three boots. In execution every burst boot yields an M1 data
point regardless of which fault it was booted for, so time to ready is reported across all
30 burst runs. Source: `acceptedAt` and `readyAt` in each run's `lease-final.json`,
cross-checked against the `lease,readyAt` row in each `harness.csv`.

### 3.1 The distribution is bimodal, and the modes are 30 seconds apart

| Statistic | Value (n = 30) |
| --- | --- |
| Median | 61.0 s |
| Minimum | 60 s |
| Maximum | 94 s |
| p95 | 91 s |
| Mean | 71.8 s |

Every one of the 30 values lands within one second above a multiple of 30 seconds:

| Mode | Values | Count |
| --- | --- | --- |
| Fast | 60 s or 61 s | 19 |
| Slow | 90 s or 91 s | 10 |
| Outlier | 94 s | 1 |

The split is **19 fast and 11 slow**, counting the 94 s run as slow. An earlier working
note recorded 18 and 10; that figure summed to 28 against 30 billed boots and is wrong. The
counts here were recomputed from all 30 artefacts.

### 3.2 The modes are the sampling grid

The bimodality is an artefact of how `readyAt` was observed. While the campaign ran,
`internal/controller/capacitylease_controller.go` registered the lease reconciler for
`CapacityLease` objects only, with no watch on `Node`, and returned a `RequeueAfter` of
30 seconds while waiting for nodes. Node readiness therefore could not reach lease status
between ticks.

This is corroborated by the Hetzner-side timings, which are not quantised at all. Across
all 30 runs, from `harness.csv`:

| Provider state | Median | Range |
| --- | --- | --- |
| `initializing` | 6 s | 5 to 6 s |
| `starting` | 12 s | 11 to 23 s |
| `running` | 22 s | 16 to 28 s |

Provider create latency is tight and unimodal. The gap from `running` to recorded ready is
34 to 45 s in the 19 fast runs and 62 to 69 s in the 11 slow ones, with nothing in between.
A physical process producing a 30-second gap with no intermediate values, on an instrument
whose grid is exactly 30 seconds, is far better explained by the grid.

The defensible statement is therefore: **true time to ready lies in (30, 60] seconds for 19
of 30 runs and in (60, 90] seconds for 11 of 30, and the instrument cannot say where inside
those intervals.** The medians and the location comparison below survive this, because the
same grid applies to every run.

The instrument is fixed, and the fix does not change any number above. The controller now
wakes on `Node` events rather than discovering readiness on a 30-second requeue, and
`readyAt` is sourced from the node's own `NodeReady` condition transition time rather than
from the moment the controller noticed. The dataset reported here predates that fix and
stays quantised exactly as described; the fix does not retrospectively improve any figure
already reported. The medians and the null result on location in section 3.3 are unaffected
either way, because the same grid applied to every run. See ADR 0026,
`docs/adr/0026-observe-node-readiness-rather-than-poll-for-it.md`.

### 3.3 Location makes no measurable difference

Restricted to the pinned type `cx23`, so machine size does not confound location:

| Location | n | Median | Min | Max | Fast | Slow |
| --- | --- | --- | --- | --- | --- | --- |
| hel1 | 14 | 61.0 s | 60 | 91 | 8 | 6 |
| fsn1 | 5 | 61.0 s | 60 | 61 | 5 | 0 |
| nbg1 | 5 | 61.0 s | 60 | 94 | 3 | 2 |

The medians are identical to the second across all three locations. The design's
calibration expected 70 to 73 seconds and treated a difference of more than a few seconds
as real; no such difference appears. fsn1 drew fast five times out of five and nbg1 three
out of five, but at n = 5 per location the modal split is not separable from chance.

Instance type likewise shows no effect: `cx23` (n = 24) median 61.0 s, `cx33` (n = 3)
median 90.0 s, `cx43` (n = 3) median 61.0 s. At n = 3 the type arm resolves nothing.

### 3.4 The one outlier is a capacity failure, not a slow boot

`m1nbg04` recorded 94 s, the only value off the grid. Its `operator.log` holds 13
consecutive `create instance ... error during placement (resource_unavailable, ...)`
errors, all within three seconds of acceptance. No other run in the campaign logged a
placement error. The controller retried and succeeded. The retry backoff shifting the
reconcile schedule off the 30-second grid is the most likely reason this is the only value
that does not sit on it, and the artefacts do not settle it.

This is the design's caveat 4 observed directly: the catalogue's `Available` flag is coarse
and does not predict capacity exhaustion at create time. Selection reduces the failure rate;
it does not eliminate it. One run in 30, in nbg1.

### 3.5 Against the falsification criterion

The design states that a p95 above roughly three minutes would mean bursting for a
ten-minute job cannot pay for itself. The measured p95 is 91 seconds. The criterion is not
met.

## 4. M2, teardown by path and per injected failure

Fourteen boots were planned across five scenarios. Fourteen were executed: four labelled
`f0base*` plus the `smoke01` run form the unfaulted arm, and F1 to F4 as designed. Teardown
behaviour is reported across all 30 burst runs, since every run tears down.

### 4.1 Zero leaks, across every path, in every run

**Across all 30 burst runs and all 19 policy runs, 55 machines in total, every machine was
released and independently verified gone.** Every `harness.csv` ends with a
`cleanup-verified,zero-servers` row, which is written only after the harness queries the
Hetzner API directly and sees no server carrying the run's label. That is 49 runs, none of
which required manual intervention, including 6 policy runs that failed for other reasons
and 2 scenarios in which the operator was deliberately scaled to zero.

No instance survived past `expiresAt + maxLifetime + orphanExpiryGrace`. With the deployed
`maxLifetime` of 8 h and `orphanExpiryGrace` of 5 min, the bound is not tight; the largest
observed overshoot past `expiresAt` was 787 seconds, in F3, by design.

### 4.2 Results by enforcing path

Injection is performed 30 seconds after the readiness signal, in a privileged `hostPID` Job
that enters the host namespaces with `nsenter`.

| Scenario | Broken | n | Enforcing path | Result |
| --- | --- | --- | --- | --- |
| F0 none | nothing | 5 | Controller at expiry | `releasedAt` equals `expiresAt` exactly in all 5 |
| F1 control plane | operator scaled to 0 | 3 | Node wall clock | Server gone 5, 13 and 9 s after `watchdogDeadline`, and 920 to 955 s **before** the lease deadline |
| F2 node agent | `horizon-watchdog` stopped | 2 | Controller at expiry | `releasedAt` equals `expiresAt` exactly in both |
| F3 both | operator 0 and agent stopped | 2 | Neither, until the operator returned | Server gone 787 s after expiry in both, 4 and 5 s after the operator came back |
| F4 node token | token file overwritten, unit restarted | 2 | Controller at expiry | `releasedAt` equals `expiresAt` exactly in both |

Twenty-one unfaulted runs plus F2 and F4, 25 runs in total, took the controller path. In
**all 25**, the operator's `releasedAt` equals `expiresAt` to the second. The Hetzner API
observed the server absent 0 to 5 seconds later, median 3 seconds. That gap is at or below
the harness's 5-second poll interval in every run, so the split between "the controller
noticed" and "the machine went away" is not resolvable by this instrument. The design asked
for that split; the honest answer is that it is smaller than the measurement can see.

### 4.3 F1, the node's own clock enforcing with the control plane gone

F1 is the strongest result in the campaign and deserves its own numbers.

Each F1 run took a 20-minute lease, waited for readiness, then scaled the operator
Deployment to zero replicas. The operator publishes the renewable wall-clock deadline as
the `horizon.dev/watchdog-deadline` annotation on the node's own `Node` object; with the
operator gone it stops being renewed. The node-side agent reads that annotation with the
kubelet credential the machine already holds and fires at the earlier of it and a monotonic
backstop.

| Run | Lease deadline | Last written watchdog deadline | Server observed absent | Early by |
| --- | --- | --- | --- | --- |
| `f1cp01` | 17:38:00Z | 17:22:00Z | 17:22:05Z | 955 s |
| `f1cp02` | 19:36:01Z | 19:20:02Z | 19:20:15Z | 946 s |
| `f1cp03` | 19:40:33Z | 19:25:04Z | 19:25:13Z | 920 s |

Source: `expiresAt` and `watchdogDeadline` in each `lease-final.json`, and the
`hetzner,...,status,absent` row in each `harness.csv`.

The deployed watchdog policy is `renewInterval: 1m`, `slack: 2m`, so the deadline is written
three minutes ahead and only re-written once the current one is within `slack`. In all three
runs the operator died before the first renewal, leaving a deadline three minutes past
readiness, and the machine was gone within 5 to 13 seconds of it with no control plane
participating. `restored` rows in the same CSVs confirm the operator was returned only after
the server was already gone, so nothing cluster-side can account for the deletion.

Note what this does not prove. Both the deadline the node used and the token it deleted with
were placed by the operator before it died. The result is that teardown survives the
operator's absence, not that it needs no operator ever.

### 4.4 F3, the honest one

With both the operator and the node agent dead, nothing collects. The orphan sweeper lives
inside the operator, so removing the operator removes it too. The guarantee is that teardown
survives the failure of **either** clock, not both.

Both F3 runs behaved exactly that way. The machine outlived its lease deadline by 787
seconds in both runs, and was collected 5 s (`f3both01`) and 4 s (`f3both02`) after the
operator's Deployment was scaled back up. Both machines outlived their own watchdog
deadlines by 1117 and 1116 seconds, because the agent that would have honoured them had been
stopped.

The 787-second overshoot is not a property of horizon. It is how long the harness waited
before restoring the operator. What the runs establish is the shape: collection resumes
within one reconcile of the operator returning, and the finalizer plus the confirmed-absent
delete still hold.

### 4.5 What F2 and F4 actually exercised

F2 stops the `horizon-watchdog` unit. F4 overwrites `/etc/horizon/token` with an invalid
value and restarts the unit, verifying the systemd `InvocationID` changed so the running
process cannot still hold the old token in memory.

In both scenarios the `node,...,watchdog-armed` rows in `harness.csv` stop advancing
immediately after the injection: `f2agent01` records its last armed value at t = 141 s
against an injection at t = 136 s, and `f4token01` records nothing after t = 94 s against an
injection at t = 112 s. So F4 as executed did not produce "an agent alive whose delete
fails"; it produced an agent that stopped reporting at all. ADR 0021 records that the agent
proves its identity with a provider `Get` at startup and exits non-zero if that fails, which
is the obvious explanation, but the artefacts show only that the annotation stopped
advancing. Either way F2 and F4 exercise the same enforcing path from different causes, and
F4 is weaker evidence than the design intended it to be.

## 5. M3, cost per burst

### 5.1 The rounding rule

Hetzner's published billing documentation states, verbatim: "We always round up the hourly
usage of a server." The sentence that follows it makes the consequence explicit, that a
server created for only a few minutes is still billed for one whole hour. The same page
states that primary IPs and cloud servers are billed separately and listed separately on
invoices. All of it was read from https://docs.hetzner.com/cloud/billing/faq/ while writing
this document.

`createOpts` does not set `PublicNet`, so every burst node receives a primary IPv4, billed
hourly and separately. From `hetzner-pricing.json` exported by every run, at hel1, net of
VAT: `cx23` EUR 0.0088/h, `cx33` EUR 0.0136/h, `cx43` EUR 0.0256/h, `cpx62` EUR 0.2083/h,
primary IPv4 EUR 0.0008/h at every location. A sub-hour `cx23` burst therefore costs EUR
0.0088 plus EUR 0.0008, EUR 0.0096 net.

### 5.2 Theoretical against billed

Theoretical is `duration x rate x replicas`. Billed is `ceil(lifetime / 1h) x rate x
replicas`. Actual lifetime is derived from each instance's `createdAt` in
`lease-final.json` against the `status,absent` row in `harness.csv`.

| Type and lease | n | Median lifetime | Theoretical EUR | Billed EUR | Billed / theoretical | Billed / actual |
| --- | --- | --- | --- | --- | --- | --- |
| cx23, 5 m | 10 | 301 s | 0.00080 | 0.0096 | 12.0x | 12.0x |
| cx23, 10 m | 11 | 603 s | 0.00160 | 0.0096 | 6.0x | 6.0x |
| cx23, 20 m | 3 | 253 s | 0.00320 | 0.0096 | 3.0x | 14.2x |
| cx33, 5 m | 3 | 302 s | 0.00120 | 0.0144 | 12.0x | 11.9x |
| cx43, 5 m | 3 | 301 s | 0.00220 | 0.0264 | 12.0x | 12.0x |

The design's headline is confirmed exactly: a ten-minute burst pays for a full hour, six
times theoretical. The 20-minute row is the F1 arm, where the node destroyed itself early;
its theoretical ratio is only 3x because the lease was long, while its ratio against actual
lifetime is 14.2x because the machine lived 253 seconds. Both readings are correct and they
disagree, which is exactly why the design asked for three figures rather than one.

Across the whole campaign, 55 machines, all billed at one hour each:

| Quantity | Value |
| --- | --- |
| Billed, net | EUR 1.2705 |
| Unrounded at observed lifetimes, net | EUR 0.1090 |
| Overall rounding premium | 11.7x |

Derived, not invoiced. The billed figure is `machines x effective hourly rate` under the
documented rounding rule; the unrounded figure is derived from measured lifetimes.

### 5.3 The third leg is missing

The design required all three figures to be reconciled, with the third read from a Hetzner
usage export, and stated that the ceil-to-hour rule "must be confirmed against a real
invoice before publication". **That confirmation did not happen.** The Hetzner Cloud API
exposes no usage or billing endpoint, so it could not be automated, and no invoice covering
20 and 21 August exists in the artefact set. The rounding rule is confirmed against
Hetzner's published documentation instead, which is weaker evidence than an invoice.

Two further costs are named for completeness and are not measured here: snapshot storage,
billed per GB-month while the image exists, and any traffic beyond the included allowance.
Neither appears in the campaign artefacts.

## 6. M4, two sizing policies against a pinned baseline

### 6.1 Setup

Three arms, expressed against the live CRD:

| Arm | Request |
| --- | --- |
| Baseline | `spec.size: cx23`, pinned |
| Policy A | `spec.requirements` with `minCPU: 2`, `minMemory: 4Gi`, `architecture: x86`, `strategy: LowestPrice` |
| Policy B | Same requirements, `strategy: LowestPricePerCore` |

The workload is a fixed synthetic quantum, `scripts/quantum.py`: 48 shards of 15,000,000
iterations of a deterministic linear congruential generator modulo 2^61 - 1, 720,000,000
iterations in total, with worker count set to `min(cores, 48)`. The shard count divides
every core count on offer, so no machine idles a worker on the tail. The output is a
SHA-256 over the joined per-shard accumulators, and every run is checked against a reference
checksum recorded before the campaign at
`var/measure-policy-runs/reference/20260807-15000000.checksum`.

**Every completed unit in every arm produced checksum
`b841e3262a3713c567e42b7957ec6c2c114cae3a0705d949babd209648dcd22f`**, matching the
reference, and every `results.jsonl` records `iterations: 720000000` and
`shardIterations: 15000000`. The harness reports a suggested recalibration after each run
and it was never applied, deliberately, because changing the work parameter between arms
would invalidate the comparison. The arms therefore ran identical work and produced
identical answers, and only the time and the price differ.

**One consequence of the requirements is load-bearing and was not intended.** `minMemory:
4Gi` is 4,294,967,296 bytes. Hetzner reports `cx23` memory as 4, which
`internal/provider/hetzner/instancetype.go` converts as decimal GB to 4,000,000,000 bytes,
so `internal/controller/capacitylease_sizing.go` excludes `cx23` from both policy arms'
candidate sets. Neither policy could ever have selected the baseline's machine. The
comparison is between a pinned `cx23` and the cheapest machine meeting 2 cores and 4 GiB,
not between a pinned machine and an unconstrained choice.

### 6.2 Replicas 1

Three verified runs per arm, as designed. Source: `cost.json` and `summary.txt` per run.

| Arm | n | Types selected | Elapsed median | Elapsed range | Cost per quantum | Quanta/h per EUR, median | Rounding premium |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Baseline | 3 | cx23 | 304.7 s | 274.6 to 337.9 s | EUR 0.0096 | 1231 | 8.4 to 9.6x |
| Policy A, LowestPrice | 3 | cx33 | 310.6 s | 77.8 to 344.0 s | EUR 0.0144 | 805 | 8.0 to 23.5x |
| Policy B, LowestPricePerCore | 3 | cx33 x2, cx43 x1 | 81.3 s | 76.0 to 158.9 s | EUR 0.0144 median, 0.0144 to 0.0264 | 1795 | 15.1 to 23.4x |

On cost per completed quantum the pinned baseline wins outright: every arm pays exactly one
billed hour per machine for a sub-hour workload, so cost per quantum reduces to the hourly
rate, and the baseline's machine is the cheapest one running. On throughput per euro, policy
B wins and policy A comes last, behind the baseline it was supposed to improve on.

An earlier note from this campaign reported that LowestPrice "wins by 2.6x on throughput per
euro". That was a single sample, `m4-policy-a-r1-w1` at 77.8 s, and the completed arm
reverses it: the other two policy A runs took 344.0 s and 310.6 s on the same instance type.
The reversal is recorded here because it is the clearest illustration of why the design
specified three runs per arm.

Two further cx23 replicas-1 runs exist, `m4-fixcheck-a` (380.064 s) and `m4-fixcheck-b`
(307.992 s). They are not part of the baseline arm: they ran on the rebuilt node image and
concurrently with each other. They are used in section 6.3 and section 7.

### 6.3 Replicas 3, and what scaling out actually buys

Two replicas-3 runs completed: `m4-r3-fixcheck` (baseline, cx23) and `m4-policy-b-r3`
(policy B, which selected cpx62). Both ran on the rebuilt image; see section 7 for why an
earlier replicas-3 attempt did not complete.

| Replicas 3 | Type | Elapsed per unit | Cost per quantum | Quanta/h per EUR | Rounding premium |
| --- | --- | --- | --- | --- | --- |
| Baseline | cx23, 2 cores | 583.2, 650.0, 679.1 s | EUR 0.0096 | 552 | 4.34x |
| Policy B | cpx62, 16 cores | 12.7, 16.4, 17.0 s | EUR 0.2091 | 1012 | 36.73x |

**Three concurrent nodes each run slower than one alone.** Comparing cx23 against cx23 on
the same image:

| Configuration | n units | Elapsed | Mean |
| --- | --- | --- | --- |
| Replicas 1, rebuilt image (`m4-fixcheck-a`, `m4-fixcheck-b`) | 2 | 308.0, 380.1 s | 344.0 s |
| Replicas 3, rebuilt image (`m4-r3-fixcheck`) | 3 | 583.2, 650.0, 679.1 s | 637.4 s |

That is 1.85x slower per unit. Against the three replicas-1 baseline runs on the earlier
image (274.6, 304.7, 337.9 s, mean 305.7 s) the ratio is 2.09x. Either way, three concurrent
cx23 machines each took roughly twice as long as a single one doing the same fixed work.

The cause is not established. Placement, three shared-vCPU instances landing on contended
physical hosts, is the obvious hypothesis, and nothing in the artefacts tests it: the
Hetzner API exposes no placement information at this tier and none was captured. The two
`m4-fixcheck` runs are themselves two concurrent machines rather than one alone, which if
anything makes the 1.85x figure conservative.

What the replicas-3 repeat was for, per design section 5.7, is to check whether the
conclusion survives more than one machine. It survives on cost and fails on time. Cost per
quantum is **identical** at replicas 1 and replicas 3 for the baseline, EUR 0.0096, because
three machine-hours buy three quanta. Throughput per euro **more than halves**, from 1231 to
552. Horizontal scale-out on shared vCPU buys parallelism at no cost penalty per unit of
work and far less wall-clock speedup than the core count implies.

The rounding premium moves the same way for the same reason: 8.4 to 9.6x on single cx23
runs, 4.34x on the slower replicas-3 cx23 units, and 36.73x on cpx62. A faster machine
wastes more of the hour it has already paid for. That is the cost model demonstrated rather
than argued.

### 6.4 The two metrics disagree, and neither is wrong

At replicas 3, on the same work with the same verified checksum:

| Metric | Baseline (cx23) | Policy B (cpx62) | Winner | Margin |
| --- | --- | --- | --- | --- |
| Cost per completed quantum | EUR 0.0096 | EUR 0.2091 | Baseline | 21.8x |
| Quanta per hour per EUR | 552 | 1012 | Policy B | 1.83x |

Cost per quantum answers "what did one unit of work cost". Below one hour of runtime it is
decided entirely by the hourly rate, because every arm pays exactly one hour regardless, so
the cheapest machine wins by construction and the policy cannot express anything. Throughput
per euro-hour answers "how much work does a euro of running time buy", which ignores the
rounding and rewards the faster machine.

Both are true of the same two runs, six work units. Reporting only the first makes cost-aware selection
look worthless; reporting only the second makes it look free. The reconciliation is the
threshold below.

### 6.5 The crossover threshold

The design calls this the interesting number. It is computed from measured throughput and
the documented ceil-to-hour rule, and it is derived rather than measured: no run was
executed at the crossover volume.

Inputs, from `m4-r3-fixcheck` and `m4-policy-b-r3` `results.jsonl` and `cost.json`:

```
cx23    mean 637.414 s per quantum  ->  3600 / 637.414 =   5.648 quanta per machine-hour
cpx62   mean  15.370 s per quantum  ->  3600 /  15.370 = 234.223 quanta per machine-hour

effective hourly rate, net:  cx23 EUR 0.0096      cpx62 EUR 0.2091
```

For N quanta run sequentially on one machine of each type, billing whole hours on both
sides:

```
cost_baseline(N) = ceil(N / 5.648)   x 0.0096
cost_policyB(N)  = ceil(N / 234.223) x 0.2091
```

| N quanta | Baseline hours | Baseline EUR | Policy B hours | Policy B EUR | Cheaper |
| --- | --- | --- | --- | --- | --- |
| 1 | 1 | 0.0096 | 1 | 0.2091 | Baseline |
| 96 | 17 | 0.1632 | 1 | 0.2091 | Baseline |
| 118 | 21 | 0.2016 | 1 | 0.2091 | Baseline |
| **119** | **22** | **0.2112** | **1** | **0.2091** | **Policy B** |
| 200 | 36 | 0.3456 | 1 | 0.2091 | Policy B |
| 235 | 42 | 0.4032 | 2 | 0.4182 | Baseline |
| 243 | 44 | 0.4224 | 2 | 0.4182 | Policy B |
| 500 | 89 | 0.8544 | 3 | 0.6273 | Policy B |
| 1000 | 178 | 1.7088 | 5 | 1.0455 | Policy B |

**LowestPricePerCore first becomes cheaper per quantum at N = 119**, about 21 cx23
machine-hours of the same work. Both sides of that number are measured; only the
extrapolation to volumes larger than were run is modelled.

The crossover is not permanent on first crossing. Both cost curves are step functions and
they interleave: the ordering reverts to the baseline for N = 235 to 242, where cpx62 has
just entered its second billed hour and cx23 has not yet accumulated enough hours to pass
it, and then stays with policy B for every N up to 3000. Asymptotically, ignoring rounding,
policy B costs EUR 0.000893 per quantum against the baseline's EUR 0.0017, a factor of 1.9.

The conclusion the design anticipated holds: cost-aware selection is real, its payoff begins
above a threshold, and below that threshold the correct advice is to pin the cheapest
machine that fits.

### 6.6 Shared vCPU variance is the dominant effect, and a price list does not express it

Design section 5.7 listed this as caveat 2. It is the headline.

| Type | n verified units | Elapsed values | Spread |
| --- | --- | --- | --- |
| cx33 | 5 | 77.8, 81.3, 158.9, 310.6, 344.0 s | **4.42x** |
| cx23, replicas 1 | 5 | 274.6, 304.7, 308.0, 337.9, 380.1 s | 1.38x |
| cx23, replicas 1, earlier image only | 3 | 274.6, 304.7, 337.9 s | 1.23x |
| cx23, replicas 1, rebuilt image only | 2 | 308.0, 380.1 s | 1.23x |
| cx23, replicas 3, one run | 3 | 583.2, 650.0, 679.1 s | 1.16x |
| cpx62, replicas 3, one run | 3 | 12.7, 16.4, 17.0 s | 1.34x |

The same instance type, in the same region, on identical fixed work, varied more than
fourfold on cx33 across five runs. The published price list expresses none of this. That
bounds how finely any selection policy can be separated: a 4.4x throughput spread on one
type swamps the 1.8x throughput-per-euro gap between the best and worst arm.

Note also that cx33 at 4 cores hit 77.8 s while cx43 at 8 cores hit 76.0 s, and that perfect
linear scaling from a 300 s two-core anchor would put 8 cores near 75 s. A four-core machine
reaching eight-core throughput is host generation or placement, not core count.

### 6.7 The policy's own output is not stable

LowestPricePerCore selected three different instance types across four verified runs with
byte-identical requirements: cx43 once, cx33 twice, cpx62 once. LowestPrice selected cx33 in
all three of its runs.

Selection is latched into `status.instanceType` at acceptance and is not revisited
mid-lease, which is deliberate and recorded as a limitation. What moved between runs is the
catalogue the operator selected from, since the request did not change.

**The cause cannot be recovered from the artefacts.** Selection filters on per-location
availability, and the harness's `hetzner-server-types.json` export is the `/v1/server_types`
payload, which carries no per-location availability field. Reconstructing the candidate set
from the exported artefacts yields cx53 as the LowestPricePerCore answer in all seven policy
runs, which is not what any of them selected, so the reconstruction is missing an input
rather than contradicting the operator. The operator logs captured for these runs contain
only reconcile errors and record no selection decision. This is an instrumentation gap: the
one field that would make the policy's choice explicable was not exported, and the operator
does not log its own reasoning. The second half of that gap has since been closed. The
operator now records in `status.selection` how many types the catalogue offered, how many
qualified, which one won, which came second and what rejected the rest, so a later campaign
can recover the reasoning that these runs cannot.

What is recorded and trustworthy is the outcome: `status.instanceType` in each
`lease-final.json`, which is the type actually provisioned and billed.

## 7. A latent defect found by measurement, not by reading code

### 7.1 What happened

The campaign was scheduled to run the three M4 arms concurrently, on the correct reasoning
that Hetzner bills a full hour per machine regardless of lifetime or concurrency, so
concurrency is free. That reasoning was verified. That concurrency **worked** was not, and
all 30 burst boots had been strictly sequential, verified by comparing every run's start
instant against the previous run's server-absent instant: no two burst runs overlap.

Under concurrency, joins became unreliable. From `lease-final.json` per run:

| Wave | Concurrent leases | Machines | Joined | Reached Ready | Still Ready at end | Quanta verified |
| --- | --- | --- | --- | --- | --- | --- |
| 21:02:37 | 2 | 2 | 2 | 2 | 1 | 1 |
| 21:22:39 | 3 | 3 | 1 | 1 | 1 | 1 |
| 22:25:52 | 1 lease, replicas 3 | 3 | 1 | 1 | 1 | 0 |
| 22:55:09 (diagnosis) | 2 | 2 | 2 | 2 | 1 | 0 |

The replicas-3 row never satisfied `InstancesReady`, which stayed
`False / WaitingForNodes / "1 of 3 nodes ready"` for the whole lease, so the lease never
reached `Active` and the quantum was never started.

Two distinct signatures appear. In the 21:22 wave and in the replicas-3 lease the failing
instances sat at phase `Created` with an empty `nodeName` and never joined at all. In the 21:02 and 22:55 waves
both nodes joined and reached Ready, and then the **earlier** of the two lost readiness: in
the 21:02 wave `m4-policy-b-r1-w1` reached ready at 21:03:39 and `m4-policy-a-r1-w1` at
21:03:40, and the earlier one ended with `InstancesReady: False` and `WatchdogArmed: False`
while the later one completed its work; in the 22:55 pair `m4-diag-a` reached ready at
22:56:41 and lost `InstancesReady` and `WatchdogArmed` thirty seconds later, while
`m4-diag-b` at 22:56:51 stayed Active.

### 7.2 The root cause

Diagnosed with two deliberate concurrent boots and an SSH session into the failing node,
recorded in the execution ledger at
`bedrock/.superpowers/sdd/2026-08-07-horizon-spec-2-instrumentation/progress.md`. That
evidence is not in the run artefacts, which is itself the point of section 8.

Both nodes reported the **same Tailscale NodeID** and the same tailnet address, and the same
`/etc/machine-id`. They were not two devices sharing an address; they were one device. The
node image, built by bedrock's Packer definition, baked `/etc/machine-id` into the snapshot,
so every clone booted from it presented the same machine identity. The later registration
took the identity and the earlier node lost its route to the control plane, which explains
both signatures: a node that loses the route before joining never joins, and one that loses
it after joining goes NotReady.

Sequentially this is invisible. A node registers, is destroyed, and the next one takes the
same identity with nothing to conflict with. All 30 burst boots were sequential and all 30
worked. Concurrency is the only condition that exposes it, and nothing had run concurrently
before.

### 7.3 The fix, and its verification

bedrock commit `858d664e`, `fix(packer): clear the node identity before taking the
snapshot`, adds one provisioner after cloud-init settles and immediately before the snapshot
is taken. It empties `/etc/machine-id` so systemd regenerates it at first boot, removes
`/var/lib/dbus/machine-id`, removes `/var/lib/tailscale` so a baked `tailscaled.state`
cannot reproduce the same NodeID by a second route, and clears the SSH host keys last
because sshd needs them to accept any further connection.

It verifies itself in-build. The same provisioner runs `test ! -s /etc/machine-id` and
`test ! -e /var/lib/tailscale/tailscaled.state` before the snapshot, so a build that fails
to clear identity fails the build rather than shipping an image that looks fine and breaks
only under concurrency. The absence of that property is what let the defect hide across the
47 boots taken on the previous image, 39 of which produced graded results.

Verified afterwards against the exact scenario that failed:

| Check | Before the fix | After the fix |
| --- | --- | --- |
| Two concurrent single-replica leases | `m4-diag-a` lost readiness, both quanta failed | `m4-fixcheck-a` and `m4-fixcheck-b` both Ready, both quanta verified against the reference checksum, both torn down to zero servers |
| One replicas-3 lease | `m4-baseline-r3`: 1 of 3 joined, quantum never ran | `m4-r3-fixcheck`: 3 of 3 joined, 3 units verified |

### 7.4 What this is and is not

An earlier characterisation of this defect as "horizon cannot reliably join concurrent
nodes" and "a headline limitation of the shipped tool" was wrong, and is corrected here. The
defect was in bedrock's image build, not in horizon. ADR 0022 has horizon generate cloud-init
rather than build images precisely so that it works with any image, and a correctly built
image never had this problem.

What it does show is that a guarantee about teardown says nothing about a guarantee that the
capacity was ever usable. Teardown held throughout every one of these failures. Every failed
run, including the three concurrent-lease failures, the node that stopped reporting after
joining, and the partially joined replicas-3 lease, was released and verified to zero
servers. The guarantee was exercised repeatedly under conditions nobody planned, and it did
not leak a machine.

It also means any measurement taken after the fix is on a different image from the 47 boots
taken before it, of which 39 produced graded results: the 30 burst runs and the nine
verified replicas-1 policy runs. The prior image was deliberately retained rather than
pruned, so the dataset stays reproducible.

## 8. A diagnosability gap in the operator

This is the self-criticism, and it is the reason section 7 cost two boots and an SSH session
rather than a `kubectl describe`.

When instances reach phase `Created` but never `Joined`, the lease sits in `Provisioning`
until expiry and surfaces nothing that explains why. The conditions written by the operator
for the failing runs, read from `lease-final.json`:

```
m4-baseline-r3       InstancesReady  False  WaitingForNodes  "1 of 3 nodes ready"
m4-policy-a-r1-w2    InstancesReady  False  WaitingForNodes  "0 of 1 nodes ready"
m4-policy-b-r1-w2    InstancesReady  False  WaitingForNodes  "0 of 1 nodes ready"
```

The condition reports a count. It does not distinguish "the machine is still booting" from
"the machine booted a quarter of an hour ago and is never going to join", and no other
condition covers the gap. `m4-policy-a-r1-w2` held that identical message from 21:22:42Z
until the harness gave up and deleted the lease at 21:37:41Z, close to fifteen minutes,
while the instance carried a `providerID` and a `createdAt` and the provider had reported
it `running` since 21:22:57Z.

The operator already knows enough to say more: it has the instance's provider timestamp, it
has a registration timeout, and it has the distinction between phase `Created` and phase
`Joined`. Surfacing a condition when an instance has been `Created` past a threshold without
joining would have turned a two-boot investigation into a one-line read.

This gap is closed. The lease now reports which join stage is blocking it, naming the
blocking instance and how long it has been in that stage, so the message improves as time
passes rather than the first word arriving at the registration timeout. See ADR 0026,
`docs/adr/0026-observe-node-readiness-rather-than-poll-for-it.md`.

A second, smaller gap: the operator's Events were rejected. `f0base02/operator.log` records
`events.events.k8s.io is forbidden: User "system:serviceaccount:horizon-system:horizon"
cannot create resource "events" in API group "events.k8s.io" in the namespace "default"`,
dropping a `WatchdogUnarmed` warning for that lease. `CapacityLease` is cluster-scoped, so
its events land in `default`. The stated cause here was wrong: the chart's ClusterRole is
bound by a ClusterRoleBinding and was already granted in every namespace including
`default`, so the namespace was never the problem. The rule's `apiGroups` listed only `""`,
and the recorder had migrated to `events.k8s.io`, so the missing group is what dropped the
event. The warning existed and never reached anyone.

This is fixed. horizon commit `3e4779b`, `fix(chart): grant the events.k8s.io group on the
events rule`, adds `events.k8s.io` to the rule, and landed on 21 August, before this
document was committed. Verified against the live cluster: `kubectl auth can-i create
events.events.k8s.io` as the operator's service account returns `yes` in `default`.

## 9. The budget

The design planned 57 boots: M1 15, M2 14, M4 18, and 10 for pilots and re-runs. 55 were
spent. Counted from `status.instances[]` in every `lease-final.json` across both artefact
directories.

| Campaign | Runs | Machines |
| --- | --- | --- |
| Burst runs (M1, M2, M3, plus the smoke test) | 30 | 30 |
| Policy runs (M4) | 19 | 25 |
| **Total** | **49** | **55** |

The burst campaign spent 30 machines against 29 planned for M1 and M2, drawing one machine
from the 10-machine allowance for pilots and re-runs. That machine was the smoke test, which
retired the largest unproven risk in the campaign, the `nsenter` injection mechanism, before
any faulted run depended on it. Nine of the ten pilot and re-run machines went unspent on
the burst side.

M4 spent 25 machines against 18 planned. Every machine of the overrun is traceable to the
concurrency failures in section 7, and is accounted for below.

| Cost | Machines | Billed net |
| --- | --- | --- |
| Concurrent-lease join failures (`m4-policy-a-r1-w2`, `m4-policy-b-r1-w2`) | 2 | EUR 0.0408 |
| Node that stopped reporting after joining (`m4-policy-b-r1-w1`) | 1 | EUR 0.0264 |
| Replicas-3 attempt that got 1 of 3 (`m4-baseline-r3`) | 3 | EUR 0.0288 |
| Diagnosis pair (`m4-diag-a`, `m4-diag-b`) | 2 | EUR 0.0192 |
| **Wasted** | **8** | **EUR 0.1152** |
| Re-runs of the three lost data points (`-w1b`, `-w2b` x2) | 3 | EUR 0.0552 |
| Fix verification, which also produced usable results (`m4-fixcheck-a`, `-b`, `m4-r3-fixcheck`) | 5 | EUR 0.0480 |

Two further replicas-3 cycles, six machines, were cancelled rather than spent on a
configuration then known to fail.

Total billed for the whole campaign: EUR 1.2705 net, against an unrounded EUR 0.1090 at
observed lifetimes. The design budgeted under EUR 5, and money was never the binding
constraint. Attended wall-clock time was, and reducing boot count was the only lever,
because shortening a lease saves nothing under ceil-to-hour billing.

## 10. Threats to validity

**Time to ready is quantised by the instrument.** Section 3.2. While the campaign ran, the
30-second grid was the operator's own poll interval, so every reported value is an upper
bound within 30 seconds of the truth, and the bimodality is the grid rather than a property
of provisioning. The instrument has since been replaced, so this threat bounds the dataset
reported here and not any measurement taken after it. See ADR 0026,
`docs/adr/0026-observe-node-readiness-rather-than-poll-for-it.md`.

**Teardown latency is quantised at 5 seconds.** The gap between the operator recording
release and the provider reporting the machine gone is below the harness's poll interval in
all 25 controller-path runs. The design asked for that split and it is not resolvable here.

**The Prometheus dumps are empty.** Section 2.2. Nothing in this document is corroborated by
the operator's own metrics, because they were not yet wired when the burst campaign ran. The
harness timeline and the lease status are the only sources.

**The cost model is not invoiced.** Section 5.3. Billed cost is computed from the documented
rounding rule and measured lifetimes, not read from a bill.

**Small n throughout.** fsn1 and nbg1 are n = 5 each, the instance-type arm is n = 3 each, each
M4 replicas-1 arm is n = 3, and each replicas-3 configuration is a single run of three
units. Against a measured 4.4x throughput spread on one instance type, three samples cannot
separate arms that differ by less than roughly 2x. Ranges are reported rather than means
wherever the runs disagree.

**One image change spans the dataset.** The 47 boots up to and including `m4-diag-b` ran on
the earlier snapshot; `m4-fixcheck-a`, `m4-fixcheck-b`, `m4-r3-fixcheck` and
`m4-policy-b-r3` ran on the rebuilt one. The quantum's checksum is identical across both, so
the work is provably the same, but timing comparisons that span the change carry an
uncontrolled variable. Section 6.3's replicas-1 against replicas-3 comparison is stated
within the rebuilt image for exactly this reason.

**One campaign, one evening, one region for M4.** All M4 runs were in hel1 between 20:54 and
23:57 on 21 August. Shared-vCPU contention is time-of-day and host dependent, and a campaign
run at another hour could produce different absolute timings. The relative structure, that
variance within a type exceeds the gap between types, is the part most likely to hold.

**Two concurrent single-replica leases are not one machine alone.** `m4-fixcheck-a` and
`m4-fixcheck-b` ran simultaneously, so the replicas-1 side of the scale-out comparison
already carries some concurrency penalty.

**The policy arms could not select the baseline's machine.** Section 6.1. `minMemory: 4Gi`
excludes the 4 GB `cx23` by construction, so the arms differ in more than their strategy.

**Selection cannot be re-derived from the artefacts.** Section 6.7.

## 11. What the numbers do not show

- **Nothing about other providers.** The teardown guarantee's second clock exists because
  Hetzner keeps billing a powered-off server and offers no server-side deadline. ADR 0021
  records the asymmetry: AWS ends billing on an in-guest shutdown with
  `InstanceInitiatedShutdownBehavior` set to terminate, and Google Compute Engine enforces
  `maxRunDuration` with `instanceTerminationAction` set to delete, and neither needs a
  delete-capable credential on the machine. The node-side watchdog measured here answers a
  Hetzner-shaped problem.
- **Nothing about long leases.** Every lease measured was 5 to 30 minutes. The monotonic
  backstop at `maxLifetime: 8h` was never approached, and no run tested a lease long enough
  for the watchdog deadline to be renewed more than a handful of times.
- **Nothing about workload migration.** No lease in the campaign set `spec.workload`, so
  affinity rewriting, drain, and placement restore are untested by these measurements.
- **Nothing about the operator under load.** At most three concurrent leases and at most
  three machines per lease, against one cluster running a single operator replica. Reconcile
  throughput, leader election failover and cache behaviour at scale are unmeasured.
- **Nothing about correctness of the second clock under clock tampering.** ADR 0021 records
  a manual proof from 3 August, with the node's wall clock stepped back three hours. That
  scenario was not repeated in this campaign.
- **No cause for the shared-vCPU variance.** Sections 6.3 and 6.6 quantify it and do not
  explain it. Placement is a hypothesis, not a finding.
- **No leak, but not a proof of no leak.** 55 machines released and verified across 49 runs
  is evidence, not a proof of the guarantee. The design's falsification criterion is that
  the count of instances alive past `expiresAt + maxLifetime + orphanExpiryGrace` must be
  zero. It is zero. That is the strongest thing this campaign can say, and it is a
  statement about 55 machines rather than about all machines.

## Appendix A. Per-run data

### A.1 Burst runs, n = 30

Source: `bedrock/var/measure-burst-runs/<run>/`. "Absent minus expiry" is the harness's
Hetzner observation of the server disappearing, relative to the lease deadline; negative
means the machine was gone before the lease expired.

| Run | Scenario | Region | Type | Lease | Time to ready s | Hetzner running s | Enforcing clock | Absent minus expiry s |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| smoke01 | none | hel1 | cx23 | 10m | 61 | 22 | controller | 0 |
| f0base01 | none | hel1 | cx23 | 10m | 91 | 22 | controller | 3 |
| f0base02 | none | hel1 | cx23 | 10m | 61 | 22 | controller | 4 |
| f0base03 | none | hel1 | cx23 | 10m | 91 | 22 | controller | 3 |
| f0base04 | none | hel1 | cx23 | 10m | 61 | 17 | controller | 3 |
| m1fsn01 | none | fsn1 | cx23 | 5m | 61 | 22 | controller | 0 |
| m1fsn02 | none | fsn1 | cx23 | 5m | 61 | 16 | controller | 0 |
| m1fsn03 | none | fsn1 | cx23 | 5m | 61 | 18 | controller | 1 |
| m1fsn04 | none | fsn1 | cx23 | 5m | 60 | 22 | controller | 4 |
| m1fsn05 | none | fsn1 | cx23 | 5m | 60 | 18 | controller | 1 |
| m1nbg01 | none | nbg1 | cx23 | 5m | 60 | 17 | controller | 5 |
| m1nbg02 | none | nbg1 | cx23 | 5m | 91 | 27 | controller | 5 |
| m1nbg03 | none | nbg1 | cx23 | 5m | 61 | 27 | controller | 0 |
| m1nbg04 | none | nbg1 | cx23 | 5m | 94 | 27 | controller | 4 |
| m1nbg05 | none | nbg1 | cx23 | 5m | 60 | 21 | controller | 3 |
| m1t33a | none | hel1 | cx33 | 5m | 91 | 27 | controller | 2 |
| m1t33b | none | hel1 | cx33 | 5m | 61 | 22 | controller | 2 |
| m1t33c | none | hel1 | cx33 | 5m | 90 | 22 | controller | 2 |
| m1t43a | none | hel1 | cx43 | 5m | 60 | 17 | controller | 1 |
| m1t43b | none | hel1 | cx43 | 5m | 91 | 27 | controller | 0 |
| m1t43c | none | hel1 | cx43 | 5m | 61 | 16 | controller | 5 |
| f2agent01 | agent | hel1 | cx23 | 10m | 91 | 22 | controller | 5 |
| f2agent02 | agent | hel1 | cx23 | 10m | 61 | 17 | controller | 3 |
| f4token01 | node-token | hel1 | cx23 | 10m | 61 | 22 | controller | 3 |
| f4token02 | node-token | hel1 | cx23 | 10m | 61 | 22 | controller | 4 |
| f1cp01 | control-plane | hel1 | cx23 | 20m | 60 | 22 | node wall clock | -955 |
| f1cp02 | control-plane | hel1 | cx23 | 20m | 61 | 22 | node wall clock | -946 |
| f1cp03 | control-plane | hel1 | cx23 | 20m | 91 | 22 | node wall clock | -920 |
| f3both01 | both | hel1 | cx23 | 10m | 90 | 28 | operator, on return | 787 |
| f3both02 | both | hel1 | cx23 | 10m | 91 | 27 | operator, on return | 787 |

Every row ends with `cleanup-verified,zero-servers` in its `harness.csv` and
`export-prometheus,0-series-0-failed`.

### A.2 Policy runs, n = 19

Source: `bedrock/var/measure-policy-runs/<run>/`. All on 21 August, hel1. "Joined" counts
instances in `status.instances[]` carrying a `nodeName`.

| Run | Started | Arm | Selected | Replicas | Machines | Joined | Quantum | Elapsed s per unit | Billed EUR |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| m4-cal-baseline-r1 | 20:54:44 | baseline | cx23, 2 cores | 1 | 1 | 1 | verified | 304.703 | 0.0096 |
| m4-policy-a-r1-w1 | 21:02:37 | policy-a | cx33, 4 cores | 1 | 1 | 1 | verified | 77.797 | 0.0144 |
| m4-policy-b-r1-w1 | 21:02:37 | policy-b | cx43, 8 cores | 1 | 1 | 1 | failed | none | 0.0264 |
| m4-baseline-r1-w2 | 21:22:39 | baseline | cx23, 2 cores | 1 | 1 | 1 | verified | 337.937 | 0.0096 |
| m4-policy-a-r1-w2 | 21:22:39 | policy-a | cx33, 4 cores | 1 | 1 | 0 | not-run | none | 0.0144 |
| m4-policy-b-r1-w2 | 21:22:39 | policy-b | cx43, 8 cores | 1 | 1 | 0 | not-run | none | 0.0264 |
| m4-policy-b-r1-w1b | 21:54:55 | policy-b | cx43, 8 cores | 1 | 1 | 1 | verified | 75.967 | 0.0264 |
| m4-baseline-r1-w3 | 21:58:22 | baseline | cx23, 2 cores | 1 | 1 | 1 | verified | 274.578 | 0.0096 |
| m4-policy-a-r1-w2b | 22:04:40 | policy-a | cx33, 4 cores | 1 | 1 | 1 | verified | 343.961 | 0.0144 |
| m4-policy-a-r1-w3 | 22:12:12 | policy-a | cx33, 4 cores | 1 | 1 | 1 | verified | 310.615 | 0.0144 |
| m4-policy-b-r1-w2b | 22:19:13 | policy-b | cx33, 4 cores | 1 | 1 | 1 | verified | 81.315 | 0.0144 |
| m4-policy-b-r1-w3 | 22:21:51 | policy-b | cx33, 4 cores | 1 | 1 | 1 | verified | 158.927 | 0.0144 |
| m4-baseline-r3 | 22:25:52 | baseline | cx23, 2 cores | 3 | 3 | 1 | not-run | none | 0.0288 |
| m4-diag-a | 22:55:09 | baseline | cx23, 2 cores | 1 | 1 | 1 | failed | none | 0.0096 |
| m4-diag-b | 22:55:19 | baseline | cx23, 2 cores | 1 | 1 | 1 | failed | none | 0.0096 |
| m4-fixcheck-a | 23:26:37 | baseline | cx23, 2 cores | 1 | 1 | 1 | verified | 380.064 | 0.0096 |
| m4-fixcheck-b | 23:26:45 | baseline | cx23, 2 cores | 1 | 1 | 1 | verified | 307.992 | 0.0096 |
| m4-r3-fixcheck | 23:36:48 | baseline | cx23, 2 cores | 3 | 3 | 3 | verified | 583.160, 650.011, 679.072 | 0.0288 |
| m4-policy-b-r3 | 23:55:22 | policy-b | cpx62, 16 cores | 3 | 3 | 3 | verified | 12.731, 16.359, 17.020 | 0.6273 |

### A.3 Verified policy runs, derived figures

Cost per quantum, quanta per hour per EUR and rounding premium are read from each run's
`cost.json`, which computes them from the observed lifetime and the pricing exported by that
same run.

| Run | Arm | Type | Cores | Replicas | EUR/h effective | Cost per quantum EUR | Quanta/h per EUR | Rounding premium |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| m4-cal-baseline-r1 | baseline | cx23 | 2 | 1 | 0.0096 | 0.0096 | 1231 | 9.33x |
| m4-baseline-r1-w2 | baseline | cx23 | 2 | 1 | 0.0096 | 0.0096 | 1110 | 8.43x |
| m4-baseline-r1-w3 | baseline | cx23 | 2 | 1 | 0.0096 | 0.0096 | 1366 | 9.57x |
| m4-fixcheck-a | baseline | cx23 | 2 | 1 | 0.0096 | 0.0096 | 987 | 7.33x |
| m4-fixcheck-b | baseline | cx23 | 2 | 1 | 0.0096 | 0.0096 | 1218 | 8.70x |
| m4-policy-a-r1-w1 | policy-a | cx33 | 4 | 1 | 0.0144 | 0.0144 | 3213 | 23.53x |
| m4-policy-a-r1-w2b | policy-a | cx33 | 4 | 1 | 0.0144 | 0.0144 | 727 | 8.02x |
| m4-policy-a-r1-w3 | policy-a | cx33 | 4 | 1 | 0.0144 | 0.0144 | 805 | 8.59x |
| m4-policy-b-r1-w1b | policy-b | cx43 | 8 | 1 | 0.0264 | 0.0264 | 1795 | 23.38x |
| m4-policy-b-r1-w2b | policy-b | cx33 | 4 | 1 | 0.0144 | 0.0144 | 3074 | 23.23x |
| m4-policy-b-r1-w3 | policy-b | cx33 | 4 | 1 | 0.0144 | 0.0144 | 1573 | 15.13x |
| m4-r3-fixcheck | baseline | cx23 | 2 | 3 | 0.0096 | 0.0096 | 552 | 4.34x |
| m4-policy-b-r3 | policy-b | cpx62 | 16 | 3 | 0.2091 | 0.2091 | 1012 | 36.73x |

## Appendix B. Where to check each claim

| Claim | Artefact |
| --- | --- |
| Time to ready per run | `measure-burst-runs/<run>/lease-final.json`, `status.acceptedAt` and `status.readyAt` |
| Provider state transitions | `measure-burst-runs/<run>/harness.csv`, rows with `source=hetzner` |
| Teardown instant | `measure-burst-runs/<run>/harness.csv`, the `hetzner,...,status,absent` row |
| Operator's own release instant | `measure-burst-runs/<run>/lease-final.json`, `status.releasedAt` |
| Node wall-clock deadline | `measure-burst-runs/<run>/lease-final.json`, `status.watchdogDeadline` |
| Fault injection outcome | `measure-burst-runs/<run>/harness.csv`, rows with `source=injection` |
| Estate empty after each run | `measure-*-runs/<run>/harness.csv`, the `cleanup-verified,zero-servers` row |
| Instance type actually provisioned | `measure-policy-runs/<run>/lease-final.json`, `status.instanceType` |
| Hetzner rates used | `measure-*-runs/<run>/hetzner-pricing.json` |
| Per-unit workload timing and checksum | `measure-policy-runs/<run>/<run>-q/results.jsonl` |
| Cost arithmetic per run | `measure-policy-runs/<run>/cost.json` and `summary.txt` |
| 30-second lease poll | `internal/controller/capacitylease_controller.go`, `DefaultPollInterval` and `nextPoll` |
| Memory floor comparison | `internal/controller/capacitylease_sizing.go`, `rejectionFor` |
| Decimal GB conversion | `internal/provider/hetzner/instancetype.go` |
| Watchdog deadline arithmetic | `internal/controller/capacitylease_watchdog.go`, `watchdogDeadline` |
| Node-side firing rule | `internal/agent/deadline.go`, `fired` |
| Image identity fix | bedrock commit `858d664e`, `infra/packer/cluster-node-snapshot.pkr.hcl` |
