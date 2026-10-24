package sourcelint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const autoRebaseFile = "auto-rebase-prs.yml"

func autoRebaseSource(t *testing.T) string {
	t.Helper()
	return workflowSource(t, autoRebaseFile)
}

func workflowSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestAutoRebasePushesSafely(t *testing.T) {
	src := autoRebaseSource(t)

	for _, want := range []struct{ needle, why string }{
		{
			`--force-with-lease="refs/heads/$branch:$head_sha"`,
			"a plain force-push overwrites a commit that landed while we were " +
				"rebasing; the lease pins the head the listing reported",
		},
		{
			`if [ "$fetched" != "$head_sha" ]`,
			"a branch that moved between the listing and the fetch is being " +
				"rebased from a head we never read",
		},
		{
			`if [ "$new_sha" = "$BASE_SHA" ]`,
			"a branch whose commits all landed in main already rebases to main " +
				"itself, and pushing that empties the pull request",
		},
		{
			`if [ "$same_repo" != "true" ]`,
			"no token here can write to a fork, so the push fails after the " +
				"branch was already reported rebased",
		},
		{
			`git rebase --abort`,
			"a conflicted rebase left in progress makes every later branch in " +
				"the loop fail to check out",
		},
		{
			`pr.base.ref !== base`,
			"a stacked pull request's base is not what just moved; rebasing it " +
				"onto main replays commits its base already has",
		},
		{
			`l.name === "no-auto-rebase"`,
			"the label is the only way for an author to keep a branch off the " +
				"rebase treadmill",
		},
		{
			`fetch-depth: 0`,
			"a shallow clone has no merge base with a branch that forked more " +
				"than a few commits back, and every rebase fails",
		},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("%s no longer contains %q — %s", autoRebaseFile, want.needle, want.why)
		}
	}
}

func TestAutoRebaseKeepsThePushGate(t *testing.T) {
	src := autoRebaseSource(t)

	if !strings.Contains(src, `secrets.AUTO_REBASE_TOKEN != ''`) {
		t.Errorf("%s no longer derives PUSH_ENABLED from the secret: under the "+
			"default token every rebased head silently loses its checks", autoRebaseFile)
	}
	if !strings.Contains(src, `if [ -z "$PUSH_ENABLED" ]`) {
		t.Errorf("%s no longer refuses to push without the token — the gate is "+
			"declared and then not applied", autoRebaseFile)
	}
	if !strings.Contains(src, "secrets.AUTO_REBASE_TOKEN || secrets.GITHUB_TOKEN") {
		t.Errorf("%s: the checkout no longer prefers AUTO_REBASE_TOKEN, so the "+
			"pushes it is allowed to make go out under the wrong identity", autoRebaseFile)
	}
}

func TestAutoRebaseCommentsOnceEachHead(t *testing.T) {
	src := autoRebaseSource(t)

	if !strings.Contains(src, "${kind} at") {
		t.Fatalf("%s: the dedupe marker no longer distinguishes the notices, so a "+
			"branch that goes from behind to conflicted is told only once", autoRebaseFile)
	}
	if !strings.Contains(src, "c.body?.includes(key)") {
		t.Errorf("%s no longer checks existing comments for the marker: every "+
			"merge into main re-posts the same notice", autoRebaseFile)
	}
	if !strings.Contains(src, "marker(kind, row.head)") {
		t.Errorf("%s: the marker is not keyed on the head the replay actually "+
			"tested, so the notice names a commit nothing was checked against",
			autoRebaseFile)
	}
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "const marker") && strings.Contains(line, "<!--") {
			t.Errorf("%s: the dedupe marker is an HTML comment, which GitHub "+
				"strips from the posted body", autoRebaseFile)
		}
	}
}

func TestAutoRebaseAsksForARebaseWhenItCannotPush(t *testing.T) {
	src := autoRebaseSource(t)

	for _, want := range []struct{ needle, why string }{
		{
			`"would-rebase":`,
			"a branch that replays cleanly but cannot be pushed for want of the " +
				"token is never told to rebase itself",
		},
		{
			`fork:`,
			"a fork PR cannot be pushed by any token here, so its author is the " +
				"only one who can rebase it",
		},
		{
			`} else if (why[row.status]) {`,
			"the notice loop no longer reaches the statuses that need one — only " +
				"conflicts would be reported",
		},
		{
			"A rebase is required",
			"the notice has to say what it wants, in both the clean and the " +
				"conflicted wording",
		},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("%s no longer contains %q — %s", autoRebaseFile, want.needle, want.why)
		}
	}

	if !strings.Contains(src, "...resolve(false)") || !strings.Contains(src, "...resolve(true)") {
		t.Errorf("%s: the two notices no longer differ in their resolve steps — one "+
			"of them tells the author the wrong thing", autoRebaseFile)
	}
}

func TestAutoRebaseSurvivesTheNextMerge(t *testing.T) {
	src := autoRebaseSource(t)

	block, ok := topLevelBlock(src, "concurrency")
	if !ok {
		t.Fatalf("%s has no top-level `concurrency:` block", autoRebaseFile)
	}
	if !strings.Contains(block, "cancel-in-progress: false") {
		t.Errorf("%s no longer waits for the run in flight: on a busy default "+
			"branch it is cancelled in the queue before it runs a step, every time",
			autoRebaseFile)
	}
	if !strings.Contains(block, "group: auto-rebase-prs") {
		t.Errorf("%s dropped its concurrency group: concurrent runs would push "+
			"the same branches against each other", autoRebaseFile)
	}
}

func TestAutoRebaseUsesTheLiveBase(t *testing.T) {
	src := autoRebaseSource(t)

	if strings.Contains(src, "BASE_SHA: ${{ github.sha }}") {
		t.Errorf("%s replays onto the triggering commit: by the time a queued run "+
			"gets a runner that is stale, and every branch it pushes is behind "+
			"again immediately", autoRebaseFile)
	}
	if !strings.Contains(src, `BASE_SHA="$(git rev-parse FETCH_HEAD)"`) {
		t.Errorf("%s no longer derives the base from a fresh fetch of the default "+
			"branch", autoRebaseFile)
	}
	if !strings.Contains(src, "process.env.BASE_SHA || context.sha") {
		t.Errorf("%s: the notices no longer name the base the replay actually "+
			"used, so they cite a commit nothing was checked against", autoRebaseFile)
	}
}

func TestAutoRebaseRunsOnlyOnTheDefaultBranch(t *testing.T) {
	src := autoRebaseSource(t)
	on, ok := onBlock(src)
	if !ok {
		t.Fatalf("%s has no `on:` block", autoRebaseFile)
	}
	if strings.Contains(on, "pull_request") {
		t.Errorf("%s triggers on pull_request: a write-scoped rebase loop must "+
			"not be reachable from a pull request", autoRebaseFile)
	}
	if strings.Contains(on, "push") && !strings.Contains(on, "branches: [main]") {
		t.Errorf("%s is not scoped to main — every branch push would rebase "+
			"every open pull request onto that branch", autoRebaseFile)
	}
}

func topLevelBlock(src, key string) (string, bool) {
	lines := strings.Split(src, "\n")
	start := -1
	for i, l := range lines {
		if l == key+":" || strings.HasPrefix(l, key+": ") {
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
