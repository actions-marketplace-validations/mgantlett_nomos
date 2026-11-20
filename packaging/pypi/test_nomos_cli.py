"""Unit tests for the zero-dependency nomos-cli PyPI / uvx binary bootstrapper."""

from __future__ import annotations

import hashlib
import http.server
import io
import os
import stat
import sys
import tarfile
import tempfile
import threading
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / "src"))

import nomos_cli  # noqa: E402


def _build_tar_gz_binary(bin_name: str, payload: bytes) -> bytes:
    """Builds an in-memory tar.gz containing an executable test script."""
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tf:
        info = tarfile.TarInfo(name=bin_name)
        info.size = len(payload)
        info.mode = 0o755
        tf.addfile(info, io.BytesIO(payload))
    return buf.getvalue()


class NomosCliLauncherTest(unittest.TestCase):
    """Verifies platform mapping, SHA-256 verification, and tampered-archive fail-closed guards."""

    def test_resolve_target_platform_matrix(self) -> None:
        """Maps Linux, Darwin, and Windows CPU targets accurately."""
        lin = nomos_cli.resolve_target_platform("v4.28.0", "Linux", "x86_64")
        self.assertEqual(lin.archive, "nomos_Linux_x86_64.tar.gz")
        self.assertEqual(lin.version, "4.28.0")

        mac = nomos_cli.resolve_target_platform("4.28.0", "Darwin", "arm64")
        self.assertEqual(mac.archive, "nomos_Darwin_arm64.tar.gz")

        win = nomos_cli.resolve_target_platform("4.28.0", "Windows", "AMD64")
        self.assertEqual(win.archive, "nomos_Windows_x86_64.zip")
        self.assertEqual(win.bin_name, "nomos.exe")

        with self.assertRaises(RuntimeError):
            nomos_cli.resolve_target_platform("4.28.0", "FreeBSD", "mips")

    def test_sha256_verified_download_and_tamper_rejection(self) -> None:
        """Downloads from NOMOS_RELEASE_BASE_URL, verifies SHA-256, and rejects corrupted archives."""
        target = nomos_cli.resolve_target_platform("4.28.0")
        if target.goos == "Windows":
            return
        fake_bin = b"#!/bin/sh\necho nomos-verified-ok\n"
        archive_bytes = _build_tar_gz_binary(target.bin_name, fake_bin)
        good_sha = hashlib.sha256(archive_bytes).hexdigest()
        state = {"checksums": f"{good_sha}  {target.archive}\n"}

        class Handler(http.server.BaseHTTPRequestHandler):
            """Serves mock release checksums and archive bytes."""

            def do_GET(self) -> None:  # noqa: N802
                if self.path.endswith("/checksums.txt"):
                    body = state["checksums"].encode("utf-8")
                else:
                    body = archive_bytes
                self.send_response(200)
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, fmt: str, *args: object) -> None:
                return

        srv = http.server.HTTPServer(("127.0.0.1", 0), Handler)
        port = srv.server_address[1]
        thread = threading.Thread(target=srv.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as tmp:
                old_env = os.environ.copy()
                try:
                    os.environ["NOMOS_RELEASE_BASE_URL"] = f"http://127.0.0.1:{port}"
                    os.environ["NOMOS_CACHE_DIR"] = str(Path(tmp) / "cache-good")
                    os.environ["NOMOS_SKIP_SYSTEM_PATH"] = "1"
                    os.environ["NOMOS_VERSION"] = "4.28.0"
                    resolved = nomos_cli.ensure_cached_binary(target)
                    self.assertTrue(Path(resolved).is_file())
                    self.assertTrue(bool(Path(resolved).stat().st_mode & stat.S_IXUSR))

                    # Tampered SHA-256 must fail closed before writing binary.
                    state["checksums"] = f"{'0' * 64}  {target.archive}\n"
                    os.environ["NOMOS_CACHE_DIR"] = str(Path(tmp) / "cache-bad")
                    with self.assertRaises(ValueError):
                        nomos_cli.ensure_cached_binary(target)
                finally:
                    os.environ.clear()
                    os.environ.update(old_env)
        finally:
            srv.shutdown()
            srv.server_close()


def test_nomos_cli_suite() -> None:
    """Executes all NomosCliLauncherTest checks for zero-arg runner discovery."""
    case = NomosCliLauncherTest()
    case.test_resolve_target_platform_matrix()
    case.test_sha256_verified_download_and_tamper_rejection()


if __name__ == "__main__":
    unittest.main()
