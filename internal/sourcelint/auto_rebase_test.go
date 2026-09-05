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
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", autoRebaseFile))
	if err != nil {
		t.Fatalf("read %s: %v", autoRebaseFile, err)
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

// The conflict comment is posted on every push to main for as long as the
// conflict lasts, so it needs a dedupe key that survives main moving. Keyed on
// the PR's own head: the same conflict is reported once, and a push that fails
// to resolve it is reported again.
func TestAutoRebaseCommentsOnceEachHead(t *testing.T) {
	src := autoRebaseSource(t)

	marker := strings.Index(src, "Head at time of check")
	if marker < 0 {
		t.Fatalf("%s: no dedupe marker in the comment body", autoRebaseFile)
	}
	if !strings.Contains(src, "c.body?.includes(key)") {
		t.Errorf("%s no longer checks existing comments for the marker: every "+
			"merge into main re-posts the same conflict notice", autoRebaseFile)
	}
	if !strings.Contains(src, "marker(row.head)") {
		t.Errorf("%s: the marker is not keyed on the head the replay actually "+
			"tested, so the notice names a commit nothing was checked against",
			autoRebaseFile)
	}
	// The marker has to be plain text. GitHub silently strips anything that
	// reads as an HTML tag from a comment body, taking the rest of the span
	// with it — an HTML-comment marker would be deleted and the dedupe would
	// never match.
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "Head at time of check") && strings.Contains(line, "<!--") {
			t.Errorf("%s: the dedupe marker is an HTML comment, which GitHub "+
				"strips from the posted body", autoRebaseFile)
		}
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
