#!/usr/bin/env node
/**
 * nomos-cli.mjs - Zero-dependency SHA-256 verified launcher for the static Go `nomos` binary.
 * Resolves system PATH `nomos` first, or downloads and verifies the pinned GitHub Release archive.
 */
import { execFileSync, spawnSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const pkgJson = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'package.json'), 'utf8'));
const FALLBACK_VERSION = String(pkgJson.version || '4.27.1').replace(/^v/, '');

async function resolveEffectiveVersion() {
	if (process.env.NOMOS_VERSION) {
		return String(process.env.NOMOS_VERSION).replace(/^v/, '');
	}
	try {
		const res = await fetch('https://api.github.com/repos/mgantlett/nomos/releases/latest', {
			headers: {
				Accept: 'application/vnd.github+json',
				'User-Agent': 'mgantlett-nomos-npx-launcher'
			},
			signal: AbortSignal.timeout(2500)
		});
		if (res.ok) {
			const data = await res.json();
			const tag = String(data?.tag_name || '').replace(/^v/, '');
			if (/^\d+\.\d+\.\d+/.test(tag)) return tag;
		}
	} catch {
		// Fall back to bundled manifest version when offline or rate-limited.
	}
	return FALLBACK_VERSION;
}

function resolveTargetPlatform(rawVersion) {
	const version = String(rawVersion || FALLBACK_VERSION).replace(/^v/, '');
	const platMap = { linux: 'Linux', darwin: 'Darwin', win32: 'Windows' };
	const archMap = { x64: 'x86_64', arm64: 'arm64' };
	const goos = platMap[process.platform];
	const goarch = archMap[process.arch];
	if (!goos || !goarch) {
		throw new Error(`Unsupported OS/arch combination: ${process.platform}/${process.arch}`);
	}
	const ext = goos === 'Windows' ? 'zip' : 'tar.gz';
	const binName = goos === 'Windows' ? 'nomos.exe' : 'nomos';
	const archive = `nomos_${goos}_${goarch}.${ext}`;
	return { version, goos, goarch, ext, binName, archive };
}

function findExistingSystemBinary(selfPath, binName) {
	if (process.env.NOMOS_SKIP_SYSTEM_PATH === '1') return null;
	const dirs = String(process.env.PATH || '').split(path.delimiter).filter(Boolean);
	for (const d of dirs) {
		const candidate = path.join(d, binName);
		try {
			if (fs.existsSync(candidate) && fs.realpathSync(candidate) !== selfPath) {
				fs.accessSync(candidate, fs.constants.X_OK);
				return candidate;
			}
		} catch {
			// Ignore unreadable PATH entries.
		}
	}
	return null;
}

function parseExpectedSha256(checksumsText, archiveName) {
	for (const line of String(checksumsText).split(/\r?\n/)) {
		const trimmed = line.trim();
		if (!trimmed) continue;
		const parts = trimmed.split(/\s+/);
		if (parts.length >= 2 && parts[1].replace(/^\*/, '') === archiveName) {
			return parts[0].toLowerCase();
		}
	}
	throw new Error(`Archive ${archiveName} not found in release checksums.txt`);
}

async function fetchReleaseBytes(url) {
	const res = await fetch(url, { headers: { 'User-Agent': 'mgantlett-nomos-npx-launcher' } });
	if (!res.ok) {
		throw new Error(`Download failed (${res.status}) for ${url}`);
	}
	return Buffer.from(await res.arrayBuffer());
}

async function ensureCachedBinary(target) {
	const releaseBase = String(
		process.env.NOMOS_RELEASE_BASE_URL ||
			`https://github.com/mgantlett/nomos/releases/download/v${target.version}`
	).replace(/\/+$/, '');
	const cacheRoot = process.env.NOMOS_CACHE_DIR || path.join(os.homedir(), '.cache', 'nomos-launcher');
	const verDir = path.join(cacheRoot, `v${target.version}-${target.goos}-${target.goarch}`);
	const binPath = path.join(verDir, target.binName);
	if (fs.existsSync(binPath)) return binPath;

	fs.mkdirSync(verDir, { recursive: true });
	const [checksumsBuf, archiveBuf] = await Promise.all([
		fetchReleaseBytes(`${releaseBase}/checksums.txt`),
		fetchReleaseBytes(`${releaseBase}/${target.archive}`)
	]);
	const expectedHex = parseExpectedSha256(checksumsBuf.toString('utf8'), target.archive);
	const actualHex = crypto.createHash('sha256').update(archiveBuf).digest('hex').toLowerCase();
	if (actualHex !== expectedHex) {
		throw new Error(`SHA-256 mismatch for ${target.archive}: expected ${expectedHex}, got ${actualHex}`);
	}

	const archivePath = path.join(verDir, target.archive);
	fs.writeFileSync(archivePath, archiveBuf);
	if (target.ext === 'tar.gz') {
		execFileSync('tar', ['-xzf', archivePath, '-C', verDir, target.binName]);
	} else {
		execFileSync('tar', ['-xf', archivePath, '-C', verDir, target.binName]);
	}
	fs.unlinkSync(archivePath);
	if (target.goos !== 'Windows') fs.chmodSync(binPath, 0o755);
	return binPath;
}

async function main() {
	const args = process.argv.slice(2);
	if (args[0] === '--launcher-info') {
		const target = resolveTargetPlatform(process.env.NOMOS_VERSION || FALLBACK_VERSION);
		process.stdout.write(`${JSON.stringify(target)}\n`);
		return;
	}
	const effectiveArgs = args.length === 0 && !process.stdin.isTTY ? ['mcp'] : args;
	const selfReal = fs.realpathSync(fileURLToPath(import.meta.url));
	const quickTarget = resolveTargetPlatform(FALLBACK_VERSION);
	const systemBin = findExistingSystemBinary(selfReal, quickTarget.binName);
	const bin = systemBin || (await ensureCachedBinary(resolveTargetPlatform(await resolveEffectiveVersion())));
	const child = spawnSync(bin, effectiveArgs, { stdio: 'inherit' });
	if (child.error) throw child.error;
	process.exit(child.status ?? 0);
}

main().catch((err) => {
	process.stderr.write(`[nomos-npx] ${err instanceof Error ? err.message : String(err)}\n`);
	process.exit(1);
});
