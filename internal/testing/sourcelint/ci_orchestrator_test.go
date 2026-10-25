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

const ciFile = "ci.yml"
const ciSuiteFile = "ci-suite.yml"
const ciMainFile = "ci-main.yml"

const reaperFile = "cancel-on-failure.yml"
const preflightFile = "preflight.yml"

type ciJob struct {
	id    string
	file  string
	keys  map[string]string
	with  map[string]string
	perms map[string]string
}

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
			jobs = append(jobs, ciJob{id: m[1], file: file, keys: map[string]string{}, with: map[string]string{}, perms: map[string]string{}})
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

func (j ciJob) usesFile() string {
	const prefix = "./.github/workflows/"
	u := j.keys["uses"]
	if !strings.HasPrefix(u, prefix) {
		return ""
	}
	return strings.TrimPrefix(u, prefix)
}

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

type laneFilter struct {
	Paths       []string `json:"paths"`
	PathsIgnore []string `json:"paths-ignore"`
}

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
		if _, ok := topLevelBlock(src, "concurrency"); ok && wf != ciSuiteFile && wf != ciFile {
			t.Errorf("%s is a called lane with a workflow-level concurrency group. The "+
				"suite lock belongs to %s; a lane must not acquire another lock", wf, ciSuiteFile)
		}
	}
}

func TestCICallsEveryLane(t *testing.T) {
	called := map[string][]string{}
	for id, j := range ciLanes(t) {
		called[j.usesFile()] = append(called[j.usesFile()], id)
	}
	// The Preflight tier calls the x86-64 legs of some suite lanes a second
	// time; those calls name the same lane and pass `hosts: x86_64`.
	tier := map[string]ciJob{}
	for _, j := range workflowJobs(t, preflightFile) {
		if f := j.usesFile(); f != "" {
			tier[f] = j
		}
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
		if tj, ok := tier[wf]; ok {
			want, _ := workflowName(src)
			if tj.keys["name"] != want {
				t.Errorf("%s: job %q calls %s under the name %q, not the lane's own name %q", preflightFile, tj.id, wf, tj.keys["name"], want)
			}
			if tj.keys["secrets"] != "inherit" {
				t.Errorf("%s: job %q does not pass `secrets: inherit` to %s", preflightFile, tj.id, wf)
			}
			if strings.Contains(src, "inputs.hosts") && tj.with["hosts"] != "x86_64" {
				t.Errorf("%s: job %q calls %s without `hosts: x86_64`; the tier runs the x86-64 legs alone", preflightFile, tj.id, wf)
			}
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
		if _, err := os.Stat(filepath.Join("..", "..", "..", ".github", "workflows", wf)); err != nil {
			t.Errorf("%s: job %v calls %s, which does not exist", ciFile, ids, wf)
			continue
		}
		t.Errorf("%s: job %v calls %s, which does not declare `workflow_call`", ciFile, ids, wf)
	}
}

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
	if strings.Contains(conc, "inputs.") {
		t.Errorf("%s: the PR group reads `inputs`; if a called workflow's concurrency key "+
			"cannot see it the group collapses to one lane with nothing reporting it",
			ciSuiteFile)
	}
	if !strings.Contains(conc, "github.run_id") {
		t.Errorf("%s: a workflow_dispatch has no per-run group, so a manual run would wait "+
			"in — or hold — a queue that is not its own", ciSuiteFile)
	}

	cfg, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "actionlint.yaml"))
	if err != nil {
		t.Fatalf("read .github/actionlint.yaml: %v", err)
	}
	if !strings.Contains(string(cfg), `unexpected key "queue" for "concurrency" section`) ||
		!strings.Contains(string(cfg), ".github/workflows/"+ciSuiteFile+":") {
		t.Errorf(".github/actionlint.yaml no longer ignores the `queue` key for %s — the "+
			"pinned actionlint rejects it, and `make actionlint` runs in the lint lane", ciSuiteFile)
	}
}

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
		// A suite lane reads the lane map ci.yml passes it; a lane ci.yml runs
		// itself, outside the suite queue, reads the `changes` job's output.
		wantNeeds, want := "", "fromJSON(inputs.lanes)['"+id+"']"
		if j.file == ciFile {
			wantNeeds, want = "[changes]", "fromJSON(needs.changes.outputs.lanes)['"+id+"']"
		}
		if j.keys["needs"] != wantNeeds {
			t.Errorf("%s: lane %q has needs=%q, want %q",
				j.file, id, j.keys["needs"], wantNeeds)
		}
		if j.keys["if"] != want {
			t.Errorf("%s: lane %q is gated on `%s`, not on its own row `%s`",
				j.file, id, j.keys["if"], want)
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

func TestBootstrapLaneWatchesTheSmokeSources(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(root, "bootstrap", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var queue []string
	for _, m := range regexp.MustCompile(`\$ROOT/([A-Za-z0-9_./-]+\.fern)`).FindAllStringSubmatch(string(script), -1) {
		queue = append(queue, m[1])
	}
	if len(queue) == 0 {
		t.Fatal("bootstrap/bootstrap.sh names no $ROOT/*.fern source; if the smoke moved, update this gate with it")
	}
	paths := laneTable(t)["bootstrap"].Paths
	relImport := regexp.MustCompile(`(?m)^import "(\.\.?/[^"]+)";`)
	seen := map[string]bool{}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		selected := false
		for _, pat := range paths {
			selected = selected || globMatches(pat, p)
		}
		if !selected {
			t.Errorf("%s: the bootstrap lane's paths do not select %s, which the bootstrap smoke compiles", ciFile, p)
		}
		src, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Errorf("read %s: %v", p, err)
			continue
		}
		for _, m := range relImport.FindAllStringSubmatch(string(src), -1) {
			queue = append(queue, filepath.ToSlash(filepath.Join(filepath.Dir(p), m[1]+".fern")))
		}
	}
}

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

func TestChangesScriptIsSelfTested(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "..", "..", "scripts", "ci-changes-selftest.mjs")); err != nil {
		t.Fatalf("scripts/ci-changes-selftest.mjs is missing: %v", err)
	}
	mk, err := os.ReadFile(filepath.Join("..", "..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	lint := workflowSource(t, "lint.yml")
	fast := regexp.MustCompile(`(?m)^lint-fast:(.*)$`).FindStringSubmatch(string(mk))
	runsSelftest := strings.Contains(lint, "make ci-selftest") ||
		(strings.Contains(lint, "lint-fast") && fast != nil && strings.Contains(" "+fast[1]+" ", " ci-selftest "))
	if !runsSelftest {
		t.Errorf("lint.yml no longer runs `make ci-selftest` (directly or through `make lint-fast`), so nothing executes the `changes` script before it reaches a pull request")
	}
	if !strings.Contains(string(mk), "node scripts/ci-changes-selftest.mjs") {
		t.Errorf("Makefile's ci-selftest target no longer runs scripts/ci-changes-selftest.mjs")
	}
	if !strings.Contains(string(mk), "node scripts/main-red-selftest.mjs") {
		t.Error("ci-selftest must exercise main failure reporting across the wrapper transition")
	}
}
