"""Test native image workers under local Docker cgroup controls."""

import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tempfile
import time
import uuid
from pathlib import Path


MEMORY = 64 * 1024 * 1024
DEADLINE = 1.0
DOCKERFILE = 'FROM scratch\nCOPY worker /worker\nUSER 1000:1000\nENTRYPOINT ["/worker"]\n'


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


class WorkerFailure(RuntimeError):
    def __init__(self, message, observation):
        self.observation = observation
        super().__init__(message)


class Docker:
    def __init__(self):
        self.env = os.environ.copy()
        for key in ["DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"]:
            self.env.pop(key, None)

    def call(self, *args, check=True, timeout=20):
        return subprocess.run(["docker", "--host=unix:///var/run/docker.sock", *args],
                              env=self.env, capture_output=True, text=True, check=check, timeout=timeout)

    def inspect(self, container):
        return json.loads(self.call("inspect", container).stdout)[0]


def state_fields(info):
    state = {key: info["State"][key] for key in ["Status", "Running", "Pid", "OOMKilled", "ExitCode", "Error"]}
    state.update({key: info["State"].get(key) for key in ["StartedAt", "FinishedAt"]})
    return state


def check_controls(info):
    config = info["HostConfig"]
    expected = {"Memory": MEMORY, "MemorySwap": MEMORY, "NanoCpus": 500000000, "PidsLimit": 16,
                "ReadonlyRootfs": True, "NetworkMode": "none", "CgroupnsMode": "private"}
    if any(config[key] != value for key, value in expected.items()):
        raise RuntimeError("worker controls differ from the requested controls")
    if config["CapDrop"] != ["ALL"] or config["SecurityOpt"] != ["no-new-privileges"]:
        raise RuntimeError("worker security controls differ")
    if info["Config"]["User"] != "1000:1000" or info["Mounts"]:
        raise RuntimeError("worker user or mounts differ")
    return {**expected, "CapDrop": config["CapDrop"], "SecurityOpt": config["SecurityOpt"], "User": "1000:1000"}


def check_self_controls(stdout):
    for name, value in [("memory.max", str(MEMORY)), ("memory.swap.max", "0"),
                        ("cpu.max", "50000 100000"), ("pids.max", "16")]:
        if "cgroup " + name + "\n" + value + "\n" not in stdout:
            raise RuntimeError("worker cannot verify its own " + name)


