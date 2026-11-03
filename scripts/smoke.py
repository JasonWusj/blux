#!/usr/bin/env python3
"""Check local services and the real CLI task contract. --real also runs a Codex task."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


def require(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def check(message: str) -> None:
    print(f"PASS {message}", flush=True)


class Smoke:
    def __init__(self, binary: Path, directory: Path, timeout: float) -> None:
        self.binary = binary
        self.directory = directory
        self.data = directory / "data"
        self.timeout = timeout
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            self.port = listener.getsockname()[1]
        self.base = f"http://127.0.0.1:{self.port}/api/v1"
        self.children: list[subprocess.Popen] = []
        self.logs: list[Path] = []
        self.api: subprocess.Popen | None = None
        self.worker: subprocess.Popen | None = None

    def start(self, name: str, *arguments: str) -> subprocess.Popen:
        path = self.directory / f"{name}-{len(self.logs)}.log"
        self.logs.append(path)
        with path.open("wb") as log:
            process = subprocess.Popen(
                [str(self.binary), *arguments],
                stdin=subprocess.DEVNULL,
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        self.children.append(process)
        return process

    def start_api(self) -> None:
        self.api = self.start(
            "api", "serve", "--addr", f"127.0.0.1:{self.port}",
            "--data-dir", str(self.data),
        )

    def start_worker(self) -> None:
        self.worker = self.start(
            "worker", "worker", "--data-dir", str(self.data),
        )

    @staticmethod
    def stop(process: subprocess.Popen | None) -> None:
        if process is None or process.poll() is not None:
            return
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)

    def close(self) -> None:
        for process in reversed(self.children):
            self.stop(process)

    def request(
        self, path: str, payload: dict | None = None,
        key: str | None = None, expected: int = 200, replayed: bool = False,
    ) -> dict:
        headers = {"Accept": "application/json"}
        body = None
        if payload is not None:
            body = json.dumps(payload).encode()
            headers["Content-Type"] = "application/json"
        if key is not None:
            headers["Idempotency-Key"] = key
        request = urllib.request.Request(self.base + path, data=body, headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=5) as response:
                status, raw = response.status, response.read()
                response_headers = response.headers
        except urllib.error.HTTPError as error:
            status, raw = error.code, error.read()
            response_headers = error.headers
        result = json.loads(raw)
        require(status == expected, f"{request.get_method()} {path}: expected {expected}, got {status}: {result}")
        if replayed:
            require(response_headers.get("Idempotency-Replayed", "").lower() == "true", "idempotent retry is missing Idempotency-Replayed: true")
        if status >= 400:
            require(isinstance(result.get("error", {}).get("code"), str), "error.code is missing")
            require(isinstance(result["error"].get("message"), str), "error.message is missing")
        return result

    def wait(self, description: str, predicate):
        deadline = time.monotonic() + self.timeout
        last_error = None
        while time.monotonic() < deadline:
            if self.api is not None and self.api.poll() is not None:
                raise AssertionError(f"API exited while waiting for {description}")
            if self.worker is not None and self.worker.poll() is not None:
                raise AssertionError(f"worker exited while waiting for {description}")
            try:
                result = predicate()
                if result:
                    return result
            except (urllib.error.URLError, ConnectionError, TimeoutError, OSError) as error:
                last_error = error
            time.sleep(0.05)
        raise AssertionError(f"timed out waiting for {description}; last connection error: {last_error}")

    def health(self) -> dict:
        result = self.request("/health")
        require(result.get("status") == "ok", "health.status must be ok")
        require(result.get("phase",0) >= 2, "health.phase must be at least 2")
        require(result.get("adapter") == "codex", "default adapter must be codex")
        require(Path(result["data_dir"]).resolve() == self.data.resolve(), "health data_dir does not match")
        return result

    def task(self, task_id: str) -> dict:
        return self.request(f"/tasks/{task_id}")

    def completed(self, task_id: str) -> dict | None:
        detail = self.task(task_id)
        status = detail["task"]["status"]
        require(status != "failed", f"task failed: {detail}")
        return detail if status == "completed" else None

    def events(self, task_id: str, *, after: int = 0, count: int | None = None,
               through: int | None = None, header_cursor: bool = False) -> list[dict]:
        headers = {"Accept": "text/event-stream"}
        path = f"/tasks/{task_id}/events"
        if header_cursor:
            headers["Last-Event-ID"] = str(after)
        else:
            path += f"?after={after}"
        request = urllib.request.Request(self.base + path, headers=headers)
        records = []
        current: dict[str, list[str]] = {}
        deadline = time.monotonic() + self.timeout
        with urllib.request.urlopen(request, timeout=self.timeout) as response:
            require(response.headers.get_content_type() == "text/event-stream", "SSE content type is incorrect")
            while time.monotonic() < deadline:
                raw = response.readline()
                if not raw:
                    break
                line = raw.decode("utf-8").rstrip("\r\n")
                if line.startswith(":"):
                    continue
                if line:
                    field, separator, value = line.partition(":")
                    if separator:
                        current.setdefault(field, []).append(value.removeprefix(" "))
                    continue
                if "data" not in current:
                    current = {}
                    continue
                require(current.get("event") == ["blux.event"], f"unexpected SSE event: {current}")
                event = json.loads("\n".join(current["data"]))
                seq = event["seq"]
                require(current.get("id") == [str(seq)], "SSE id differs from event.seq")
                require(event["task_id"] == task_id, "SSE delivered another task's event")
                expected_seq = records[-1]["seq"] + 1 if records else after + 1
                require(seq == expected_seq, f"SSE duplicate or gap: expected {expected_seq}, got {seq}")
                records.append(event)
                current = {}
                if (count is not None and len(records) >= count) or (through is not None and seq >= through):
                    break
        if count is not None:
            require(len(records) == count, f"expected {count} SSE events, got {len(records)}")
        if through is not None:
            require(bool(records) and records[-1]["seq"] == through, "SSE ended before the persisted event cursor")
        return records

    @staticmethod
    def projection(detail: dict) -> dict:
        task = detail["task"]
        return {
            "task": {name: task[name] for name in ("id", "status", "session_id", "current_turn_id", "last_event_seq")},
            "session": detail["session"],
            "turns": detail["turns"],
            "messages": detail["messages"],
        }

    def run(self, real: bool = False) -> None:
        self.start_api(); self.start_worker(); self.wait("health", self.health)
        agents=self.request("/agents")["agents"]
        require({a["id"] for a in agents}=={"codex","claude","deepseek","gemini"},"unexpected execution adapters")
        self.request("/tasks",{"prompt":"unsupported adapter","adapter":"demo"},"reject-retired",expected=400)
        require(not self.request("/tasks")["tasks"],"rejected adapter created a task")
        check("services online; only real CLI adapters; unsupported execution rejected")
        if not real: return
        repo=self.directory/"project";repo.mkdir()
        def git(*args):
            return subprocess.check_output(["git","-c","user.name=Blux Test","-c","user.email=test@local","-C",str(repo),*args],stderr=subprocess.DEVNULL).decode().strip()
        git("init");git("commit","--allow-empty","-m","baseline")
        baseline=git("rev-parse","HEAD")
        payload={"adapter":"codex","repository_path":str(repo),"prompt":"Save a one-line Result.md in the artifacts directory saying Blux is ready. Only use tools needed to write this file. Do not modify the repository or commit. Reply briefly."}
        detail=self.request("/tasks",payload,"real-task",expected=201);tid=detail["task"]["id"]
        require(self.request("/tasks",payload,"real-task",replayed=True)["task"]["id"]==tid,"duplicate task")
        detail=self.wait("Codex completed",lambda:self.completed(tid))
        require(detail["session"]["runtime"]["native_thread_id"],"missing native session")
        snapshots=self.request("/tasks/"+tid+"/snapshots")["snapshots"]
        require(snapshots and snapshots[-1]["artifacts"],"no real deliverable captured")
        before=self.projection(detail);self.stop(self.api);self.start_api();self.wait("API restart",self.health)
        require(self.projection(self.task(tid))==before,"restart lost history")
        require(git("rev-parse","HEAD")==baseline,"source changed before acceptance")
        check("real Codex task, idempotency, output capture, API restart and source isolation")

    def print_logs(self) -> None:
        for path in self.logs:
            print(f"\n--- {path.name} ---", file=sys.stderr)
            print("\n".join(path.read_text(errors="replace").splitlines()[-40:]), file=sys.stderr)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default=Path(__file__).resolve().parents[1] / "bin" / ("blux.exe" if os.name=="nt" else "blux"))
    parser.add_argument("--timeout", type=float, default=180, help="maximum seconds for each wait (default: 180)")
    parser.add_argument("--real", action="store_true", help="run a real Codex model task in a temporary repository")
    args = parser.parse_args()
    binary = args.binary.expanduser().resolve()
    if not binary.is_file() or not os.access(binary, os.X_OK):
        parser.error(f"executable not found: {binary}; build it with go build -o bin/blux ./cmd/blux")
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    with tempfile.TemporaryDirectory(prefix="blux-smoke-") as directory:
        smoke = Smoke(binary, Path(directory), args.timeout)
        try:
            smoke.run(args.real)
        except Exception as error:
            print(f"FAIL {error}", file=sys.stderr)
            smoke.print_logs()
            return 1
        finally:
            smoke.close()
    print("Blux smoke passed.", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
