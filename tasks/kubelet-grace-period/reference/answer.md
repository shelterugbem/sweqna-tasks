# Reference answer — TASK

## Conclusion

When a pod is already terminating with a 60-second grace period and a new kill request specifies 10 seconds, the pod worker shortens its effective grace period from 60 seconds to 10 seconds.

The new value is stored in `status.gracePeriod` and in the pending `KillPodOptions.PodTerminationGracePeriodSecondsOverride`. Because the grace period was shortened, the pod worker calls its cancellation function and reprocesses the pending termination update.

However, the currently running termination operation is not interrupted through the pod-worker context in this implementation. `SyncTerminatingPod` currently replaces the incoming pod-worker context with `context.TODO()`. Therefore, an already-running `killPod`/container-runtime termination can continue using the original 60-second request. The later pending update uses the shortened 10-second override.

The elapsed time since `terminatingAt` is not subtracted from the new 10-second value in this path. The 10 seconds is therefore not calculated as "10 seconds minus time already elapsed."

## Mechanism

1. When termination begins, `completeSync` records `status.terminatingAt` and sets `status.startedTerminating = true`.

2. While the pod is terminating, `UpdatePod` calls `calculateEffectiveGracePeriod`.

3. `calculateEffectiveGracePeriod` starts with the current `status.gracePeriod` and only accepts a new API-server or kubelet override when the new value is smaller. Thus a 10-second request changes an existing 60-second value to 10 seconds.

4. The function returns whether the existing grace period changed. Therefore the 60-to-10 transition makes `gracePeriodShortened` true.

5. `UpdatePod` stores the resulting value in `status.gracePeriod` and puts the same value into `KillPodOptions.PodTerminationGracePeriodSecondsOverride`.

6. Because the grace period was shortened, `UpdatePod` calls `status.cancelFn()`.

7. `SyncTerminatingPod` documents that it may be interrupted when the grace period is shortened. However, the implementation currently replaces the incoming pod-worker context with a new context based on `context.TODO()`. The source identifies this as TODO #113606.

8. `SyncTerminatingPod` then calls `killPod` using this replacement context and the supplied grace-period override.

9. `killPod` passes both the context and grace-period override to the container runtime.

10. `killContainer` applies the override directly and eventually passes the resulting grace period to `StopContainer`.

## Boundaries

The pod worker does shorten its recorded/effective grace period from 60 to 10 seconds and schedules the shortened termination update. However, the current `SyncTerminatingPod` implementation does not use the cancellable pod-worker context because it replaces it with `context.TODO()`.

The source examined does not show kubelet subtracting elapsed time from `terminatingAt` when calculating the new grace period. `terminatingAt` records when termination began, but the effective-grace-period calculation uses the current stored grace period and the new override.

The exact timing and behavior after the 10-second update reaches the CRI runtime can depend on the runtime implementation and the state of the containers at that point.

## Verification

The source-backed control flow is:

`UpdatePod`
→ `calculateEffectiveGracePeriod`
→ 60 seconds becomes 10 seconds
→ `wasGracePeriodShortened = true`
→ `status.cancelFn()`
→ pending termination update is processed
→ `SyncTerminatingPod`
→ `killPod`
→ container runtime
→ `killContainer`
→ `StopContainer(..., gracePeriod)`.

The source also shows that `terminatingAt` is set when termination begins but is not used by `calculateEffectiveGracePeriod` to subtract elapsed time. Therefore the new 10-second request is an effective grace-period value for subsequent termination processing, rather than a calculation of remaining time from the original 60-second deadline.
