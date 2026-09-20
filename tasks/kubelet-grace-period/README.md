# kubelet-grace-period
This task investigates how the Kubernetes kubelet pod worker handles a shorter termination grace-period request while termination is already in progress, including state updates, cancellation, elapsed-time handling, and propagation into the container runtime.
## Build, start, reset, smoke-test
```bash
docker build -t sweqa-kubelet-grace-period environment/
docker run --rm --network none sweqa-kubelet-grace-period go run /task/src/grace_period.go
```
## File map
- `instruction.md` — participant-facing investigation question and requirements.
- `environment/` — reproducible source and focused experiment.
- `reference/` — verified answer and evidence.
- `evaluation/` — rubric and grading examples.
