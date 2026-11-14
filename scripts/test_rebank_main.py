import os
import shutil
import subprocess
import tempfile
import unittest

SCRIPTS = os.path.dirname(os.path.abspath(__file__))
ENV = {
    **{k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "GITHUB_", "FERN_"))},
    "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@fern.invalid",
    "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@fern.invalid",
    "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull,
}


def git(cwd, *args):
    return subprocess.run(["git", *args], cwd=cwd, env=ENV, check=True, capture_output=True, text=True).stdout.strip()


class RebankMainTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.remote = os.path.join(self.tmp, "remote.git")
        self.work = os.path.join(self.tmp, "work")
        self.rows = os.path.join(self.tmp, "corrected")
        git(self.tmp, "init", "-q", "--bare", "-b", "main", self.remote)
        git(self.tmp, "clone", "-q", self.remote, self.work)
        os.makedirs(os.path.join(self.work, ".github"))
        os.makedirs(os.path.join(self.work, "scripts"))
        for name in ("rebank", "rebank-main"):
            shutil.copy(os.path.join(SCRIPTS, name), os.path.join(self.work, "scripts", name))
        self.write(".github/perf-baseline.txt", "x86_64/a.ir\t1000\nx86_64/b.ir\t1000\naarch64/a.ir\t2000\n")
        self.write(".github/alloc-baseline.txt", "x86_64/checker.allocs\t500\n")
        self.measured = self.commit("first")

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def write(self, rel, text):
        with open(os.path.join(self.work, rel), "w") as f:
            f.write(text)

    def commit(self, subject):
        git(self.work, "checkout", "-q", "-B", "main")
        git(self.work, "add", "-A")
        git(self.work, "commit", "-q", "-m", subject)
        git(self.work, "push", "-q", "origin", "main")
        return git(self.work, "rev-parse", "HEAD")

    def corrected(self, host, name, text):
        os.makedirs(os.path.join(self.rows, host), exist_ok=True)
        with open(os.path.join(self.rows, host, name), "w") as f:
            f.write(text)

    def run_script(self, measured=None):
        return subprocess.run(["scripts/rebank-main", measured or self.measured, self.rows],
                              cwd=self.work, env=ENV, capture_output=True, text=True)

    def remote_file(self, rel):
        return git(self.remote, "show", "main:" + rel) + "\n"

    def test_sets_the_rows_of_each_host_and_pushes_one_commit(self):
        self.corrected("x86_64", "perf-baseline.txt", "x86_64/a.ir 1020\nx86_64/b.ir 2000\n")
        self.corrected("aarch64", "perf-baseline.txt", "aarch64/a.ir 1990\n")
        self.corrected("x86_64", "alloc-baseline.txt", "")
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.remote_file(".github/perf-baseline.txt"),
                         "x86_64/a.ir\t1020\nx86_64/b.ir\t1000\naarch64/a.ir\t1990\n")
        self.assertEqual(git(self.remote, "rev-list", "--count", "main"), "2")
        message = git(self.remote, "log", "-1", "--format=%B", "main")
        self.assertIn("perf baselines: the rows of main at " + self.measured[:8], message)
        self.assertIn("held x86_64/b.ir 1000 -> 2000", message)

    def test_nothing_to_set_pushes_nothing(self):
        self.corrected("x86_64", "perf-baseline.txt", "")
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(git(self.remote, "rev-list", "--count", "main"), "1")

    def test_a_baseline_that_changed_after_the_measurement_is_left(self):
        self.write(".github/perf-baseline.txt", "x86_64/a.ir\t1010\nx86_64/b.ir\t1000\naarch64/a.ir\t2000\n")
        self.commit("a person set a row")
        self.corrected("x86_64", "perf-baseline.txt", "x86_64/a.ir 1020\n")
        self.corrected("x86_64", "alloc-baseline.txt", "x86_64/checker.allocs 505\n")
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("x86_64/a.ir\t1010\n", self.remote_file(".github/perf-baseline.txt"))
        self.assertEqual(self.remote_file(".github/alloc-baseline.txt"), "x86_64/checker.allocs\t505\n")

    def test_a_measurement_of_a_rebank_commit_sets_nothing(self):
        self.corrected("x86_64", "perf-baseline.txt", "x86_64/a.ir 1020\n")
        self.assertEqual(self.run_script().returncode, 0)
        rebank = git(self.remote, "rev-parse", "main")
        git(self.work, "fetch", "-q", "origin", "main")
        self.corrected("x86_64", "perf-baseline.txt", "x86_64/a.ir 1030\n")
        r = self.run_script(rebank)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(git(self.remote, "rev-parse", "main"), rebank)
        self.assertIn("is a re-bank", r.stderr)

    def test_a_refused_push_is_a_warning(self):
        hook = os.path.join(self.remote, "hooks", "pre-receive")
        with open(hook, "w") as f:
            f.write("#!/bin/sh\nexit 1\n")
        os.chmod(hook, 0o755)
        self.corrected("x86_64", "perf-baseline.txt", "x86_64/a.ir 1020\n")
        r = self.run_script()
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("the push to main was refused", r.stderr)
        self.assertEqual(git(self.remote, "rev-list", "--count", "main"), "1")


if __name__ == "__main__":
    unittest.main()
