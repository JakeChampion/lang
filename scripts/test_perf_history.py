"""Cover scripts/perf-history show — a noteless commit must not end the scan.

The newest main commits carry no perf note until the nightly records one,
so `show` has to walk past them to reach the commits that do; a regression
that stops at the first noteless commit would silently show one row, or
none, where the notes hold a whole trend.
"""
import os
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "perf-history")
REF = "refs/notes/perf"


def git(repo, *args):
    subprocess.run(["git", "-C", repo, *args], check=True,
                   capture_output=True, text=True)


class PerfHistoryShowTest(unittest.TestCase):
    def setUp(self):
        self.repo = tempfile.mkdtemp()
        self.addCleanup(subprocess.run, ["rm", "-rf", self.repo])
        git(self.repo, "init", "-q")
        git(self.repo, "config", "user.email", "t@example.invalid")
        git(self.repo, "config", "user.name", "t")
        for i in range(5):
            git(self.repo, "commit", "-q", "--allow-empty", "-m", "c%d" % i)
        # Notes in the middle: the two newest commits carry none (the scan
        # must not stop at the start) and so does the oldest (show must still
        # exit 0 after walking past a noteless final commit).
        git(self.repo, "notes", "--ref=" + REF, "add",
            "-m", "# lane: net-nightly\nnet/fern/c64.rps\t100", "HEAD~2")
        git(self.repo, "notes", "--ref=" + REF, "add",
            "-m", "# lane: net-nightly\nnet/fern/c64.rps\t200", "HEAD~3")

    def show(self, metric):
        # A remote that cannot resolve makes fetch_notes a no-op.
        env = dict(os.environ, FERN_PERF_REMOTE="perf-history-test-no-remote")
        return subprocess.run(["bash", SCRIPT, "show", metric, "50", "HEAD"],
                              cwd=self.repo, env=env,
                              capture_output=True, text=True)

    def test_noteless_newest_commits_do_not_end_the_scan(self):
        r = self.show("net/fern/c64.rps")
        self.assertEqual(r.returncode, 0, r.stderr)
        values = [line.split()[-1] for line in r.stdout.splitlines() if line.strip()]
        # Both note-bearing commits are reached, past the two noteless ones.
        self.assertEqual(values, ["100", "200"], r.stdout)


if __name__ == "__main__":
    unittest.main()
