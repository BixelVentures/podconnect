// Executes the shipped panel handler with mocked I/O; not browser rendering proof.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../podconnect/manager/main.go'), 'utf8');
const handler = source.match(/st\.onclick = async function \(\) \{[\s\S]*?\n    \};/)[0];
(async () => {
  for (const scenario of ['accepted', 'rejected', 'unreachable']) {
    const alerts = [];
    let calls = 0;
    const scope = {st: {}, rm: {id: 'kitchen'}, encodeURIComponent,
      alert: message => alerts.push(message),
      fetch: async () => { calls++; if (scenario === 'unreachable') throw Error('offline'); return {ok: scenario === 'accepted'}; }
    };
    vm.runInNewContext(handler, scope);
    await scope.st.onclick();
    assert.equal(calls, 1);
    assert.equal(scope.st.disabled, false);
    assert.equal(alerts.length, scenario === 'accepted' ? 0 : 1);
  }
  console.log('Stop feedback: accepted, rejected, unreachable passed');
})();
