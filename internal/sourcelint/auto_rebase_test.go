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

// workflowSource reads one workflow from .github/workflows.
func workflowSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// auto-rebase-prs.yml force-pushes other people's branches, so every guard
// deciding WHICH branch gets written is a correctness boundary — and all of
// them live in an inline shell body or a `github-script` block, where nothing
// type-checks them. Each failure mode below is silent at the point it happens
// and only visible later as someone's lost work.
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

// Pushing is gated on AUTO_REBASE_TOKEN because a push made with the default
// GITHUB_TOKEN triggers no workflow run: the rebased head would carry no checks
// at all, in a repo that merges on the checks list. Both halves of that gate —
// reading the secret, and refusing to push without it — have to stay.
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

// A notice is posted on every push to main for as long as the branch needs its
// author, so it needs a dedupe key that survives main moving. Keyed on the PR's
// own head: the same state is reported once, and a push that does not fix it is
// reported again.
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
	// The marker has to be plain text. GitHub silently strips anything that
	// reads as an HTML tag from a comment body, taking the rest of the span
	// with it — an HTML-comment marker would be deleted and the dedupe would
	// never match.
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "const marker") && strings.Contains(line, "<!--") {
			t.Errorf("%s: the dedupe marker is an HTML comment, which GitHub "+
				"strips from the posted body", autoRebaseFile)
		}
	}
}

// Every branch the workflow cannot push is a branch whose author has to rebase
// it by hand, and the only way they learn that is the comment. Without the
// token — the state this repository is in — that is EVERY open PR, so a
// reporting loop that only ever speaks up about conflicts leaves a clean branch
// silently behind forever.
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

	// The clean-branch notice must not tell the author to resolve conflicts
	// there are none of.
	if !strings.Contains(src, "...resolve(false)") || !strings.Contains(src, "...resolve(true)") {
		t.Errorf("%s: the two notices no longer differ in their resolve steps — one "+
			"of them tells the author the wrong thing", autoRebaseFile)
	}
}

// The lane shares a runner pool with ~85 jobs per open PR, so it routinely sits
// queued for longer than the gap between merges here. `cancel-in-progress: true`
// therefore kills it on the next push every time — its first eleven runs were
// all cancelled without one of them ever being allocated a runner, so not a
// single step executed. Nothing else reports that: the lane is green-by-absence,
// and the PRs it should have spoken about simply stay silent.
func TestAutoRebaseSurvivesTheNextMerge(t *testing.T) {
	src := autoRebaseSource(t)

	// The top-level `concurrency:` mapping, not the file: the header explains
	// why the setting is what it is, and matching that prose would pass on the
	// very config it warns about.
	block, ok := topLevelBlock(src, "concurrency")
	if !ok {
		t.Fatalf("%s has no top-level `concurrency:` block", autoRebaseFile)
	}
	if !strings.Contains(block, "cancel-in-progress: false") {
		t.Errorf("%s no longer waits for the run in flight: on a busy default "+
			"branch it is cancelled in the queue before it runs a step, every time",
			autoRebaseFile)
	}
	// GitHub keeps one pending run per group, so waiting collapses a burst of
	// merges into a single follow-up rather than queueing one run per push.
	if !strings.Contains(block, "group: auto-rebase-prs") {
		t.Errorf("%s dropped its concurrency group: concurrent runs would push "+
			"the same branches against each other", autoRebaseFile)
	}
}

// A run that waited in the queue starts against a default branch that has
// already moved. Rebasing onto the commit that TRIGGERED it would leave every
// branch behind again the moment it landed, and the notice would name a commit
// that is no longer the head.
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

// The workflow must not run on pull_request. It force-pushes branches and
// comments on PRs with `contents: write` + `pull-requests: write`; a
// pull_request trigger would hand that to any fork PR, and it would rebase
// every open branch on every push to every branch.
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
	if !strings.Contains(on, "branches: [main]") {
		t.Errorf("%s is not scoped to main — every branch push would rebase "+
			"every open pull request onto that branch", autoRebaseFile)
	}
}

// topLevelBlock returns a top-level mapping's body: everything under `key:` up
// to the next line starting in column zero. Same shape as onBlock next door,
// which is that function specialised to `on:`.
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
