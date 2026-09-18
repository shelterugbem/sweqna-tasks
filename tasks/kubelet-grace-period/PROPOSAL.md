# Proposal: kubelet-grace-period

**This file is the record, not the gate.** You propose by message, in six lines
(the format is in your invitation). Once Askable approves it, paste the approval
into this file, fill in the rest, and commit it. That way the thing you were
approved for is in the repository next to the thing you built.

One approved task at a time. Authoring against an unapproved repository is the
most expensive mistake available to you.

## Repository

- **URL:**
 https://github.com/kubernetes/kubernetes
- **Full commit SHA:**  e2a4a8777f423a096937a8d1c5d30469232ffe7e
- **Licence:**
 Apache-2.0
- **Primary language(s):**
Go
- **Your familiarity:** how you know this project — contributed, ran it in
  production, read it closely. Be specific; "I've used it" is not familiarity.
   I investigated the kubelet pod worker termination path, including "pkg/kubelet/pod_workers.go", the pod sync state machine, grace-period handling, and the "SyncTerminatingPod" execution path.

- **Your relationship to this code:** what you wrote or contributed. Writing
  about your own code is allowed; the question must be about observable
  behaviour derivable from the supplied source, not design rationale
  (`AUTHORING.md` §2).

   I did not write or contribute the Kubernetes code. This task analyzes observable behavior in the upstream Kubernetes source.

- **Rights basis:** `open-source`, `owned`, or `permissioned` (`AUTHORING.md` §2).
open-source
- **Licence flag:** for open source, permissive or copyleft? Copyleft needs a
  decision before you start. AGPL is out.
  permissive
- **For `permissioned`:** who owns it, and what permission exists. Askable
  confirms this with the owner before authoring starts. Do not attach private
  code to a proposal.
 N/A

## Question

- **Category:** one of Architecture · Root-Cause Analysis · Code Onboarding ·
  Security Behavior · API Integration · Change Correctness
   Change Correctness
- **The question, as you would put it to the participant:**
A kubelet pod worker is already terminating a pod with status.gracePeriod = 30. Three UpdatePod calls are then considered for the same pod: the first is the existing termination state, the second specifies a 30-second termination-grace-period override, and the third specifies a 10-second override. Trace the exact control flow through calculateEffectiveGracePeriod and UpdatePod for the second and third requests. For each request, state the returned gracePeriodShortened value, the resulting status.gracePeriod, whether the wasGracePeriodShortened branch can invoke cancelFn, and what value is stored in KillPodOptions.PodTerminationGracePeriodSecondsOverride. Then trace how the resulting pendingUpdate is consumed by startPodSync/the pod-worker loop and reaches SyncTerminatingPod. Explain why the repeated 30-second request and the later 10-second request take different branches. Cite the exact source locations and distinguish state changes made immediately by UpdatePod from changes made when the worker processes the pending update.

- **The answer, in two or three sentences:** you must already know it. A proposal
  whose answer you have not established is a research plan, not a proposal.
Once termination has been requested, kubelet allows the grace period to decrease but not increase. A new 10-second request therefore changes the stored "status.gracePeriod" from 60 to 10, marks "wasGracePeriodShortened", updates "PodTerminationGracePeriodSecondsOverride", and cancels the current pod sync so the terminating operation is retried with the shorter value. The original "terminatingAt" is not reset, so the second request does not create a new termination start time; the downstream termination path receives the shortened grace-period override.
## Why it takes investigation

- **Components involved:** the files, modules, or execution paths a correct
  answer has to cross.
  "pkg/kubelet/pod_workers.go" ("UpdatePod", "calculateEffectiveGracePeriod", "podSyncStatus", "mergeLastUpdate", and the terminating worker path); "pkg/kubelet/kubelet.go" ("SyncTerminatingPod" and "killPod"); and the container-runtime termination path that consumes the grace-period override.

