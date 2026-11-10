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

const rescanHandler = source.match(/document\.getElementById\('addrescan'\)\.onclick = async function \(\) \{[\s\S]*?\n\};/)[0];
(async () => {
  for (const [response, expected] of [
    [{ok: true, status: 200, json: async () => ({ok: true})}, 'Rescan accepted. The audio engine is restarting.'],
    [{ok: false, status: 409, text: async () => 'original attention restoration pending\n'}, 'Rescan blocked (HTTP 409). Waiting for the original volume to be restored.'],
    [{ok: false, status: 503, text: async () => 'engine not running\n'}, 'Rescan unavailable (HTTP 503). The audio engine is not running.'],
    [{ok: false, status: 503, text: async () => 'engine replacement incomplete or superseded; retirement unknown\n'}, 'Rescan outcome unknown (HTTP 503). Check the Speakers log.'],
    [{ok: false, status: 502, text: async () => 'private upstream detail'}, 'Rescan outcome unknown (HTTP 502). Check the Speakers log.'],
    [{ok: true, status: 200, json: async () => ({})}, 'Rescan outcome unknown. Check the Speakers log.'],
    [null, 'Rescan outcome unknown. Check the Speakers log.'],
  ]) {
    const elements = {addrescan: {}, addstatus: {}, rescanstatus: {}};
    let calls = 0;
    const scope = {
      document: {getElementById: id => elements[id]},
      confirm: () => true,
      setTimeout: fn => fn(),
      fetch: async (path, options) => {
        calls++;
        assert.equal(path, 'api/rescan');
        assert.equal(options.method, 'POST');
        if (!response) throw Error('offline');
        return response;
      },
    };
    vm.runInNewContext(rescanHandler, scope);
    await elements.addrescan.onclick();
    elements.addstatus.textContent = 'Discovery refreshed';
    assert.equal(elements.rescanstatus.textContent, expected);
    assert.equal(elements.addrescan.disabled, false);
    assert.equal(calls, 1);
  }
  console.log('Rescan feedback: restarted, blocked, unavailable and unknown passed');
})();
