"""Zero-dependency SHA-256 verified Python launcher for the static Go nomos binary."""

from __future__ import annotations

import hashlib
import io
import json
import os
import platform
import stat
import subprocess
import sys
import tarfile
import urllib.request
import zipfile
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import List, Optional

FALLBACK_VERSION = "4.28.0"
USER_AGENT = "mgantlett-nomos-pypi-launcher"


@dataclass(frozen=True)
class TargetPlatform:
    """Resolved OS, CPU architecture, and release archive metadata."""

    version: str
    goos: str
    goarch: str
    ext: str
    bin_name: str
    archive: str


def resolve_effective_version(env_version: Optional[str] = None) -> str:
    """Resolves NOMOS_VERSION or queries GitHub's latest release tag with offline fallback."""
    explicit = (env_version if env_version is not None else os.environ.get("NOMOS_VERSION", "")).strip()
    if explicit:
        return explicit.lstrip("v")
    req = urllib.request.Request(
        "https://api.github.com/repos/mgantlett/nomos/releases/latest",
        headers={"Accept": "application/vnd.github+json", "User-Agent": USER_AGENT},
    )
    try:
        with urllib.request.urlopen(req, timeout=2.5) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
            tag = str(payload.get("tag_name", "")).strip().lstrip("v")
            parts = tag.split(".")
            if len(parts) >= 3 and parts[0].isdigit() and parts[1].isdigit():
                return tag
    except Exception:
        return FALLBACK_VERSION
    return FALLBACK_VERSION


def resolve_target_platform(
    raw_version: str = FALLBACK_VERSION,
    sys_name: Optional[str] = None,
    machine_name: Optional[str] = None,
) -> TargetPlatform:
    """Maps Python OS and CPU identifiers onto GoReleaser archive coordinates."""
    version = (raw_version or FALLBACK_VERSION).strip().lstrip("v")
    raw_os = (sys_name or platform.system()).lower()
    raw_arch = (machine_name or platform.machine()).lower()
    os_map = {"linux": "Linux", "darwin": "Darwin", "windows": "Windows"}
    arch_map = {
        "x86_64": "x86_64",
        "amd64": "x86_64",
        "aarch64": "arm64",
        "arm64": "arm64",
    }
    goos = os_map.get(raw_os)
    goarch = arch_map.get(raw_arch)
    if not goos or not goarch:
        raise RuntimeError(f"Unsupported OS/arch combination: {raw_os}/{raw_arch}")
    ext = "zip" if goos == "Windows" else "tar.gz"
    bin_name = "nomos.exe" if goos == "Windows" else "nomos"
    archive = f"nomos_{goos}_{goarch}.{ext}"
    return TargetPlatform(version=version, goos=goos, goarch=goarch, ext=ext, bin_name=bin_name, archive=archive)


def parse_expected_sha256(checksums_text: str, archive_name: str) -> str:
    """Extracts the expected lowercase SHA-256 hex digest for archive_name from checksums.txt."""
    for line in checksums_text.splitlines():
        parts = line.strip().split()
        if len(parts) >= 2 and parts[1].lstrip("*") == archive_name:
            return parts[0].lower()
    raise ValueError(f"Archive {archive_name} not found in release checksums.txt")


def _is_usable_system_candidate(
    cand: Path,
    script_dir: Optional[Path],
    exe_real: Optional[Path],
) -> bool:
    """Returns True when cand is an executable native binary distinct from Python wrapper shims."""
    if not cand.is_file() or not os.access(cand, os.X_OK):
        return False
    resolved = cand.resolve()
    if exe_real and resolved == exe_real:
        return False
    if script_dir and cand.parent.resolve() == script_dir and cand.read_bytes()[:2] == b"#!":
        return False
    return True


def find_existing_system_binary(bin_name: str, self_exe: Optional[Path] = None) -> Optional[str]:
    """Finds an already-installed native nomos binary on PATH while skipping Python wrapper shims."""
    if os.environ.get("NOMOS_SKIP_SYSTEM_PATH") == "1":
        return None
    script_dir = Path(sys.argv[0]).resolve().parent if sys.argv else None
    exe_real = self_exe.resolve() if self_exe else None
    entries = [e for e in os.environ.get("PATH", "").split(os.pathsep) if e]
    for entry in entries:
        cand = Path(entry) / bin_name
        try:
            if _is_usable_system_candidate(cand, script_dir, exe_real):
                return str(cand)
        except OSError:
            continue
    return None


