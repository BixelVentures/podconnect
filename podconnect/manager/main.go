// PodConnect manager — a tiny stdlib HTTP service that powers the HA Ingress panel.
//
// It surfaces OwnTone's live AirPlay scan (GET /api/outputs) as a click-to-pick list so the
// user never has to type a HomePod name. The chosen name is persisted to
// /data/selected_output.json, which the select-homepod s6 service reads (preferring it over the
// static homepod_name config option) to lock OwnTone onto that output.
//
// No third-party deps on purpose: this is also the seed of the future manager (volume relay,
// multi-room spawn). Keep it boring and dependency-free.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	owntone = envOr("OWNTONE_URL", "http://localhost:3689")
	dataDir = envOr("DATA_DIR", "/data")
	port    = envOr("PORT", "8099")
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

type device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Selected  bool   `json:"selected"`
	NeedsAuth bool   `json:"needs_auth"`
}

type stateResp struct {
	Speaker   string   `json:"speaker"`
	Saved     string   `json:"saved"`
	OwntoneUp bool     `json:"owntone_up"`
	Devices   []device `json:"devices"`
}

func selectionPath() string { return filepath.Join(dataDir, "selected_output.json") }

func readSaved() string {
	b, err := os.ReadFile(selectionPath())
	if err != nil {
		return ""
	}
	var s struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(b, &s)
	return s.Name
}

func writeSaved(name string) error {
	b, _ := json.Marshal(map[string]string{"name": name})
	return os.WriteFile(selectionPath(), b, 0o644)
}

func readSpeaker() string {
	b, err := os.ReadFile(filepath.Join(dataDir, "options.json"))
	if err != nil {
		return ""
	}
	var o struct {
		SpeakerName string `json:"speaker_name"`
	}
	_ = json.Unmarshal(b, &o)
	return o.SpeakerName
}

// fetchOutputs returns the AirPlay outputs OwnTone has discovered, and whether OwnTone answered.
// Parsed defensively (map + UseNumber) so a field OwnTone serializes as a number rather than a
// string — notably the 64-bit output "id" — can't make a strict decoder drop every device.
func fetchOutputs() ([]device, bool) {
	cl := &http.Client{Timeout: 4 * time.Second}
	resp, err := cl.Get(owntone + "/api/outputs")
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var raw struct {
		Outputs []map[string]any `json:"outputs"`
	}
	if err := dec.Decode(&raw); err != nil {
		log.Printf("outputs decode error: %v", err)
		return nil, true
	}
	out := []device{}
	for _, o := range raw.Outputs {
		typ, _ := o["type"].(string)
		if !strings.EqualFold(typ, "airplay") {
			continue
		}
		name, _ := o["name"].(string)
		out = append(out, device{
			ID:        fmt.Sprint(o["id"]), // works whether id is a JSON string or number
			Name:      name,
			Selected:  asBool(o["selected"]),
			NeedsAuth: asBool(o["needs_auth_key"]) || asBool(o["requires_auth"]),
		})
	}
	return out, true
}

func asBool(v any) bool { b, _ := v.(bool); return b }

// selectOnOwntone activates exactly one output (the proven call select-homepod uses).
func selectOnOwntone(id string) {
	cl := &http.Client{Timeout: 4 * time.Second}
	req, _ := http.NewRequest(http.MethodPut, owntone+"/api/outputs/set", bytes.NewBufferString(`{"outputs":["`+id+`"]}`))
	if resp, err := cl.Do(req); err == nil {
		resp.Body.Close()
	}
}

// setMasterVolume sets OwnTone's player volume (0-100) so a stuck-near-zero level can't make the
// test (or playback) silent.
func setMasterVolume(pct int) {
	cl := &http.Client{Timeout: 4 * time.Second}
	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/player/volume?volume=%d", owntone, pct), nil)
	if resp, err := cl.Do(req); err == nil {
		resp.Body.Close()
	}
}

var toneMu sync.Mutex

