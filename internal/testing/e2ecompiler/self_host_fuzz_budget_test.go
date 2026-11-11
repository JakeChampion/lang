package e2ecompiler

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func budgetFixture(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestBudgetFixture$")
	cmd.Env = append(os.Environ(), "FERN_BUDGET_FIXTURE="+mode)
	return cmd
}

// The parent control keeps its pages until the small target returns.
// The memory control makes the target itself exceed the same 512 MiB limit.
func TestBudgetFixture(t *testing.T) {
	mode := os.Getenv("FERN_BUDGET_FIXTURE")
	if mode == "" {
		return
	}
	switch mode {
	case "parent", "memory":
		pages := make([]byte, 576<<20)
		for i := 0; i < len(pages); i += 4096 {
			pages[i] = 1
		}
		if mode == "parent" {
			r := runUnderBudget(budgetFixture("output"))
			json.NewEncoder(os.Stdout).Encode(budgetStatus{r.exited, r.code, r.state, r.maxRSS})
		}
		runtime.KeepAlive(pages)
	case "output":
		input, _ := io.ReadAll(os.Stdin)
		fmt.Print("target:", string(input), ":", os.Getenv("FERN_BUDGET_VALUE"))
		fmt.Fprint(os.Stderr, "target-error")
		os.Exit(7)
	case "signal":
		syscall.Kill(os.Getpid(), syscall.SIGKILL)
	case "timeout":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func TestRunUnderBudget(t *testing.T) {
	t.Run("parent RSS", func(t *testing.T) {
		out, err := budgetFixture("parent").Output()
		if err != nil {
			t.Fatal(err)
		}
		var r budgetStatus
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("status %q: %v", out, err)
		}
		if !r.Exited || r.Code != 7 || r.MaxRSS <= 0 || r.MaxRSS >= 128<<20 {
			t.Fatalf("small target inherited parent RSS: %+v", r)
		}
	})
	t.Run("target RSS", func(t *testing.T) {
		r := runUnderBudget(budgetFixture("memory"))
		if !r.exited || r.code != 0 || r.maxRSS <= fuzzMemoryBudget || !strings.Contains(budgetFault("control", r, nil), "resident memory") {
			t.Fatalf("target memory did not fail the budget: %+v", r)
		}
	})
	t.Run("output and exit", func(t *testing.T) {
		cmd := budgetFixture("output")
		cmd.Env = append(cmd.Env, "FERN_BUDGET_VALUE=env")
		cmd.Stdin = strings.NewReader("input")
		r := runUnderBudget(cmd)
		if !r.exited || r.code != 7 || r.stdout != "target:input:env" || r.stderr != "target-error" || budgetFault("control", r, nil) != "" {
			t.Fatalf("target result changed: %+v", r)
		}
	})
	t.Run("signal", func(t *testing.T) {
		r := runUnderBudget(budgetFixture("signal"))
		if r.exited || r.code != -1 || !strings.Contains(r.state, "killed") || !strings.Contains(budgetFault("control", r, nil), "signal") {
			t.Fatalf("target signal did not fail the budget: %+v", r)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		r := runUnderBudget(budgetFixture("timeout"))
		if !r.timedOut || !strings.Contains(budgetFault("control", r, nil), "hung past 20s") {
			t.Fatalf("target timeout did not fail the budget: %+v", r)
		}
	})
	t.Run("missing target", func(t *testing.T) {
		r := runUnderBudget(exec.Command("/no/such/fern-budget-target"))
		if r.exited || r.code != -1 || !strings.Contains(r.state, "no such file") || budgetFault("control", r, nil) == "" {
			t.Fatalf("missing target did not fail the budget: %+v", r)
		}
	})
}