def _fetch_bytes(url: str) -> bytes:
    """Downloads raw bytes from a release URL using stdlib urllib."""
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return resp.read()


def _read_tar_member_bytes(archive_bytes: bytes, target: TargetPlatform) -> bytes:
    """Reads the target executable payload from a verified tar.gz archive."""
    with tarfile.open(fileobj=io.BytesIO(archive_bytes), mode="r:gz") as tf:
        for member in tf.getmembers():
            if member.isfile() and Path(member.name).name == target.bin_name:
                extracted = tf.extractfile(member)
                if extracted is not None:
                    return extracted.read()
    raise RuntimeError(f"Binary {target.bin_name} missing inside {target.archive}")


def _read_zip_member_bytes(archive_bytes: bytes, target: TargetPlatform) -> bytes:
    """Reads the target executable payload from a verified zip archive."""
    with zipfile.ZipFile(io.BytesIO(archive_bytes)) as zf:
        for name in zf.namelist():
            if Path(name).name == target.bin_name:
                return zf.read(name)
    raise RuntimeError(f"Binary {target.bin_name} missing inside {target.archive}")


def _extract_verified_binary(archive_bytes: bytes, target: TargetPlatform, dest_bin: Path) -> None:
    """Extracts exclusively the target nomos binary from a verified tar.gz or zip payload."""
    payload = (
        _read_tar_member_bytes(archive_bytes, target)
        if target.ext == "tar.gz"
        else _read_zip_member_bytes(archive_bytes, target)
    )
    dest_bin.write_bytes(payload)
    if target.goos != "Windows":
        dest_bin.chmod(dest_bin.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


def ensure_cached_binary(target: TargetPlatform) -> str:
    """Returns a cached SHA-256 verified nomos binary path, downloading on cache miss."""
    release_base = os.environ.get(
        "NOMOS_RELEASE_BASE_URL",
        f"https://github.com/mgantlett/nomos/releases/download/v{target.version}",
    ).rstrip("/")
    default_cache = Path.home() / ".cache" / "nomos-launcher"
    cache_root = Path(os.environ.get("NOMOS_CACHE_DIR", str(default_cache)))
    ver_dir = cache_root / f"v{target.version}-{target.goos}-{target.goarch}"
    bin_path = ver_dir / target.bin_name
    if bin_path.is_file():
        return str(bin_path)

    ver_dir.mkdir(parents=True, exist_ok=True)
    checksums_text = _fetch_bytes(f"{release_base}/checksums.txt").decode("utf-8")
    archive_bytes = _fetch_bytes(f"{release_base}/{target.archive}")
    expected_hex = parse_expected_sha256(checksums_text, target.archive)
    actual_hex = hashlib.sha256(archive_bytes).hexdigest().lower()
    if actual_hex != expected_hex:
        raise ValueError(f"SHA-256 mismatch for {target.archive}: expected {expected_hex}, got {actual_hex}")

    tmp_bin = ver_dir / f".{target.bin_name}.tmp.{os.getpid()}"
    _extract_verified_binary(archive_bytes, target, tmp_bin)
    tmp_bin.replace(bin_path)
    return str(bin_path)


def run_cli(argv: Optional[List[str]] = None) -> int:
    """Executes the resolved nomos binary with CLI arguments or stdio mcp fallback."""
    args = list(sys.argv[1:] if argv is None else argv)
    if args[:1] == ["--launcher-info"]:
        target = resolve_target_platform(os.environ.get("NOMOS_VERSION", FALLBACK_VERSION))
        sys.stdout.write(json.dumps(asdict(target)) + "\n")
        return 0
    effective_args = ["mcp"] if (not args and not sys.stdin.isatty()) else args
    quick_target = resolve_target_platform(FALLBACK_VERSION)
    bin_path = find_existing_system_binary(quick_target.bin_name)
    if not bin_path:
        bin_path = ensure_cached_binary(resolve_target_platform(resolve_effective_version()))
    completed = subprocess.run([bin_path, *effective_args], check=False)
    return int(completed.returncode)


def main() -> None:
    """Console script entrypoint for uvx nomos-cli and nomos."""
    try:
        raise SystemExit(run_cli())
    except Exception as exc:
        sys.stderr.write(f"[nomos-pypi] {exc}\n")
        raise SystemExit(1) from exc