// playTestTone writes ~3s of a 440 Hz sine (s16le/44100/stereo) into the pipe. OwnTone's
// pipe_autostart picks it up and plays it to the selected output — proving the OwnTone→AirPlay→
// HomePod leg with zero dependency on Spotify or go-librespot. Runs in a goroutine (the write
// drains at real time, ~3s). The lock prevents overlapping tones.
func playTestTone() {
	if !toneMu.TryLock() {
		return
	}
	defer toneMu.Unlock()
	f, err := os.OpenFile("/srv/media/spotify", os.O_WRONLY, 0)
	if err != nil {
		log.Printf("test tone: open pipe: %v", err)
		return
	}
	defer f.Close()
	const rate, secs, freq = 44100, 3, 440.0
	amp := 0.25 * 32767.0
	buf := make([]byte, 0, rate*secs*4)
	for i := 0; i < rate*secs; i++ {
		u := uint16(int16(amp * math.Sin(2*math.Pi*freq*float64(i)/float64(rate))))
		lo, hi := byte(u), byte(u>>8)
		buf = append(buf, lo, hi, lo, hi) // little-endian s16, L+R
	}
	if _, err := f.Write(buf); err != nil {
		log.Printf("test tone: write: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	lastCount := -1
	http.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		devs, up := fetchOutputs()
		if devs == nil {
			devs = []device{}
		}
		if up && len(devs) != lastCount {
			log.Printf("AirPlay devices visible to picker: %d", len(devs))
			lastCount = len(devs)
		}
		writeJSON(w, stateResp{Speaker: readSpeaker(), Saved: readSaved(), OwntoneUp: up, Devices: devs})
	})

	http.HandleFunc("/api/select", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		name := strings.TrimSpace(body.Name)
		if err := writeSaved(name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Apply immediately for instant feedback; select-homepod also keeps it locked.
		if name != "" {
			if devs, _ := fetchOutputs(); devs != nil {
				for _, d := range devs {
					if strings.EqualFold(d.Name, name) {
						selectOnOwntone(d.ID)
						break
					}
				}
			}
		}
		log.Printf("selection saved: %q", name)
		writeJSON(w, map[string]bool{"ok": true})
	})

	http.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		devs, _ := fetchOutputs()
		saved := readSaved()
		target := ""
		for _, d := range devs {
			if saved != "" && strings.EqualFold(d.Name, saved) {
				target = d.ID
				break
			}
		}
		if target == "" && len(devs) > 0 {
			target = devs[0].ID // fall back to the first discovered AirPlay device
		}
		if target != "" {
			selectOnOwntone(target)
		}
		setMasterVolume(55)
		go playTestTone()
		log.Printf("test tone requested (target output id=%q)", target)
		writeJSON(w, map[string]any{"ok": true, "playing": target != ""})
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, indexHTML)
	})

	log.Printf("podconnect-manager listening on :%s (owntone=%s)", port, owntone)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>PodConnect</title>