- **Why a simple search is insufficient:** what a grep or a symbol lookup
  returns, and why that is not the answer.

  Why a simple search is insufficient: A search for "gracePeriod" shows both the interface and the pod-worker state updates, but does not by itself establish the complete behavior. The answer requires tracing the new kill request through the existing termination state, cancellation/requeue behavior, "SyncTerminatingPod", and the runtime kill path.

- **Two plausible wrong conclusions:** the ones a competent engineer reaches by
  stopping early. Say what makes each attractive and what kills it.

  1. The new 10-second request starts a completely fresh 10-second termination period. This is attractive because the new request carries a 10-second override, but it is contradicted by the fact that "terminatingAt" is preserved rather than reset.
  2. The original 60-second grace period remains because termination has already started. This is attractive because the pod is already in the terminating state, but it is contradicted by "calculateEffectiveGracePeriod", which explicitly permits a shorter grace period and triggers cancellation when it is shortened.

## Contamination probe

Paste the question into a frontier chat model with no repository access, no
attachments, no follow-ups. Record model, version, date, and the full answer.

Model/version: Claude sonnet5

Date: 2026-09-18

Full answer:
## Setup

Before either new call: `status.terminatingAt` is set, `status.startedTerminating = true` (worker is inside `SyncTerminatingPod`), `status.gracePeriod = 30`, and `status.ctx`/`status.cancelFn` are live (assigned by `startPodSync` for the current attempt). Both new calls come in as `UpdateType: SyncPodKill` with `KillPodOptions.PodTerminationGracePeriodSecondsOverride` set to 30 and 10 respectively, and I'll assume `pod.DeletionGracePeriodSeconds == nil` (the override is coming through `KillPodOptions`, not an apiserver delete), matching what "termination-grace-period override" describes.

