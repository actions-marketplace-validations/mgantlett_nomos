import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';

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
});