<style>
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
         margin: 0; padding: 24px; color: #1c1c1e; background: transparent; }
  @media (prefers-color-scheme: dark) { body { color: #f2f2f7; } }
  .wrap { max-width: 560px; margin: 0 auto; }
  h1 { font-size: 1.35rem; margin: 0 0 4px; }
  .sub { margin: 0 0 16px; opacity: .65; font-size: .9rem; }
  .status { padding: 10px 14px; border-radius: 10px; font-size: .9rem; margin-bottom: 14px; }
  .status.ok { background: rgba(48,209,88,.15); }
  .status.warn { background: rgba(255,159,10,.15); }
  .list { display: flex; flex-direction: column; gap: 8px; }
  .row { display: flex; align-items: center; gap: 12px; padding: 14px 16px; border-radius: 12px;
         border: 1px solid rgba(120,120,128,.25); cursor: pointer; transition: border-color .15s; }
  .row:hover { border-color: rgba(10,132,255,.6); }
  .row.active { border-color: rgba(48,209,88,.7); }
  .row input { width: 18px; height: 18px; accent-color: #0a84ff; }
  .name { flex: 1; font-weight: 600; }
  .badge { font-size: .72rem; padding: 3px 8px; border-radius: 999px; background: rgba(255,159,10,.2); }
  .badge.live { background: rgba(48,209,88,.22); }
  .actions { display: flex; gap: 10px; margin-top: 18px; }
  button { font: inherit; font-weight: 600; padding: 11px 18px; border-radius: 10px; border: 0;
           cursor: pointer; background: #0a84ff; color: #fff; }
  button:disabled { opacity: .4; cursor: default; }
  button.ghost { background: transparent; color: inherit; border: 1px solid rgba(120,120,128,.4); }
  .hint { margin-top: 18px; font-size: .82rem; opacity: .6; line-height: 1.4; }
</style>
</head>
<body>
<div class="wrap">
  <h1>Pick a HomePod</h1>
  <p class="sub" id="speaker"></p>
  <div id="status" class="status warn">Loading…</div>
  <div id="list" class="list"></div>
  <div class="actions">
    <button id="save" disabled>Save selection</button>
    <button id="auto" class="ghost">Auto (clear choice)</button>
  </div>
  <div class="actions">
    <button id="test" class="ghost">🔊 Play test sound on HomePod</button>
  </div>
  <p id="testmsg" class="hint"></p>
  <p class="hint">This list is a live network scan (OwnTone AirPlay discovery) — the same one that
  feeds Spotify Connect. No typing: pick a device and Save. Your speaker keeps playing to it across
  restarts.<br><br><b>Play test sound</b> sends a 3-second tone straight to the selected HomePod via
  AirPlay — no Spotify needed. Hear it = the HomePod audio path works.</p>
</div>
<script>
var chosen = null;
async function load() {
  var s;
  try { s = await (await fetch('api/state', {cache:'no-store'})).json(); }
  catch (e) { return; }
  document.getElementById('speaker').textContent = s.speaker ? ('Speaker: ' + s.speaker) : '';
  var st = document.getElementById('status');
  var list = document.getElementById('list');
  list.innerHTML = '';
  if (!s.owntone_up) { st.textContent = 'Audio engine starting…'; st.className = 'status warn'; return; }
  if (!s.devices.length) { st.textContent = 'Scanning the network for HomePods… (this can take a moment)'; st.className = 'status warn'; }
  else { st.textContent = s.devices.length + ' AirPlay device(s) found on your network'; st.className = 'status ok'; }
  var current = (chosen !== null) ? chosen : s.saved;
  s.devices.forEach(function (d) {
    var row = document.createElement('label');
    row.className = 'row' + (d.selected ? ' active' : '');
    var rb = document.createElement('input');
    rb.type = 'radio'; rb.name = 'hp'; rb.value = d.name;
    if (d.name === current) rb.checked = true;
    rb.onchange = function () { chosen = d.name; document.getElementById('save').disabled = false; };
    var nm = document.createElement('span'); nm.className = 'name'; nm.textContent = d.name;
    row.appendChild(rb); row.appendChild(nm);
    if (d.needs_auth) { var b = document.createElement('span'); b.className = 'badge'; b.textContent = 'needs verification'; row.appendChild(b); }
    if (d.selected) { var p = document.createElement('span'); p.className = 'badge live'; p.textContent = 'playing here'; row.appendChild(p); }
    list.appendChild(row);
  });
}
document.getElementById('save').onclick = async function () {
  if (chosen === null) return;
  this.disabled = true;
  await fetch('api/select', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: chosen }) });
  chosen = null;
  await load();
};
document.getElementById('auto').onclick = async function () {
  await fetch('api/select', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: '' }) });
  chosen = null;
  document.getElementById('save').disabled = true;
  await load();
};
document.getElementById('test').onclick = async function () {
  this.disabled = true;
  var m = document.getElementById('testmsg');
  m.textContent = 'Sending a 3-second tone to the HomePod — listen now…';
  try {
    var r = await (await fetch('api/test', { method: 'POST' })).json();
    m.textContent = r.playing
      ? 'Tone sent. You should hear a beep on the HomePod. No sound? Then it is the AirPlay/volume leg — not Spotify.'
      : 'No HomePod discovered/selected yet — wait for the list above, or pick one and Save first.';
  } catch (e) { m.textContent = 'Could not send the test.'; }
  var btn = this;
  setTimeout(function () { btn.disabled = false; }, 4000);
};
load();
setInterval(load, 5000);
</script>
</body>
</html>`
