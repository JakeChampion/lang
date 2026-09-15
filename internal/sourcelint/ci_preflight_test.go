package sourcelint

import (
	"strings"
	"testing"
)

func TestCILintRunsOutsideTheFullSuiteQueue(t *testing.T) {
	if _, ok := topLevelBlock(workflowSource(t, ciFile), "concurrency"); ok {
		t.Fatal("root CI must not queue lint behind a full compiler suite")
	}
	rootJobs := workflowJobs(t, ciFile)
	allowed := map[string]bool{"changes": true, "lint": true, "suite": true, "reap-lint": true}
	var suite ciJob
	for _, job := range rootJobs {
		if !allowed[job.id] {
			t.Errorf("unexpected root job %s bypasses the full-suite queue", job.id)
		}
		delete(allowed, job.id)
		if job.id == "suite" {
			suite = job
		}
	}
	if len(allowed) != 0 {
		t.Fatalf("missing preflight jobs: %v", allowed)
	}
	if suite.usesFile() != ciSuiteFile || suite.keys["name"] != "Full suite" ||
		suite.keys["needs"] != "[changes]" || suite.keys["if"] != "" || suite.keys["secrets"] != "inherit" ||
		suite.with["lanes"] != "${{ needs.changes.outputs.lanes }}" {
		t.Fatalf("full-suite caller must preserve lane selection, independent lint and secrets: %+v", suite)
	}
	for scope, level := range permissionRequests(workflowSource(t, ciSuiteFile)) {
		if suite.perms[scope] != level {
			t.Errorf("suite caller grants %s:%q, nested jobs require %q", scope, suite.perms[scope], level)
		}
	}
	if perms, _ := topLevelBlock(workflowSource(t, ciSuiteFile), "permissions"); strings.Contains(perms, ": write") {
		t.Fatal("ordinary suite jobs must retain read-only default permissions")
	}
	seen := map[string]bool{}
	for _, job := range ciJobs(t) {
		if seen[job.id] {
			t.Errorf("job %s is duplicated across orchestration layers", job.id)
		}
		seen[job.id] = true
	}
	on, _ := onBlock(workflowSource(t, ciSuiteFile))
	for _, event := range []string{"pull_request", "push", "workflow_dispatch"} {
		if _, ok := triggerBlock(on, event); ok {
			t.Errorf("suite must be reached through preflight, not a %s trigger", event)
		}
	}
}
