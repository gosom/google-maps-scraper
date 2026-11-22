import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { chmod, mkdtemp, mkdir, readFile, rmdir, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import test from 'node:test';
import { parseOptions } from './run-local.mjs';

const execFileAsync = promisify(execFile);
async function exec(command, args, options = {}) {
  const env = { ...(options.env || process.env) };
  delete env.NODE_TEST_CONTEXT;
  return execFileAsync(command, args, { ...options, env });
}
const scripts = fileURLToPath(new URL('.', import.meta.url));
const installer = fileURLToPath(new URL('../../../install.sh', import.meta.url));
const node = process.execPath;
const run = (args, env = process.env) => exec(node, [join(scripts, 'run-local.mjs'), ...args], { env, timeout: 10000 });
async function executable(path, text) { await writeFile(path, text); await chmod(path, 0o755); }
async function finished(directory) {
  for (let i = 0; i < 100; i++) {
    const state = JSON.parse(await readFile(join(directory, 'run.json'), 'utf8'));
    if (state.status === 'exited') {
      // Worker releases its lock immediately after persisting completion.
      await new Promise(r => setTimeout(r, 25));
      return state;
    }
    await new Promise(r => setTimeout(r, 25));
  }
  throw new Error('Worker did not finish');
}

test('native run preserves partial results, resumes, and reports a stopped elapsed time', async () => {
  const root = await mkdtemp(join(tmpdir(), 'gmaps-native-test-'));
  const binary = join(root, 'fake scraper');
  await executable(binary, `#!${node}\nconst fs = require('node:fs');
const args = process.argv.slice(2);
const output = args[args.indexOf('-results') + 1];
if (!args.includes('-resume')) process.exit(99);
if (!fs.existsSync(output)) fs.writeFileSync(output, 'title,address\\n');
fs.appendFileSync(output, 'Cafe,"Floor 1\\nMain Street"\\n');
setTimeout(() => process.exit(process.env.FAKE_FAIL === '1' ? 2 : 0), 100);
`);
  const queries = join(root, 'queries.txt');
  await writeFile(queries, 'cafes in Nicosia\n');
  const directory = join(root, 'crawl');
  const args = ['--backend', 'native', '--executable', binary, '--queries', queries, '--output-dir', directory];
  await run(args, { ...process.env, FAKE_FAIL: '1' });
  assert.equal((await finished(directory)).exit_code, 2);
  const first = await readFile(join(directory, 'results.csv'), 'utf8');
  await assert.rejects(run(args), (error) => error.code === 1);
  assert.equal(await readFile(join(directory, 'results.csv'), 'utf8'), first);
  await run([...args, '--resume']);
  assert.equal((await finished(directory)).exit_code, 0);
  const { stdout } = await exec(node, [join(scripts, 'status-local.mjs'), '--run-dir', directory]);
  const status = JSON.parse(stdout);
  assert.equal(status.result_count, 2);
  assert.equal(status.exit_code, 0);
  assert.equal(status.status, 'exited');
  // Simulate a restart: results survive, but the supervisor never saved completion.
  const stale = JSON.parse(await readFile(join(directory, 'run.json'), 'utf8'));
  stale.status = 'running';
  delete stale.exit_code;
  delete stale.finished_at;
  await writeFile(join(directory, 'run.json'), JSON.stringify(stale));
  await mkdir(join(directory, '.agent-lock'));
  const recoveredStatus = await exec(node, [join(scripts, 'status-local.mjs'), '--run-dir', directory]);
  assert.equal(JSON.parse(recoveredStatus.stdout).status, 'interrupted');
  await assert.rejects(run([...args, '--resume']), /recover-interrupted/);
  await assert.rejects(run([...args, '--resume', '--recover-interrupted']), /EEXIST/);
  await rmdir(join(directory, '.agent-lock'));
  await writeFile(join(directory, 'run.json'), JSON.stringify({ ...stale, pid: process.pid }));
  await assert.rejects(run([...args, '--resume', '--recover-interrupted']), /has not stopped/);
  await writeFile(join(directory, 'run.json'), JSON.stringify(stale));
  await run([...args, '--resume', '--recover-interrupted']);
  assert.equal((await finished(directory)).exit_code, 0);
  const recovered = await exec(node, [join(scripts, 'status-local.mjs'), '--run-dir', directory]);
  assert.equal(JSON.parse(recovered.stdout).result_count, 3);
  await writeFile(queries, 'changed query\n');
  await assert.rejects(run([...args, '--resume']), (error) => error.code === 1);
});

test('run options reject invalid geographic coverage and review format', () => {
  const base = ['--queries', 'queries', '--output-dir', 'output'];
  assert.throws(() => parseOptions([...base, '--extra-reviews']), /require/);
  assert.throws(() => parseOptions([...base, '--grid-bbox', '91,0,92,1']), /Invalid/);
  assert.throws(() => parseOptions([...base, '--grid-cell', '0']), /positive/);
  assert.throws(() => parseOptions([...base, '--depth']), /missing/);
});

test('standalone installer verifies before execution, reuses assets, and preserves an existing binary on mismatch', async () => {
  const root = await mkdtemp(join(tmpdir(), 'gmaps-install-test-'));
  const bin = join(root, 'bin');
  await mkdir(bin);
  const payload = '#!/usr/bin/env bash\nif [[ "${1:-}" == -version ]]; then echo v9.8.7; fi\n';
  const digest = createHash('sha256').update(payload).digest('hex');
  await executable(join(bin, 'uname'), '#!/usr/bin/env bash\nif [[ "$1" == -s ]]; then echo Linux; else echo aarch64; fi\n');
  await executable(join(bin, 'curl'), `#!${node}
const fs = require('node:fs');
const args = process.argv.slice(2);
const url = args.find(a => a.startsWith('https://'));
const output = args[args.indexOf('-o') + 1];
fs.appendFileSync(process.env.DOWNLOAD_LOG, url + '\\n');
if (url.endsWith('/latest')) { process.stdout.write('https://github.com/gosom/google-maps-scraper/releases/tag/v9.8.7'); process.exit(0); }
if (url.endsWith('/checksums.txt')) fs.writeFileSync(output, (process.env.BAD_HASH ? '0'.repeat(64) : ${JSON.stringify(digest)}) + '  google_maps_scraper-9.8.7-linux-arm64\\n');
else fs.writeFileSync(output, ${JSON.stringify(payload)});
`);
  const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, XDG_CONFIG_HOME: join(root, 'config'), DOWNLOAD_LOG: join(root, 'downloads') };
  const args = [installer, '--install-dir', join(root, 'installed')];
  const { stdout } = await exec('bash', args, { env });
  const installed = stdout.trim();
  assert.equal(await readFile(installed, 'utf8'), payload);
  assert.equal((await readFile(join(root, 'config/google-maps-scraper/native-executable'), 'utf8')).trim(), installed);
  await exec('bash', args, { env });
  const downloads = (await readFile(env.DOWNLOAD_LOG, 'utf8')).split('\n');
  assert.equal(downloads.filter(line => line.endsWith('/google_maps_scraper-9.8.7-linux-arm64')).length, 1);
  await assert.rejects(exec('bash', args, { env: { ...env, BAD_HASH: '1' } }), /Checksum mismatch/);
  assert.equal(await readFile(installed, 'utf8'), payload);
});

