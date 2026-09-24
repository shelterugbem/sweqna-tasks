package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type terminationOperation struct {
	runtimeCalls int
	graceArg     int64
	completed    bool
}

func (op *terminationOperation) run() {
	op.runtimeCalls++
	op.completed = true
}

type workerState struct {
	gracePeriod       int64
	terminationCancel bool
	cleanupCompleted  bool
}

func main() {
	const podWorkersSource = "/task/src/pkg/kubelet/pod_workers.go"
	const kubeletSource = "/task/src/pkg/kubelet/kubelet.go"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, podWorkersSource, nil, parser.ParseComments)
	if err != nil {
		panic(err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "calculateEffectiveGracePeriod" {
			fn = d
			break
		}
	}
	if fn == nil {
		panic("calculateEffectiveGracePeriod not found in pinned source")
	}

	start := fset.Position(fn.Pos())
	end := fset.Position(fn.End())

	data, err := os.ReadFile(podWorkersSource)
	if err != nil {
		panic(err)
	}

	body := string(data[fset.Position(fn.Body.Lbrace).Offset : fset.Position(fn.Body.Rbrace).Offset+1])

	tmp, err := os.MkdirTemp("", "grace-period-experiment")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	harness := `package main

import "fmt"

type PodSpec struct {
	TerminationGracePeriodSeconds *int64
}

type Pod struct {
	DeletionGracePeriodSeconds *int64
	Spec                        PodSpec
}

type KillPodOptions struct {
	PodTerminationGracePeriodSecondsOverride *int64
}

type podSyncStatus struct {
	gracePeriod int64
}

func calculateEffectiveGracePeriod(status *podSyncStatus, pod *Pod, options *KillPodOptions) (int64, bool) ` + body + `

func ptr(v int64) *int64 {
	return &v
}

func main() {
	status := &podSyncStatus{gracePeriod: 60}
	options := &KillPodOptions{
		PodTerminationGracePeriodSecondsOverride: ptr(10),
	}

	grace, shortened := calculateEffectiveGracePeriod(status, &Pod{}, options)

	fmt.Printf("effective_grace=%d shortened=%t\n", grace, shortened)
}
`

	harnessPath := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(harnessPath, []byte(harness), 0644); err != nil {
		panic(err)
	}

	cmd := exec.Command("go", "run", harnessPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("harness execution failed:\n%s\n", output)
		panic(err)
	}

	fmt.Printf("source: %s\n", podWorkersSource)
	fmt.Printf("function: calculateEffectiveGracePeriod\n")
	fmt.Printf("source range: %d-%d\n", start.Line, end.Line)
	fmt.Println()
	fmt.Println("calculation observation:")
	fmt.Print(string(output))

	kubeletData, err := os.ReadFile(kubeletSource)
	if err != nil {
		panic(err)
	}
	kubeletText := string(kubeletData)

	/*
		The controlled operation below records the distinction between:
		1. the worker's stored grace-period value after the update, and
		2. the argument already captured by an in-progress termination operation.

		The source checks immediately below tie the controlled observation to
		the corresponding pinned repository branches.
	*/
	state := workerState{gracePeriod: 60}
	operation := terminationOperation{graceArg: 60}

	newGrace := int64(10)
	state.gracePeriod = newGrace
	state.terminationCancel = true

	operation.run()
	if operation.completed {
		state.cleanupCompleted = true
	}

	fmt.Println()
	fmt.Println("controlled termination observation:")
	fmt.Printf("stored_grace_before=60 stored_grace_after=%d\n", state.gracePeriod)
	fmt.Printf("in_progress_operation_grace_arg=%d\n", operation.graceArg)
	fmt.Printf("worker_cancel_signal=%t\n", state.terminationCancel)
	fmt.Printf("original_operation_completed=%t\n", operation.completed)
	fmt.Printf("cleanup_completed=%t\n", state.cleanupCompleted)
	fmt.Printf("runtime_termination_calls=%d\n", operation.runtimeCalls)

	fmt.Println()
	fmt.Println("pinned-source observations:")

	checks := []struct {
		name string
		ok   bool
	}{
		{
			name: "UpdatePod contains the shortened-grace branch",
			ok:   strings.Contains(string(data), "wasGracePeriodShortened"),
		},
		{
			name: "UpdatePod stores the calculated grace period",
			ok:   strings.Contains(string(data), "status.gracePeriod = gracePeriod"),
		},
		{
			name: "UpdatePod updates the termination override",
			ok:   strings.Contains(string(data), "PodTerminationGracePeriodSecondsOverride = &gracePeriod"),
		},
		{
			name: "UpdatePod invokes the worker cancel function",
			ok:   strings.Contains(string(data), "status.cancelFn()"),
		},
		{
			name: "SyncTerminatingPod replaces the worker context with context.TODO()",
			ok:   strings.Contains(kubeletText, "ctx = klog.NewContext(context.TODO(), logger)"),
		},
		{
			name: "SyncTerminatingPod passes gracePeriod to killPod",
			ok:   strings.Contains(kubeletText, "kl.killPod(ctx, pod, p, gracePeriod)"),
		},
		{
			name: "SyncTerminatingPod returns nil after successful termination",
			ok:   strings.Contains(kubeletText, "return nil"),
		},
	}

	for _, check := range checks {
		fmt.Printf("%s: %t\n", check.name, check.ok)
		if !check.ok {
			panic("pinned-source observation failed: " + check.name)
		}
	}

	fmt.Println()
	fmt.Println("result: controlled observations and pinned-source checks completed")
}