All line numbers below are from `pkg/kubelet/pod_workers.go` (current master / v1.37.0 layout — stable since the `overridden` fix in #124063).

## Request 2 (30s override, same as current)

In `UpdatePod`: `!firstTime && status.IsTerminationRequested()` (line 858) is true but `UpdateType != SyncPodCreate`, so no early return. `status.IsFinished()` (867) is false. The "check for a transition to terminating" switch (872–899) is skipped entirely because `status.IsTerminationRequested()` is already true — **`becameTerminating` stays `false`**.

The grace-period switch (902–949) hits `case status.IsTerminationRequested()` (921) and calls, at line 933:

```go
gracePeriod, gracePeriodShortened := calculateEffectiveGracePeriod(status, pod, options.KillPodOptions)
```

Inside `calculateEffectiveGracePeriod` (1009–1040):
```go
gracePeriod := status.gracePeriod   // 30
overridden := false
// pod.DeletionGracePeriodSeconds is nil → skip
if override := options.PodTerminationGracePeriodSecondsOverride; override != nil { // 30
    if gracePeriod == 0 || *override < gracePeriod {   // 30==0? no. 30<30? no.
        ...                                             // body NOT entered
    }
}
// gracePeriod stays 30, overridden stays false; floor-at-1 doesn't fire
return gracePeriod, status.gracePeriod != 0 && status.gracePeriod != gracePeriod
//     = 30,        30 != 0 (true)     && 30 != 30 (false)  →  false
```

**Returned: `(30, false)`.**

Back in `UpdatePod` (935–939): `wasGracePeriodShortened = false`; `status.gracePeriod = 30` (rewritten, unchanged); `options.KillPodOptions.PodTerminationGracePeriodSecondsOverride = &gracePeriod` → a **new pointer to a fresh int64 holding 30**.

At line 1000:
```go
if (becameTerminating || wasGracePeriodShortened) && status.cancelFn != nil {
```
`false || false` → **condition is false regardless of `cancelFn`** — it is never even read due to short-circuiting. **`cancelFn` cannot fire.** `UpdatePod` falls through to the end of the function having only replaced `status.pendingUpdate` and signaled the (buffered, cap-1) channel.

## Request 3 (10s override)

Same path through `!firstTime`/`IsFinished`/`becameTerminating` (still `false`). `status.gracePeriod` is still 30 at this point — it's set synchronously in `UpdatePod` regardless of whether the worker has drained the earlier pending update, so request 3 sees 30 no matter what the goroutine has consumed so far.

`calculateEffectiveGracePeriod`:
```go
gracePeriod := status.gracePeriod   // 30
overridden := false
if override := options.PodTerminationGracePeriodSecondsOverride; override != nil { // 10
    if gracePeriod == 0 || *override < gracePeriod {   // 10 < 30 → true
        gracePeriod = *override   // 10
        overridden = true
    }
}
return gracePeriod, status.gracePeriod != 0 && status.gracePeriod != gracePeriod
//     = 10,        30 != 0 (true) && 30 != 10 (true)  →  true
```

**Returned: `(10, true)`.**

Back in `UpdatePod`: `wasGracePeriodShortened = true`; **`status.gracePeriod` becomes 10**; `options.KillPodOptions.PodTerminationGracePeriodSecondsOverride = &10`.

Line 1000: `(false || true) && status.cancelFn != nil` → true, given `status.cancelFn` is live (the worker is mid-sync). So:
```go
updateLogger.V(3).Info("Cancelling current pod sync", ...)
status.cancelFn()
return
```
**`cancelFn` fires.** `UpdatePod` returns immediately after calling it — it does not wait for the cancellation to be observed.

## Why the branches differ

`calculateEffectiveGracePeriod`'s second return value is `status.gracePeriod != 0 && status.gracePeriod != gracePeriod` — it compares the value **status.gracePeriod had on entry** to the **final computed value**, not to the requested override. The two `if gracePeriod == 0 || *override < gracePeriod` guards (1016, 1024) enforce "grace period only decreases": a 30s request against an existing 30s status is a no-op (equal, not less), so the final value equals the entry value → not shortened. A 10s request is strictly less, gets accepted, and the final value differs from the entry value → shortened. That boolean is exactly the signal `UpdatePod` uses to decide whether an in-flight `SyncTerminatingPod` needs to be interrupted — per the `podSyncer` interface contract (271–276), that method "should return context.Canceled" when its context is canceled, "which may occur when a shorter grace period is detected." An unchanged grace period has nothing for the current sync to react to, so no cancellation is needed; a shortened one does, so the kubelet wakes the syncer up rather than letting it run out the old, longer deadline.

## From pendingUpdate to SyncTerminatingPod (request 3's path)

1. `status.cancelFn()` cancels `status.ctx`, the same context passed into the in-flight `SyncTerminatingPod` call. That call returns `context.Canceled`.
2. In `podWorkerLoop`'s inner closure (1267–1328), that error propagates out; the outer switch (1330) matches `errors.Is(err, context.Canceled)` (1332) and just logs — no phase transition, no backoff-as-error.
3. `p.completeWork(logger, podUID, phaseTransition=false, err=context.Canceled)` runs (1371). Its `default` backoff branch (1535–1547) still enqueues a workQueue retry, but then (1552–1565) it re-checks `status.pendingUpdate`: since request 3 already set it and it hasn't been drained, it re-signals the (already-buffered) `podUpdates` channel rather than clearing `status.working`.
4. The outer `for range podUpdates` loop (1247) receives that token and calls `p.startPodSync(parentCtx, podUID)` again (1248).
5. `startPodSync` (1132–1223): consumes `status.pendingUpdate` into `update` and clears it (1156–1163); since `status.ctx.Err() != nil` now, it allocates a **fresh** `status.ctx`/`status.cancelFn` pair (1165–1168); since `status.IsStarted()` is true, it calls `status.mergeLastUpdate(update.Options)` (1173) — this is the point where `status.activeUpdate.KillPodOptions` (the value other kubelet components observe) actually picks up the 10s override — and returns `(ctx, update, true, true, true)`.
6. Back in `podWorkerLoop`, `update.WorkType == TerminatingPod` (1304): it pulls `gracePeriod := opt.PodTerminationGracePeriodSecondsOverride` from `update.Options.KillPodOptions` (1305–1308) — this is the `&10` written in step "Request 3" above — and calls
   ```go
   err = p.podSyncer.SyncTerminatingPod(ctx, update.Options.Pod, status, gracePeriod, podStatusFn)
   ```
   with the new, non-canceled `ctx` and a grace period of 10. This is where the 10-second value actually reaches the syncer/runtime.

For request 2 (no cancellation), the equivalent replay would only happen once the *current* `SyncTerminatingPod(…, &30, …)` call finishes on its own (success or a non-`Canceled` error) — the worker never gets kicked out of it early, and when it does replay, it does so with an equal 30s value, i.e. effectively a no-op resync.

## Immediate (inside `UpdatePod`, under `podLock`) vs. deferred (worker-side)

**Immediate, for both requests:**
- `status.gracePeriod` write (936) — happens before any goroutine touches the update.
- Building the new `KillPodOptions.PodTerminationGracePeriodSecondsOverride` pointer (939).
- `status.pendingUpdate = &options` (989) — wholesale replacement, not a queue; an undelivered prior pending update is discarded (only its already-applied side effects like `status.gracePeriod` and appended `notifyPostTerminating`/`statusPostTerminating` entries survive).
- `status.working = true` (993) and the non-blocking channel signal (995–998).
- Request 3 only: the call to `status.cancelFn()` itself (1002) is synchronous, but it only *requests* cancellation — it doesn't block for the syncer to actually stop.

**Deferred to the worker (`startPodSync`/`podWorkerLoop`):**
- Clearing `status.pendingUpdate` and copying it into `update` (1157–1158).
- Allocating the fresh `status.ctx`/`status.cancelFn` pair (1167).
- `status.activeUpdate` (the value downstream components read) is only updated via `mergeLastUpdate` (1173) — so there's a window where `status.gracePeriod` already reflects 10 but `status.activeUpdate.KillPodOptions` still reflects 30, until the worker actually reaches this point.
- The actual `SyncTerminatingPod` invocation with the new grace-period pointer (1315), and everything that follows it (`completeWork`, requeueing, etc.).

- **Result:** how many of your intended required facts it got.
7/7 required facts

- **Verdict:** if a no-access model answers it, the question is dead. Propose a
  different one rather than arguing that the model was lucky.
  the question is not sufficiently source-dependent because the fresh model reconstructed the required implementation details without repository access

## Environment and evidence

- **Mode:** `container` or `source-only`. Source-only needs both: every piece of
  evidence is source analysis, and the category is Architecture or Code
  Onboarding. Say which, and if source-only, why reading settles it. Askable
  signs that off here rather than at submission, because execution is how this
  task type gets its difficulty (`DIFFICULTY.md` §2).
  container
- **Planned runtime experiment:** what you will run, and which wrong conclusion
  it rules out.
  Build the pinned Kubernetes source in the task container and exercise the relevant pod-worker/termination path with an initial 60-second grace period followed by a 10-second kill request. The experiment should verify that the shorter request updates the effective grace period and interrupts/requeues the in-progress termination path rather than simply being ignored or starting a new termination timestamp.
- **Dependencies required, and the source of each asset:**

Kubernetes source at the pinned full commit SHA and its declared Go/module dependencies.
- **Anything that cannot run offline:** say so now, not at submission.
- Any dependency that cannot be provided by the pinned source/module cache must be identified during environment construction rather than assumed available.

## Related public material

Links to issues, discussions, blog posts, Stack Overflow answers, or benchmark
items that touch this question. Disclose them; do not omit them because they
weaken the proposal. An undisclosed near-duplicate found at review costs the task.

Kubernetes source and publicly documented Kubernetes issues concerning pod termination/grace-period behavior will be disclosed as related material where applicable. No external material is being used as a substitute for source evidence.
## Effort

- **Estimated author hours:**
4–6
- **Estimated independent solve time:**
30–60 minutes
- **Proposed submission date:**
2026-09-18
