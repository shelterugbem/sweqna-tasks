# Reference answer — TASK

## Conclusion

When a pod is already terminating with a 60-second grace period and a new kill request specifies 10 seconds, kubelet accepts the smaller value: the pod worker effective grace period becomes 10 seconds.

The important distinction is between the stored termination state and the already-running termination operation. UpdatePod stores the shorter value in status.gracePeriod and in KillPodOptions.PodTerminationGracePeriodSecondsOverride, then calls status.cancelFn() because the grace period was shortened. However, SyncTerminatingPod replaces the incoming pod-worker context with context.TODO(). Therefore, cancellation of the pod-worker sync does not propagate to an already-running termination operation.

If the original 60-second termination operation succeeds, the worker can complete termination and move toward cleanup without making another runtime termination call merely because the 10-second update arrived. If the operation instead returns context.Canceled or another error, the worker follows its error path and the pending update can be processed again.

## Control flow

1. podWorkers.completeSync records status.terminatingAt and sets status.startedTerminating.
   Source: environment/src/pkg/kubelet/pod_workers.go, podWorkers.completeSync, lines 1406-1429.

2. podWorkers.UpdatePod processes the terminating pod and calls calculateEffectiveGracePeriod.
   Source: environment/src/pkg/kubelet/pod_workers.go, podWorkers.UpdatePod, lines 900-939.

3. calculateEffectiveGracePeriod starts from status.gracePeriod and only replaces it when an incoming override is smaller. With 60 stored and 10 supplied, it returns 10 and reports that the grace period was shortened.
   Source: environment/src/pkg/kubelet/pod_workers.go, calculateEffectiveGracePeriod, lines 1009-1040.

4. UpdatePod stores the resulting value in status.gracePeriod and places the same value in KillPodOptions.PodTerminationGracePeriodSecondsOverride. Because the value was shortened, it calls status.cancelFn().
   Source: environment/src/pkg/kubelet/pod_workers.go, podWorkers.UpdatePod, lines 921-939.

5. SyncTerminatingPod receives the pod-worker context but replaces it with a context based on context.TODO(). The source marks this with TODO #113606. Thus worker cancellation does not propagate into this running termination operation.
   Source: environment/src/pkg/kubelet/kubelet.go, Kubelet.SyncTerminatingPod, lines 2343-2348.

6. The pod-worker termination path passes the pending grace-period override into SyncTerminatingPod, which passes it to killPod.
   Source: environment/src/pkg/kubelet/pod_workers.go, podWorkers.podWorkerLoop, lines 1332-1360; environment/src/pkg/kubelet/kubelet.go, Kubelet.SyncTerminatingPod, lines 2343-2380.

7. killPod passes the override to the container runtime. killContainer applies the override and passes the resulting value to StopContainer.
   Sources: environment/src/pkg/kubelet/kubelet_pods.go, Kubelet.killPod, lines 1080-1092; environment/src/pkg/kubelet/kuberuntime/kuberuntime_manager.go, kubeGenericRuntimeManager.KillPod, lines 2103-2125; environment/src/pkg/kubelet/kuberuntime/kuberuntime_container.go, kubeGenericRuntimeManager.killContainer, lines 864-920.

8. A successful terminating sync leads the pod worker to completeTerminating. The context.Canceled branch does not complete termination; it expects the pending update to be processed again.
   Source: environment/src/pkg/kubelet/pod_workers.go, podWorkers.podWorkerLoop, lines 1332-1360; podWorkers.completeTerminating, lines 1431-1455.

## Elapsed time

terminatingAt records when termination began, but calculateEffectiveGracePeriod does not subtract elapsed time from a newly supplied 10-second override.

Therefore this is not calculated as 10 seconds minus time already elapsed since terminatingAt. The new value is compared with the stored grace period and the smaller value is selected.

Sources: environment/src/pkg/kubelet/pod_workers.go, podWorkers.completeSync, lines 1418-1428; calculateEffectiveGracePeriod, lines 1009-1040.

## Experiment

The focused experiment was run in the supplied Docker environment against the pinned source.

Command:
docker build -t sweqa-kubelet-grace-period tasks/kubelet-grace-period/environment/ >/dev/null && docker run --rm --network none sweqa-kubelet-grace-period go run /task/src/grace_period.go

Observed output:

    source: /task/src/pkg/kubelet/pod_workers.go
    function: calculateEffectiveGracePeriod
    source range: 1009-1040

    calculation observation:
    effective_grace=10 shortened=true

    controlled termination observation:
    stored_grace_before=60 stored_grace_after=10
    in_progress_operation_grace_arg=60
    worker_cancel_signal=true
    original_operation_completed=true
    cleanup_completed=true
    runtime_termination_calls=1

    pinned-source observations:
    UpdatePod contains the shortened-grace branch: true
    UpdatePod stores the calculated grace period: true
    UpdatePod updates the termination override: true
    UpdatePod invokes the worker cancel function: true
    SyncTerminatingPod replaces the worker context with context.TODO(): true
    SyncTerminatingPod passes gracePeriod to killPod: true
    SyncTerminatingPod returns nil after successful termination: true

    result: controlled observations and pinned-source checks completed

The controlled termination model demonstrates the distinction between the updated stored value and the already-running operation: the stored value changes from 60 to 10 while the original operation retains its 60-second argument, completes successfully, and reaches cleanup without an additional runtime termination call. The participant-facing instrumentation reports these observations; the interpretation comes from the pinned-source control flow described above.

## Boundaries

The inspected source establishes the kubelet-side state transition and control flow, but does not establish every final shutdown detail inside an external CRI implementation.

Within kubelet, killContainer can also reduce the grace period because a PreStop hook consumes time, termination ordering can consume time, and a minimum grace period is enforced before StopContainer is called.

Source: environment/src/pkg/kubelet/kuberuntime/kuberuntime_container.go, kubeGenericRuntimeManager.killContainer, lines 864-920.

## Summary

60s stored -> new 10s request -> calculateEffectiveGracePeriod returns 10s -> status.gracePeriod becomes 10s -> cancelFn is invoked -> SyncTerminatingPod uses a replacement context.TODO() -> the already-running termination operation is not interrupted by that worker cancellation -> if the original operation succeeds, the worker can complete termination without another runtime termination call solely because of the new request.