test('Docker uses the same run/status contract and refuses resume while its container is active', async () => {
  const root = await mkdtemp(join(tmpdir(), 'gmaps-docker-test-'));
  const bin = join(root, 'bin');
  await mkdir(bin);
  await executable(join(bin, 'docker'), `#!${node}
const fs = require('node:fs');
const args = process.argv.slice(2);
if (args[0] === 'image') console.log('sha256:fake-image');
if (args[0] === 'inspect') {
  if (process.env.DOCKER_UNAVAILABLE) { console.error('Cannot connect to the Docker daemon'); process.exit(1); }
  if (process.env.CONTAINER_MISSING) { console.error('Error: No such object: ' + args.at(-1)); process.exit(1); }
  const status = process.env.CONTAINER_RUNNING ? 'running' : 'exited';
  if (args.includes('{{.State.Status}}')) console.log(status);
  else console.log(JSON.stringify({ Status: status, ExitCode: 0, FinishedAt: new Date().toISOString() }));
}
if (args[0] === 'run') {
  const mount = args.find(a => a.endsWith(':/out'));
  const dir = mount.slice(0, -5);
  if (!args.includes('sha256:fake-image') || !args.includes('-resume')) process.exit(99);
  if (args.includes('--user')) process.exit(98);
  const output = dir + '/results.csv';
  if (!fs.existsSync(output) || (fs.statSync(output).mode & 0o777) !== 0o600 || fs.statSync(output).uid !== process.getuid()) process.exit(96);
  if (args.includes('gmaps-playwright-cache:/opt')) process.exit(97);
  fs.writeFileSync(dir + '/results.csv', 'title,address\\nCafe,Main Street\\n');
}
`);
  const env = { ...process.env, PATH: `${bin}:${process.env.PATH}` };
  const queries = join(root, 'queries.txt');
  await writeFile(queries, 'cafes in Nicosia\n');
  const directory = join(root, 'crawl');
  const args = ['--backend', 'docker', '--queries', queries, '--output-dir', directory];
  await run(args, env);
  assert.equal((await finished(directory)).exit_code, 0);
  const { stdout } = await exec(node, [join(scripts, 'status-local.mjs'), '--run-dir', directory], { env });
  assert.equal(JSON.parse(stdout).result_count, 1);
  await assert.rejects(run([...args, '--resume'], { ...env, CONTAINER_RUNNING: '1' }), /has not stopped/);
  const statusArgs = [join(scripts, 'status-local.mjs'), '--run-dir', directory];
  const removed = await exec(node, statusArgs, { env: { ...env, CONTAINER_MISSING: '1' } });
  const saved = JSON.parse(removed.stdout);
  assert.equal(saved.status, 'exited');
  assert.equal(saved.exit_code, 0);
  assert.equal(saved.result_count, 1);
  await assert.rejects(exec(node, statusArgs, { env: { ...env, DOCKER_UNAVAILABLE: '1' } }), /Cannot connect/);
  await run([...args, '--resume'], { ...env, CONTAINER_MISSING: '1' });
  const stale = await finished(directory);
  stale.status = 'running';
  delete stale.exit_code;
  await writeFile(join(directory, 'run.json'), JSON.stringify(stale));
  await assert.rejects(run([...args, '--resume'], env), /recover-interrupted/);
  await assert.rejects(run([...args, '--resume', '--recover-interrupted'], { ...env, CONTAINER_RUNNING: '1' }), /has not stopped/);
  const missingActive = await exec(node, statusArgs, { env: { ...env, CONTAINER_MISSING: '1' } });
  assert.equal(JSON.parse(missingActive.stdout).status, 'interrupted');
  assert.equal(JSON.parse(missingActive.stdout).exit_code, undefined);
  await run([...args, '--resume', '--recover-interrupted'], env);
  assert.equal((await finished(directory)).exit_code, 0);

});
