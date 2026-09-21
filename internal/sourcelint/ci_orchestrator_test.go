package sourcelint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ci.yml starts lint independently and calls ci-suite.yml after lane selection.
// Pin complete lane coverage, the two full-suite queue lanes, unchanged
// required lint checks, and lossless queuing of ready PR suites.

const ciFile = "ci.yml"
const ciSuiteFile = "ci-suite.yml"
const ciMainFile = "ci-main.yml"

// The reusable failure reaper called once per lane.
const reaperFile = "cancel-on-failure.yml"

// ciJob is one entry of a workflow's `jobs:` mapping, read textually like every
// other gate here: the scalar keys, plus the `with:` and `permissions:`
// sub-mappings.
type ciJob struct {
	id    string
	keys  map[string]string // name, uses, needs, if, secrets, ...
	with  map[string]string
	perms map[string]string
}

// ciJobs returns both orchestration layers; lane and reaper IDs are disjoint.
func ciJobs(t *testing.T) []ciJob {
	t.Helper()
	return append(workflowJobs(t, ciFile), workflowJobs(t, ciSuiteFile)...)
}

func workflowJobs(t *testing.T, file string) []ciJob {
	t.Helper()
	src := workflowSource(t, file)
	body, ok := topLevelBlock(src, "jobs")
	if !ok {
		t.Fatalf("%s has no `jobs:` block", file)
	}
	var (
		jobLine = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`)
		keyLine = regexp.MustCompile(`^    ([a-z-]+):\s*(.*?)\s*$`)
		subLine = regexp.MustCompile(`^      ([A-Za-z0-9_-]+):\s*(.*?)\s*$`)
		jobs    []ciJob
		sub     string
	)
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if m := jobLine.FindStringSubmatch(line); m != nil {
			jobs = append(jobs, ciJob{id: m[1], keys: map[string]string{}, with: map[string]string{}, perms: map[string]string{}})
			sub = ""
			continue
		}
		if len(jobs) == 0 {
			continue
		}
		cur := &jobs[len(jobs)-1]
		if m := keyLine.FindStringSubmatch(line); m != nil {
			cur.keys[m[1]] = m[2]
			sub = m[1]
			continue
		}
		if m := subLine.FindStringSubmatch(line); m != nil {
			switch sub {
			case "with":
				cur.with[m[1]] = m[2]
			case "permissions":
				cur.perms[m[1]] = m[2]
			}
		}
	}
	if len(jobs) == 0 {
		t.Fatalf("%s: no jobs parsed: did the layout change?", file)
	}
	return jobs
}

// usesFile returns the workflow filename a `uses: ./.github/workflows/X` job
// calls, or "" for a job that runs steps of its own.
func (j ciJob) usesFile() string {
	const prefix = "./.github/workflows/"
	u := j.keys["uses"]
	if !strings.HasPrefix(u, prefix) {
		return ""
	}
	return strings.TrimPrefix(u, prefix)
}

// ciLanes returns leaf lane callers across both orchestration layers,
// excluding the suite wrapper and failure reapers, keyed by job id.
func ciLanes(t *testing.T) map[string]ciJob {
	t.Helper()
	lanes := map[string]ciJob{}
	for _, j := range ciJobs(t) {
		if f := j.usesFile(); f != "" && f != reaperFile && f != ciSuiteFile {
			lanes[j.id] = j
		}
	}
	if len(lanes) == 0 {
		t.Fatalf("%s calls no lane — did the `uses:` shape change?", ciFile)
	}
	return lanes
}

// laneFilter is one entry of ci.yml's lane table: the `paths:` or
// `paths-ignore:` filter the lane ran under when it triggered itself.
type laneFilter struct {
	Paths       []string `json:"paths"`
	PathsIgnore []string `json:"paths-ignore"`
}

// laneTable returns ci.yml's lane table, the JSON block under `LANES: |` in
// the `changes` job. It is the single place a lane's path filter lives now, so
// every gate that used to read a lane's `on:` block reads this instead.
func laneTable(t *testing.T) map[string]laneFilter {
	t.Helper()
	src := workflowSource(t, ciFile)
	lines := strings.Split(src, "\n")
	start, indent := -1, 0
	for i, l := range lines {
		if strings.TrimSpace(l) == "LANES: |" {
			start, indent = i, len(l)-len(strings.TrimLeft(l, " "))
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s has no `LANES: |` block — if the lane table moved, update this gate with it", ciFile)
	}
	var b strings.Builder
	for _, l := range lines[start+1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(l)-len(strings.TrimLeft(l, " ")) <= indent {
			break
		}
		b.WriteString(l)
		b.WriteString("\n")
	}
	table := map[string]laneFilter{}
	if err := json.Unmarshal([]byte(b.String()), &table); err != nil {
		t.Fatalf("%s: the lane table is not valid JSON: %v", ciFile, err)
	}
	if len(table) == 0 {
		t.Fatalf("%s: the lane table is empty", ciFile)
	}
	return table
}

// Only ci.yml may trigger on a pull request's pushes. A lane with its own
// `pull_request:` trigger runs outside the lock, so two pull requests would be
// running CI at once again — silently, since the lane would still report.
// Lifecycle hooks (`types: [closed]`, `synchronize`) launch no suite and are
// exempt.
func TestCIIsTheOnlyPullRequestLane(t *testing.T) {
	for _, wf := range workflowFiles(t) {
		src := workflowSource(t, wf)
		on, ok := onBlock(src)
		if !ok {
			continue
		}
		pr, ok := triggerBlock(on, "pull_request")
		if !ok {
			continue
		}
		if strings.Contains(pr, "types:") || wf == ciFile {
			continue
		}
		t.Errorf("%s triggers on pull_request on its own, outside %s's lock — make it a "+
			"`workflow_call` and call it from %s", wf, ciFile, ciFile)
	}
	for _, wf := range workflowFiles(t) {
		src := workflowSource(t, wf)
		on, ok := onBlock(src)
		if !ok || !strings.Contains(on, "workflow_call:") {
			continue
		}
		if _, ok := triggerBlock(on, "push"); ok {
			t.Errorf("%s is a called lane that also triggers on push: main would run it "+
				"twice, once on its own and once inside %s", wf, ciFile)
		}
		if _, ok := topLevelBlock(src, "concurrency"); ok && wf != ciSuiteFile {
			t.Errorf("%s is a called lane with a workflow-level concurrency group. The "+
				"suite lock belongs to %s; a lane must not acquire another lock", wf, ciSuiteFile)
		}
	}
}

// Every `workflow_call` lane is called by the orchestration layers once, under the
// lane's own `name:` — the caller job's name is the prefix of every check the
// lane reports (`Test units / test-units-x86_64`) and the lane main-red.yml
// files issues under — and with the repository's secrets passed through.
func TestCICallsEveryLane(t *testing.T) {
	called := map[string][]string{}
	for id, j := range ciLanes(t) {
		called[j.usesFile()] = append(called[j.usesFile()], id)
	}
	for _, wf := range workflowFiles(t) {
		src := workflowSource(t, wf)
		on, ok := onBlock(src)
		if !ok || !strings.Contains(on, "workflow_call:") || wf == reaperFile || wf == ciFile || wf == ciSuiteFile {
			continue
		}
		ids := called[wf]
		if len(ids) != 1 {
			t.Errorf("%s is a `workflow_call` lane called by %d job(s) of %s (%v); it must be "+
				"called by exactly one", wf, len(ids), ciFile, ids)
			continue
		}
		delete(called, wf)
		job := ciLanes(t)[ids[0]]
		want, ok := workflowName(src)
		if !ok {
			t.Fatalf("%s has no top-level `name:`", wf)
		}
		if got := job.keys["name"]; got != want {
			t.Errorf("%s: job %q calls %s under the name %q; the lane's own name is %q, "+
				"and the two must agree or its checks and its main-red issues change name",
				ciFile, ids[0], wf, got, want)
		}
		if job.keys["secrets"] != "inherit" {
			t.Errorf("%s: job %q does not pass `secrets: inherit` to %s — a secret the lane "+
				"reads would come back empty, silently", ciFile, ids[0], wf)
		}
	}
	for wf, ids := range called {
		if _, err := os.Stat(filepath.Join("..", "..", ".github", "workflows", wf)); err != nil {
			t.Errorf("%s: job %v calls %s, which does not exist", ciFile, ids, wf)
			continue
		}
		t.Errorf("%s: job %v calls %s, which does not declare `workflow_call`", ciFile, ids, wf)
	}
}

// The PR group must be a QUEUE. GitHub's default keeps one pending run per
// group and cancels it when the next arrives, so with two pull requests waiting
// behind a running suite the older waiter is silently dropped — its CI never
// runs and nothing says so. `queue: max` holds up to 100 and starts them in
// order; it forbids `cancel-in-progress: true`, which is why a newer push to a
// PR is superseded by reap-stale-runs.yml rather than here.
//
// There are TWO such queues, split on the parity of the pull request number,
// because the runner ceiling fits two suites and one suite leaves half of it
// idle (docs/CI-PERFORMANCE.md). Both halves are checked: a group that stops
// varying per PR collapses them into one lane and restores the wait this split
// removed, and a digit dropped from the parity test sends nine PRs in ten to
// the same lane while the other idles — neither failure shows up as anything
// but a slow queue.
//
// actionlint does not know the key yet, so .github/actionlint.yaml ignores
// that one message for this one file; the ignore is pinned too, because
// dropping it would fail the lint lane on every pull request.
func TestCIQueuesPullRequests(t *testing.T) {
	src := workflowSource(t, ciSuiteFile)
	conc, ok := topLevelBlock(src, "concurrency")
	if !ok {
		t.Fatalf("%s has no workflow-level concurrency group — nothing serialises pull requests", ciSuiteFile)
	}
	setting := func(key string) string {
		for _, l := range strings.Split(conc, "\n") {
			if s, ok := strings.CutPrefix(strings.TrimSpace(l), key+":"); ok {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	if got := setting("queue"); got != "max" {
		t.Errorf("%s: concurrency.queue is %q, not `max`: GitHub keeps ONE pending run per "+
			"group by default and cancels it when the next arrives, so the second pull "+
			"request to wait loses its CI", ciSuiteFile, got)
	}
	if got := setting("cancel-in-progress"); got != "false" {
		t.Errorf("%s: cancel-in-progress is %q; it must be `false` — `queue: max` forbids "+
			"`true`, and a cancelled run here is another pull request's turn taken", ciSuiteFile, got)
	}
	if !strings.Contains(conc, "github.event_name == 'pull_request'") ||
		!strings.Contains(conc, "format('pr-ci-{0}'") {
		t.Errorf("%s: pull requests do not land in a `pr-ci-` group — the group name is what "+
			"queues them", ciSuiteFile)
	}
	for _, lane := range []string{"'even'", "'odd'"} {
		if !strings.Contains(conc, lane) {
			t.Errorf("%s: the PR group never names lane %s, so every pull request queues in "+
				"one lane and half the runner pool stays idle", ciSuiteFile, lane)
		}
	}
	// The lane is the parity of the PR number, so the digits tested must be
	// exactly the even ones: a missing digit quietly sends that tenth of pull
	// requests to the wrong lane, an extra one unbalances the split the other way.
	parity := regexp.MustCompile(`endsWith\(format\('\{0\}', github\.event\.pull_request\.number\), '(\d)'\)`)
	var digits []string
	for _, m := range parity.FindAllStringSubmatch(conc, -1) {
		digits = append(digits, m[1])
	}
	sort.Strings(digits)
	if got := strings.Join(digits, ""); got != "02468" {
		t.Errorf("%s: the PR lane is chosen on digits %q, not the even digits 02468 — "+
			"expressions have no `%%`, so every even digit needs its own endsWith term",
			ciSuiteFile, got)
	}
	// `inputs` in a called workflow's `concurrency` cannot be verified offline, and
	// an expression that evaluates to empty would put every PR in one lane silently.
	if strings.Contains(conc, "inputs.") {
		t.Errorf("%s: the PR group reads `inputs`; if a called workflow's concurrency key "+
			"cannot see it the group collapses to one lane with nothing reporting it",
			ciSuiteFile)
	}
	if !strings.Contains(conc, "github.run_id") {
		t.Errorf("%s: a workflow_dispatch has no per-run group, so a manual run would wait "+
			"in — or hold — a queue that is not its own", ciSuiteFile)
	}

	cfg, err := os.ReadFile(filepath.Join("..", "..", ".github", "actionlint.yaml"))
	if err != nil {
		t.Fatalf("read .github/actionlint.yaml: %v", err)
	}
	if !strings.Contains(string(cfg), `unexpected key "queue" for "concurrency" section`) ||
		!strings.Contains(string(cfg), ".github/workflows/"+ciSuiteFile+":") {
		t.Errorf(".github/actionlint.yaml no longer ignores the `queue` key for %s — the "+
			"pinned actionlint rejects it, and `make actionlint` runs in the lint lane", ciSuiteFile)
	}
}

// Main coalesces only pending runs. The active suite must finish, while the
// shared implementation must not acquire the outer lock a second time.
func TestMainCICoalescesPendingRuns(t *testing.T) {
	main := workflowSource(t, ciMainFile)
	conc, ok := topLevelBlock(main, "concurrency")
	if !ok || !strings.Contains(conc, "group: ci-main-latest") ||
		!strings.Contains(conc, "cancel-in-progress: false") {
		t.Fatal("main must share one lock and preserve the active run")
	}
	for _, line := range strings.Split(conc, "\n") {
		if queue, ok := strings.CutPrefix(strings.TrimSpace(line), "queue:"); ok && strings.TrimSpace(queue) != "single" {
			t.Fatal("main must retain only one pending run, using the default or queue: single")
		}
	}
	inner, _ := topLevelBlock(workflowSource(t, ciSuiteFile), "concurrency")
	if strings.Contains(inner, "ci-main-latest") || !strings.Contains(inner, "format('ci-run-{0}', github.run_id)") {
		t.Fatal("nested CI must use its own per-run group outside the PR queue and main lock")
	}
	jobs := workflowJobs(t, ciMainFile)
	if len(jobs) != 1 || jobs[0].usesFile() != ciFile || jobs[0].keys["name"] != "Validate" ||
		jobs[0].keys["secrets"] != "inherit" || jobs[0].keys["if"] != "" {
		t.Fatal("main must call the shared CI unconditionally as Validate with inherited secrets")
	}
	for scope, level := range permissionRequests(workflowSource(t, ciFile)) {
		if jobs[0].perms[scope] != level {
			t.Errorf("main caller grants %s: %q, nested CI requires %q", scope, jobs[0].perms[scope], level)
		}
	}
	perms, _ := topLevelBlock(workflowSource(t, ciFile), "permissions")
	if strings.Contains(perms, ": write") || !strings.Contains(perms, "contents: read") {
		t.Fatal("ordinary CI jobs must narrow the main caller's token to read-only")
	}
}

// Every lane but lint is gated on the `changes` job, by its own row of the
// lane table, under its own job id. The three have to agree by name: a lane
// gated on another lane's row runs for the wrong changes, and a row nothing
// reads is a filter that filters nothing.
func TestCILaneFiltersMatchTheirJobs(t *testing.T) {
	lanes := ciLanes(t)
	table := laneTable(t)

	for id, j := range lanes {
		if id == alwaysRunOnPR {
			if _, ok := table[id]; ok {
				t.Errorf("%s: %q is in the lane table, but it must run on every pull request — "+
					"it is the one gate a doc-only PR still reports", ciFile, id)
			}
			if j.keys["needs"] != "" || j.keys["if"] != "" {
				t.Errorf("%s: %q must run unconditionally, but has needs=%q if=%q",
					ciFile, id, j.keys["needs"], j.keys["if"])
			}
			continue
		}
		if _, ok := table[id]; !ok {
			t.Errorf("%s: lane %q has no row in the lane table, so nothing decides whether "+
				"it runs", ciFile, id)
		}
		if j.keys["needs"] != "" {
			t.Errorf("%s: lane %q must use its passed lane map without another dependency; needs=%q",
				ciSuiteFile, id, j.keys["needs"])
		}
		want := "fromJSON(inputs.lanes)['" + id + "']"
		if j.keys["if"] != want {
			t.Errorf("%s: lane %q is gated on `%s`, not on its own row `%s`",
				ciFile, id, j.keys["if"], want)
		}
	}
	var stale []string
	for id := range table {
		if _, ok := lanes[id]; !ok {
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("%s: the lane table has rows for %v, which no job reads", ciFile, stale)
	}
}

// permissionRequests returns the strongest level a workflow asks for per
// scope, across its workflow-level and job-level `permissions:` blocks.
func permissionRequests(src string) map[string]string {
	rank := map[string]int{"none": 0, "read": 1, "write": 2}
	out := map[string]string{}
	lines := strings.Split(src, "\n")
	entry := regexp.MustCompile(`^(\s*)([a-z-]+):\s*(none|read|write)\s*$`)
	for i, l := range lines {
		if strings.TrimSpace(l) != "permissions:" {
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " "))
		for _, m := range lines[i+1:] {
			if strings.TrimSpace(m) == "" || strings.HasPrefix(strings.TrimSpace(m), "#") {
				continue
			}
			sm := entry.FindStringSubmatch(m)
			if sm == nil || len(sm[1]) <= indent {
				break
			}
			if rank[sm[3]] > rank[out[sm[2]]] {
				out[sm[2]] = sm[3]
			}
		}
	}
	return out
}

// A called workflow can only narrow what its caller hands it, and GitHub
// checks that STATICALLY: a nested job asking for `write` on a scope the
// caller did not grant fails the whole run at startup, whether or not that
// job's `if:` would have run it. The repository's default token is read-only,
// so every `write` a lane asks for anywhere must be granted at its caller job.
// This PR's own first run failed exactly that way, on bootstrap's
// dispatch-only `release` job.
func TestCILanesAreGrantedWhatTheyRequest(t *testing.T) {
	for id, j := range ciLanes(t) {
		for scope, level := range permissionRequests(workflowSource(t, j.usesFile())) {
			if level != "write" {
				continue
			}
			if j.perms[scope] != "write" {
				t.Errorf("%s: lane %q asks for `%s: write` in %s but its caller job grants %q — "+
					"GitHub rejects the run at startup, even if the asking job is skipped",
					ciFile, id, scope, j.usesFile(), j.perms[scope])
			}
		}
	}
	reaper := permissionRequests(workflowSource(t, reaperFile))
	if reaper["actions"] == "write" {
		t.Errorf("%s declares permissions of its own; it takes them from each reap-* caller job", reaperFile)
	}
}

// The `changes` script has no test of its own inside ci.yml, so
// tools/ci-changes-selftest.mjs executes it against stubbed responses, and
// the lint lane is where that runs. Drop the step and the JS copy of the
// grammar is unpinned again while the Go copy stays green.
func TestChangesScriptIsSelfTested(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "..", "tools", "ci-changes-selftest.mjs")); err != nil {
		t.Fatalf("tools/ci-changes-selftest.mjs is missing: %v", err)
	}
	if !strings.Contains(workflowSource(t, "lint.yml"), "make ci-selftest") {
		t.Errorf("lint.yml no longer runs `make ci-selftest`, so nothing executes the `changes` script before it reaches a pull request")
	}
	mk, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	if !strings.Contains(string(mk), "node tools/ci-changes-selftest.mjs") {
		t.Errorf("Makefile's ci-selftest target no longer runs tools/ci-changes-selftest.mjs")
	}
	if !strings.Contains(string(mk), "node tools/main-red-selftest.mjs") {
		t.Error("ci-selftest must exercise main failure reporting across the wrapper transition")
	}
}
