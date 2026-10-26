"""Check failure evidence without a Docker daemon."""

import copy
import importlib.util
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock


source = Path(__file__).resolve().parents[1] / "benchmarks/qualify_workers.py"
spec = importlib.util.spec_from_file_location("worker_qualification", source)
qualification = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qualification)


class FakeDocker:
    def __init__(self, code=137, oom=False, stdout="", stderr="", delayed_oom=False):
        self.commands = []
        self.removed = False
        self.inspect_count = 0
        self.delayed_oom = delayed_oom
        self.stdout = stdout
        self.stderr = stderr
        self.info = {
            "HostConfig": {"Memory": qualification.MEMORY, "MemorySwap": qualification.MEMORY,
                           "NanoCpus": 500000000, "PidsLimit": 16, "ReadonlyRootfs": True,
                           "NetworkMode": "none", "CgroupnsMode": "private", "CapDrop": ["ALL"],
                           "SecurityOpt": ["no-new-privileges"]},
            "Config": {"User": "1000:1000"}, "Mounts": [],
            "State": {"Status": "exited", "Running": False, "Pid": 0,
                      "OOMKilled": oom, "ExitCode": code, "Error": ""},
        }

    def inspect(self, container):
        self.inspect_count += 1
        info = copy.deepcopy(self.info)
        if self.delayed_oom and self.inspect_count >= 4:
            info["State"]["OOMKilled"] = True
        return info

    def call(self, *args, **kwargs):
        self.commands.append(args)
        stdout, stderr, code = "", "", 0
        if args[0] == "create":
            stdout = "owned-container\n"
        elif args[0] == "logs":
            stdout, stderr = self.stdout, self.stderr
        elif args[0] == "rm":
            self.removed = True
        elif args[0] == "inspect":
            code = 1 if self.removed else 0
        elif args[0] == "info":
            stdout = json.dumps({"ServerVersion": "fixture", "CgroupDriver": "systemd",
                                 "CgroupVersion": "2", "KernelVersion": "fixture",
                                 "Architecture": "x86_64", "MemoryLimit": True, "SwapLimit": True,
                                 "CpuCfsQuota": True, "PidsLimit": True})
        elif args[0] == "build":
            stdout = "fixture-image\n"
        return subprocess.CompletedProcess(args, code, stdout, stderr)


class WorkerEvidenceTests(unittest.TestCase):
    def test_failed_oom_retains_state_and_output_after_cleanup(self):
        docker = FakeDocker(stdout="worker output\n", stderr="worker error\n")
        with self.assertRaises(qualification.WorkerFailure) as caught:
            qualification.run_worker(docker, "image", "oom", 2)
        sample = caught.exception.observation
        self.assertFalse(sample["state"]["OOMKilled"])
        self.assertEqual(sample["state"]["ExitCode"], 137)
        self.assertEqual(sample["stdout"], "worker output\n")
        self.assertEqual(sample["stderr"], "worker error\n")
        self.assertEqual(sample["status"], "failed")
        self.assertTrue(sample["removed"])
        self.assertIn(("rm", "-f", "owned-container"), docker.commands)

    def test_later_oom_flag_does_not_waive_original_failure(self):
        docker = FakeDocker(delayed_oom=True)
        with self.assertRaises(qualification.WorkerFailure) as caught:
            qualification.run_worker(docker, "image", "oom", 2)
        sample = caught.exception.observation
        self.assertFalse(sample["state"]["OOMKilled"])
        self.assertTrue(sample["state_after_failure"]["OOMKilled"])
        self.assertEqual(sample["status"], "failed")

    def test_unexpected_exit_still_rejects_oom(self):
        with self.assertRaises(qualification.WorkerFailure) as caught:
            qualification.run_worker(FakeDocker(code=11, oom=True), "image", "oom", 0)
        self.assertEqual(caught.exception.observation["state"]["ExitCode"], 11)

    def test_missing_required_oom_state_still_rejects(self):
        docker = FakeDocker()
        del docker.info["State"]["OOMKilled"]
        with self.assertRaises(qualification.WorkerFailure):
            qualification.run_worker(docker, "image", "oom", 0)

    def test_kernel_oom_signal_still_passes(self):
        sample = qualification.run_worker(FakeDocker(oom=True), "image", "oom", 0)
        self.assertEqual(sample["status"], "passed")
        self.assertTrue(sample["removed"])

    def test_unbalanced_leaks_still_reject(self):
        controls = "cgroup memory.max\n67108864\ncgroup memory.swap.max\n0\ncgroup cpu.max\n50000 100000\ncgroup pids.max\n16\nok mode=valid\n"
        docker = FakeDocker(code=0, stdout=controls,
                            stderr="leakcheck: allocs=2 frees=1 live_bytes=0\n")
        with self.assertRaises(qualification.WorkerFailure) as caught:
            qualification.run_worker(docker, "image", "valid", 0)
        self.assertIn("release every allocation", str(caught.exception))

    def test_main_preserves_failed_sample_and_completed_prefix(self):
        docker = FakeDocker()
        sample = {"mode": "oom", "repeat": 0, "removed": True,
                  "state": {"ExitCode": 137, "OOMKilled": False}, "status": "failed"}

        def run(docker, tag, mode, repeat):
            if mode == "oom":
                raise qualification.WorkerFailure("expected OOM failure", sample)
            return {"mode": mode, "repeat": repeat, "removed": True, "status": "passed"}

        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "binary"
            binary.write_bytes(b"fixture")
            output = Path(directory) / "record.json"
            with mock.patch.object(qualification, "Docker", return_value=docker), \
                 mock.patch.object(qualification, "run_worker", side_effect=run), \
                 mock.patch.object(qualification.subprocess, "check_output", return_value="fixture\n"), \
                 mock.patch.object(qualification.shutil, "which", return_value=str(binary)), \
                 mock.patch.object(qualification.sys, "argv", ["qualify", str(binary), str(binary), str(output)]):
                with self.assertRaises(qualification.WorkerFailure):
                    qualification.main()
            record = json.loads(output.read_text())
        self.assertEqual(record["status"], "failed")
        self.assertEqual([s["mode"] for s in record["samples"]], ["valid", "oom"])
        self.assertEqual(record["samples"][1], sample)
        self.assertTrue(record["sources_unchanged"])
        self.assertTrue(any(command[:2] == ("image", "rm") for command in docker.commands))
        self.assertNotIn("env", record)


if __name__ == "__main__":
    unittest.main()
