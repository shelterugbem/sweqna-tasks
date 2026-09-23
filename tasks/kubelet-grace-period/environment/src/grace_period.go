package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	const source = "/task/src/pkg/kubelet/pod_workers.go"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, source, nil, parser.ParseComments)
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

	data, err := os.ReadFile(source)
	if err != nil {
		panic(err)
	}

	body := string(data[
		fset.Position(fn.Body.Lbrace).Offset:
		fset.Position(fn.Body.Rbrace).Offset+1])

	fmt.Printf("source: %s\n", source)
	fmt.Printf("function: calculateEffectiveGracePeriod\n")
	fmt.Printf("source range: %d-%d\n", start.Line, end.Line)
	fmt.Println("executing the exact pinned repository function body")

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
	fmt.Printf("scenario 1: initial=60 override=10 effective=%d shortened=%t\n", grace, shortened)

	status = &podSyncStatus{gracePeriod: 60}
	options = &KillPodOptions{
		PodTerminationGracePeriodSecondsOverride: ptr(60),
	}

	grace, shortened = calculateEffectiveGracePeriod(status, &Pod{}, options)
	fmt.Printf("scenario 2: initial=60 override=60 effective=%d shortened=%t\n", grace, shortened)
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

	fmt.Println()
	fmt.Println("actual execution output:")
	fmt.Print(string(output))
}
