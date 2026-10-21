const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const runner = path.join(__dirname, 'run-browser-tests.js');

function fixture(t, files) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'razdacha-browser-runner-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  for (const [name, source] of Object.entries(files)) fs.writeFileSync(path.join(directory, name), source);
  return directory;
}
function run(directory, timeoutMs) {
  return spawnSync(process.execPath, ['-e', `process.exitCode=require(${JSON.stringify(runner)}).runSuites(${JSON.stringify(directory)},${JSON.stringify({ timeoutMs })})`], {
    encoding: 'utf8', timeout: 10_000,
  });
}
const record = value => `require('node:fs').appendFileSync(require('node:path').join(__dirname,'order'),${JSON.stringify(value)});`;

test('discovers suites in stable sequential order and excludes helpers', t => {
  const directory = fixture(t, {
    'b.cjs': record('b'),
    'a.cjs': record('a'),
    'support.helper.cjs': "throw new Error('Helper must not run')",
    'data.fixture.cjs': "throw new Error('Fixture must not run')",
  });
  const result = run(directory);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(fs.readFileSync(path.join(directory, 'order'), 'utf8'), 'ab');
});
test('a failing suite returns its exit code and prevents subsequent execution', t => {
  const directory = fixture(t, { 'a.cjs': 'process.exit(7)', 'b.cjs': record('b') });
  const result = run(directory);
  assert.equal(result.status, 7);
  assert.match(result.stderr, /a.cjs.*exit 7/);
  assert(!fs.existsSync(path.join(directory, 'order')));
});
test('a hung suite times out and prevents subsequent execution', t => {
  const directory = fixture(t, { 'a.cjs': 'setInterval(()=>{},1000)', 'b.cjs': record('b') });
  const result = run(directory, 200);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /a.cjs.*ETIMEDOUT/);
  assert(!fs.existsSync(path.join(directory, 'order')));
});
test('an empty suite directory fails instead of reporting success', t => {
  const directory = fixture(t, {});
  const result = run(directory);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /No browser regression suites found/);
});
