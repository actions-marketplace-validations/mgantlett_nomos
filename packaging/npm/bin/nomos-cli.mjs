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
const PINNED_VERSION = String(process.env.NOMOS_VERSION || pkgJson.version || '4.27.0').replace(/^v/, '');
const RELEASE_BASE = `https://github.com/mgantlett/nomos/releases/download/v${PINNED_VERSION}`;

function resolveTargetPlatform() {
	const platMap = { linux: 'linux', darwin: 'darwin', win32: 'windows' };
	const archMap = { x64: 'x86_64', arm64: 'arm64' };
	const goos = platMap[process.platform];
	const goarch = archMap[process.arch];
	if (!goos || !goarch) {
		throw new Error(`Unsupported OS/arch combination: ${process.platform}/${process.arch}`);
	}
	const ext = goos === 'windows' ? 'zip' : 'tar.gz';
	const binName = goos === 'windows' ? 'nomos.exe' : 'nomos';
	const archive = `nomos_${PINNED_VERSION}_${goos}_${goarch}.${ext}`;
	return { goos, goarch, ext, binName, archive };
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
	const res = await fetch(url, { headers: { 'User-Agent': 'sophialabs-nomos-npx-launcher' } });
	if (!res.ok) {
		throw new Error(`Download failed (${res.status}) for ${url}`);
	}
	return Buffer.from(await res.arrayBuffer());
}

async function ensureCachedBinary(target) {
	const cacheRoot = process.env.NOMOS_CACHE_DIR || path.join(os.homedir(), '.cache', 'nomos-launcher');
	const verDir = path.join(cacheRoot, `v${PINNED_VERSION}-${target.goos}-${target.goarch}`);
	const binPath = path.join(verDir, target.binName);
	if (fs.existsSync(binPath)) return binPath;

	fs.mkdirSync(verDir, { recursive: true });
	const [checksumsBuf, archiveBuf] = await Promise.all([
		fetchReleaseBytes(`${RELEASE_BASE}/checksums.txt`),
		fetchReleaseBytes(`${RELEASE_BASE}/${target.archive}`)
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
	if (target.goos !== 'windows') fs.chmodSync(binPath, 0o755);
	return binPath;
}

async function main() {
	const args = process.argv.slice(2);
	const target = resolveTargetPlatform();
	if (args[0] === '--launcher-info') {
		process.stdout.write(`${JSON.stringify({ version: PINNED_VERSION, ...target })}\n`);
		return;
	}
	const selfReal = fs.realpathSync(fileURLToPath(import.meta.url));
	const bin = findExistingSystemBinary(selfReal, target.binName) || (await ensureCachedBinary(target));
	const child = spawnSync(bin, args, { stdio: 'inherit' });
	if (child.error) throw child.error;
	process.exit(child.status ?? 0);
}

main().catch((err) => {
	process.stderr.write(`[nomos-npx] ${err instanceof Error ? err.message : String(err)}\n`);
	process.exit(1);
});
