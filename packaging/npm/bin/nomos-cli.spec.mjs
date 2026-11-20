import assert from 'node:assert/strict';
import { execFile, execFileSync } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const cliPath = path.join(__dirname, 'nomos-cli.mjs');

describe('@mgantlett/nomos SHA-256 CLI launcher', () => {
	it('resolves platform archive metadata and strips leading v on NOMOS_VERSION', () => {
		const out = execFileSync(process.execPath, [cliPath, '--launcher-info'], {
			env: { ...process.env, NOMOS_VERSION: 'v4.27.1' },
			encoding: 'utf8'
		});
		const info = JSON.parse(out.trim());
		assert.equal(info.version, '4.27.1');
		assert.equal(typeof info.goos, 'string');
		assert.equal(typeof info.goarch, 'string');
		assert.equal(info.archive, `nomos_${info.goos}_${info.goarch}.${info.ext}`);
	});

	it('rejects tampered SHA-256 checksums and executes valid archive via NOMOS_RELEASE_BASE_URL', async () => {
		const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'nomos-cli-spec-'));
		const info = JSON.parse(
			execFileSync(process.execPath, [cliPath, '--launcher-info'], {
				env: { ...process.env, NOMOS_VERSION: '99.0.0' },
				encoding: 'utf8'
			}).trim()
		);
		if (info.goos === 'Windows') return;
		const stubBin = path.join(tmpDir, info.binName);
		fs.writeFileSync(stubBin, '#!/bin/sh\necho "nomos-stub-99.0.0:$*"\n', { mode: 0o755 });
		const archivePath = path.join(tmpDir, info.archive);
		execFileSync('tar', ['-czf', archivePath, '-C', tmpDir, info.binName]);
		const archiveBuf = fs.readFileSync(archivePath);
		const shaHex = crypto.createHash('sha256').update(archiveBuf).digest('hex');

		const srv = http.createServer((req, res) => {
			if (req.url === '/tampered/checksums.txt') {
				res.end(`${'0'.repeat(64)}  ${info.archive}\n`);
				return;
			}
			if (req.url === '/valid/checksums.txt') {
				res.end(`${shaHex}  ${info.archive}\n`);
				return;
			}
			if (req.url?.endsWith(`/${info.archive}`)) {
				res.end(archiveBuf);
				return;
			}
			res.statusCode = 404;
			res.end('not found');
		});
		await new Promise((resolve) => srv.listen(0, '127.0.0.1', resolve));
		const port = srv.address().port;
		try {
			await assert.rejects(
				execFileAsync(process.execPath, [cliPath, '--version'], {
					env: {
						...process.env,
						NOMOS_SKIP_SYSTEM_PATH: '1',
						NOMOS_VERSION: '99.0.0',
						NOMOS_CACHE_DIR: path.join(tmpDir, 'cache-bad'),
						NOMOS_RELEASE_BASE_URL: `http://127.0.0.1:${port}/tampered`
					}
				}),
				/SHA-256 mismatch/
			);

			const { stdout } = await execFileAsync(process.execPath, [cliPath, '--version'], {
				env: {
					...process.env,
					NOMOS_SKIP_SYSTEM_PATH: '1',
					NOMOS_VERSION: '99.0.0',
					NOMOS_CACHE_DIR: path.join(tmpDir, 'cache-ok'),
					NOMOS_RELEASE_BASE_URL: `http://127.0.0.1:${port}/valid`
				}
			});
			assert.match(stdout, /nomos-stub-99\.0\.0:--version/);
		} finally {
			srv.close();
			fs.rmSync(tmpDir, { recursive: true, force: true });
		}
	});
});
