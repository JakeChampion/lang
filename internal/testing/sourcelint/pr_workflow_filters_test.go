package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var docOnlyPathsIgnore = []string{
	"docs/**",
	"*.md",
}

var selfHostOnlyPaths = []string{
	"compiler/**",
	"internal/testing/e2ecompiler/**",
}

const dropSelfHostAction = "./.github/actions/drop-selfhost-sources"

const alwaysRunOnPR = "lint"

func TestPRLanesShareOneDocOnlyFilter(t *testing.T) {
	for lane, f := range laneTable(t) {
		switch {
		case len(f.Paths) > 0 && len(f.PathsIgnore) > 0:
			t.Errorf("%s: %q carries both `paths` and `paths-ignore`; GitHub requires both to "+
				"be satisfied, which no lane here means", ciFile, lane)
		case len(f.Paths) > 0:
		case len(f.PathsIgnore) == 0:
			t.Errorf("%s: %q has no path filter: a doc-only PR would launch it. Give it the "+
				"shared paths-ignore block or a `paths` allowlist.", ciFile, lane)
		case strings.Join(f.PathsIgnore, " ") == strings.Join(docOnlyPathsIgnore, " "):
		case strings.Join(f.PathsIgnore, " ") == strings.Join(append(append([]string{}, docOnlyPathsIgnore...), selfHostOnlyPaths...), " "):
			checkDropsSelfHostSources(t, lane)
		default:
			t.Errorf("%s: %q's paths-ignore is %v, not %v, optionally followed by %v — "+
				"the doc-only part must be identical across lanes, or a doc-only PR fires "+
				"some of them and not others",
				ciFile, lane, f.PathsIgnore, docOnlyPathsIgnore, selfHostOnlyPaths)
		}
	}
}

func checkDropsSelfHostSources(t *testing.T, lane string) {
	t.Helper()
	src := workflowSource(t, lane+".yml")
	jobs := jobBlocks(src)
	if len(jobs) == 0 {
		t.Fatalf("%s.yml: no jobs found — did the `jobs:` layout change?", lane)
	}
	for name, job := range jobs {
		checkout := strings.Index(job, "- uses: actions/checkout@")
		drop := strings.Index(job, "- uses: "+dropSelfHostAction+"\n")
		if checkout < 0 || drop < 0 {
			t.Errorf("%s.yml job %q skips self-host-only changes (ci.yml's lane table) but does "+
				"not run %s after checkout", lane, name, dropSelfHostAction)
			continue
		}
		if between := job[checkout:drop]; strings.Count(between, "\n      - ") > 1 {
			t.Errorf("%s.yml job %q runs a step between checkout and %s; delete the "+
				"self-host sources before anything else can read them", lane, name, dropSelfHostAction)
		}
	}
	action, err := os.ReadFile(filepath.Join("..", "..", "..", dropSelfHostAction[2:], "action.yml"))
	if err != nil {
		t.Fatalf("read %s: %v", dropSelfHostAction, err)
	}
	for _, p := range selfHostOnlyPaths {
		if dir := strings.TrimSuffix(p, "/**"); !strings.Contains(string(action), " "+dir) {
			t.Errorf("%s does not delete %s, which the lanes using it skip changes to", dropSelfHostAction, dir)
		}
	}
}

func onBlock(src string) (string, bool) {
	lines := strings.Split(src, "\n")
	start := -1
	for i, l := range lines {
		if l == "on:" || strings.HasPrefix(l, "on: ") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	var b strings.Builder
	for _, l := range lines[start+1:] {
		if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String(), true
}

func TestGateLanesRunOnMain(t *testing.T) {
	src := workflowSource(t, ciFile)
	on, ok := onBlock(src)
	if !ok {
		t.Fatalf("%s has no `on:` block", ciFile)
	}
	pr, ok := triggerBlock(on, "pull_request")
	if !ok {
		t.Fatalf("%s does not trigger on pull_request — nothing gates a pull request", ciFile)
	}
	if _, ok := triggerBlock(on, "push"); ok {
		t.Fatal("ci.yml must not also trigger on push: ci-main.yml already calls it")
	}
	if _, ok := triggerBlock(on, "workflow_call"); !ok {
		t.Fatal("ci.yml must be callable from main's entry workflow")
	}
	main := workflowSource(t, ciMainFile)
	mainOn, _ := onBlock(main)
	push, ok := triggerBlock(mainOn, "push")
	if !ok {
		t.Fatalf("%s must trigger on push to validate main", ciMainFile)
	}
	if !strings.Contains(push, "branches: [main]") {
		t.Errorf("%s: push trigger is not scoped to main — every branch would fire it "+
			"twice, once for the push and once for the PR", ciMainFile)
	}
	for name, block := range map[string]string{"pull_request": pr, "push": push} {
		if strings.Contains(block, "paths") {
			t.Errorf("the %s trigger must not skip an entire CI run with a path filter", name)
		}
	}

	if conc, ok := concurrencyBlock(main); ok && strings.Contains(conc, "github.sha") {
		t.Errorf("%s: main is keyed on the SHA, so every merge gets its own concurrency "+
			"group and waits on nothing. A burst of rebase-merges then holds an "+
			"uncancellable run each, ahead of every open PR (#8124). Key main on the "+
			"ref so a burst queues", ciFile)
	}
}

func triggerBlock(on, name string) (string, bool) {
	lines := strings.Split(on, "\n")
	start := -1
	for i, l := range lines {
		if l == "  "+name+":" || strings.HasPrefix(l, "  "+name+": ") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	var b strings.Builder
	for _, l := range lines[start+1:] {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "    ") {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String(), true
}

func concurrencyBlock(src string) (string, bool) {
	lines := strings.Split(src, "\n")
	start := -1
	for i, l := range lines {
		if l == "concurrency:" {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	var b strings.Builder
	for _, l := range lines[start+1:] {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, " ") {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String(), true
}
