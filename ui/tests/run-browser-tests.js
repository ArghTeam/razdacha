// Every top-level .cjs file is a suite, except explicitly named fixture/helper files.
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

function runSuites(directory, { timeoutMs = 180_000 } = {}) {
  const suites = fs.readdirSync(directory)
    .filter(name => name.endsWith('.cjs') && !/\.(fixture|helper)\.cjs$/.test(name))
    .sort();
  if (suites.length === 0) throw new Error('No browser regression suites found');

  for (const suite of suites) {
    console.log(`\nRunning browser suite: ${suite}`);
    const result = spawnSync(process.execPath, [path.join(directory, suite)], {
      cwd: path.resolve(directory, '../..'),
      stdio: 'inherit',
      env: process.env,
      timeout: timeoutMs,
      killSignal: 'SIGKILL',
    });
    if (result.error || result.signal || result.status !== 0) {
      console.error(`Browser suite failed: ${suite}`, result.error?.message || result.signal || `exit ${result.status}`);
      return result.status || 1;
    }
  }
  console.log(`\nAll ${suites.length} browser regression suites passed.`);
  return 0;
}

if (require.main === module) process.exitCode = runSuites(__dirname);
module.exports = { runSuites };