def run_worker(docker, tag, mode, repeat):
    container = docker.call("create", "--memory=64m", "--memory-swap=64m", "--cpus=0.5",
                            "--pids-limit=16", "--read-only", "--cap-drop=ALL",
                            "--security-opt=no-new-privileges", "--network=none", "--cgroupns=private",
                            tag, mode).stdout.strip()
    removed = False
    observation = {"repeat": repeat, "mode": mode, "removed": False}
    try:
        controls = check_controls(docker.inspect(container))
        observation["controls"] = controls
        start = time.monotonic()
        docker.call("start", container)
        deadline_start = None
        timed_out = False
        while True:
            info = docker.inspect(container)
            if not info["State"]["Running"]:
                break
            stdout = docker.call("logs", container).stdout
            if mode in ["busy", "pressure-busy"] and "ready mode=" + mode + "\n" in stdout:
                if deadline_start is None:
                    deadline_start = time.monotonic()
                if time.monotonic() - deadline_start >= DEADLINE:
                    docker.call("kill", "--signal=KILL", container)
                    timed_out = True
                    break
            if time.monotonic() - start > 15:
                raise RuntimeError("worker did not reach the expected completion or deadline")
            time.sleep(0.04)
        docker.call("wait", container)
        stopped = time.monotonic()
        info = docker.inspect(container)
        state = state_fields(info)
        logs = docker.call("logs", container)
        observation.update({"state": state, "collector_killed": timed_out,
                            "deadline_seconds_after_ready_observation": DEADLINE if deadline_start else None,
                            "observed_seconds_ready_to_stopped": stopped - deadline_start if deadline_start else None,
                            "observed_seconds_start_to_stopped": stopped - start,
                            "stdout": logs.stdout, "stderr": logs.stderr})
        if state["Running"] or state["Pid"] != 0 or state["Error"]:
            raise RuntimeError("worker did not stop cleanly")
        if mode == "oom":
            if state["ExitCode"] != 137 or not state["OOMKilled"] or timed_out:
                raise RuntimeError("retained images did not produce a kernel OOM kill")
        elif mode in ["busy", "pressure-busy"]:
            if state["ExitCode"] != 137 or state["OOMKilled"] or not timed_out:
                raise RuntimeError("deadline did not stop the worker")
            check_self_controls(logs.stdout)
        else:
            if state["ExitCode"] != 0 or state["OOMKilled"] or "ok mode=" + mode + "\n" not in logs.stdout:
                raise RuntimeError("valid image or cancellation job failed: " + logs.stdout + logs.stderr)
            check_self_controls(logs.stdout)
            leak = re.fullmatch(r"leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0\n?", logs.stderr)
            if not leak or leak[1] != leak[2]:
                raise RuntimeError("completed worker did not release every allocation")
        if mode in ["pressure-busy", "pressure-cancel"]:
            peaks = [int(value) for value in re.findall(r"cgroup memory.peak\n(\d+)", logs.stdout)]
            if not peaks or min(peaks) < 48 * 1024 * 1024:
                raise RuntimeError("retained images did not produce the expected cgroup charge")
        docker.call("rm", container)
        removed = True
        if docker.call("inspect", container, check=False).returncode == 0:
            raise RuntimeError("container remains after removal")
        observation.update({"status": "passed", "removed": True})
        return observation
    except Exception as error:
        observation.update({"status": "failed", "failure": str(error), "failure_type": type(error).__name__})
        try:
            observation["state_after_failure"] = state_fields(docker.inspect(container))
        except Exception as diagnostic_error:
            observation["diagnostic_error_type"] = type(diagnostic_error).__name__
        message = str(error) + " " + json.dumps({key: observation.get(key) for key in ["repeat", "mode", "state", "collector_killed"]})
        raise WorkerFailure(message, observation) from error
    finally:
        if not removed:
            try:
                cleanup = docker.call("rm", "-f", container, check=False)
                observation["removed"] = cleanup.returncode == 0
                if cleanup.returncode != 0:
                    observation["cleanup_error"] = cleanup.stderr
            except Exception as cleanup_error:
                observation["cleanup_error_type"] = type(cleanup_error).__name__


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: qualify_workers.py BINARY COMPILER OUTPUT.json")
    binary, compiler, output = (Path(arg).resolve() for arg in sys.argv[1:])
    root = Path(__file__).resolve().parents[1]
    paths = {"binary": binary, "compiler": compiler, "collector": Path(__file__)}
    for directory in [root / "packages", root.parent / "internal/stdlib"]:
        for path in sorted(directory.rglob("*")):
            if path.is_file():
                paths[str(path.relative_to(root.parent))] = path
    paths["fern-image/benchmarks/worker_probe.fern"] = root / "benchmarks/worker_probe.fern"
    hashes = {name: digest(path) for name, path in paths.items()}
    docker = Docker()
    info = json.loads(docker.call("info", "--format", "{{json .}}").stdout)
    daemon = {key: info[key] for key in ["ServerVersion", "CgroupDriver", "CgroupVersion", "KernelVersion", "Architecture",
                                       "MemoryLimit", "SwapLimit", "CpuCfsQuota", "PidsLimit"]}
    if daemon["CgroupVersion"] != "2" or not all(daemon[k] for k in ["MemoryLimit", "SwapLimit", "CpuCfsQuota", "PidsLimit"]):
        raise SystemExit("local Docker cgroup v2 controls are unavailable")
    tag = "fern-image-worker-qualification:" + uuid.uuid4().hex
    built = False
    samples = []
    record = {
        "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
        "compiler_version": subprocess.check_output([str(compiler), "-version"], text=True).strip(),
        "host": platform.platform(), "daemon": daemon, "image_id": None, "dockerfile": DOCKERFILE,
        "docker_cli_sha256": digest(Path(shutil.which("docker"))), "sha256": hashes,
        "flags": "-target x86-64-linux with FERN_LEAKCHECK=1, no quarantine",
        "concurrency": 1, "samples": samples, "status": "failed",
        "limitations": "This record tests a disposable native worker fixture and a local Docker controller. It does not implement a production worker pool. A 64 MiB cgroup memory.max is a kernel control, not an exact RSS or allocator bound. Kernel memory.peak observations differ from RSS. CPU quota limits bandwidth, not elapsed time. The external controller polls after a ready observation and kills a timed worker. It does not promise an exact deadline. Forced kills do not run Fern destructors or a leak census. Later valid workers prove fresh-process recovery, not recovery inside the killed process. Collector, Docker daemon, simultaneous workers, and aggregate service memory remain outside each worker limit. The record does not qualify malicious inputs, production safety, codec compatibility, or performance parity. An incomplete record is not a passing qualification. Later state observations are diagnostic only.",
    }
    try:
        with tempfile.TemporaryDirectory() as context:
            shutil.copy(binary, Path(context) / "worker")
            (Path(context) / "Dockerfile").write_text(DOCKERFILE)
            image = docker.call("build", "--network=none", "-q", "-t", tag, context, timeout=60).stdout.strip()
            record["image_id"] = image
            built = True
        for repeat in range(3):
            for mode in ["valid", "oom", "valid", "busy", "valid", "pressure-busy", "valid", "pressure-cancel", "valid"]:
                samples.append(run_worker(docker, tag, mode, repeat))
                print("passed repeat=" + str(repeat) + " mode=" + mode, flush=True)
    except BaseException as error:
        if isinstance(error, WorkerFailure):
            samples.append(error.observation)
        record["failure"] = {"type": type(error).__name__, "message": str(error)}
        raise
    finally:
        try:
            if built:
                docker.call("image", "rm", tag)
            record["sources_unchanged"] = hashes == {name: digest(path) for name, path in paths.items()}
            if not record["sources_unchanged"]:
                raise RuntimeError("worker sources or tools changed")
            if "failure" not in record:
                record["status"] = "complete"
        except BaseException as finalization_error:
            record["status"] = "failed"
            record["finalization_error_type"] = type(finalization_error).__name__
            if "failure" not in record:
                record["failure"] = {"type": type(finalization_error).__name__, "message": str(finalization_error)}
                raise
        finally:
            output.write_text(json.dumps(record, indent=2) + "\n")


if __name__ == "__main__":
    main()
