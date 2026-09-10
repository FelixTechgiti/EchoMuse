// Tests for endpointHealthLine() in dashboard.jsx — the sentence that says
// whether a streaming endpoint is actually RUNNING.
//
//     node controller/tests/endpoint_health.test.mjs
//
// Source extraction rather than import, for the reason boot_target.test.mjs
// gives: the dashboard compiles to a single classic script with no module
// boundary, so the alternative is a second copy that drifts.
//
// What is under test is a distinction that cost two hours on a real device.
// shairport-sync was installed, executable, the right size and reported
// `ok: true`, while an orphan from before the last OTA held TCP 5000 and
// every new instance exited immediately. Every panel said the endpoint was
// fine. The requirement is therefore not "show the state" — it is that the
// two ABSENCES (firmware too old, and no stats tick yet) never render as the
// failure, because accusing a working Echo is how this feature would become
// the thing people learn to ignore.

import { readFileSync } from "fs";
import { fileURLToPath } from "url";
import { dirname, join } from "path";
import assert from "assert";

const HERE = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(HERE, "..", "static", "dashboard.jsx"), "utf8");

function liftFunction(name) {
  const start = src.indexOf(`function ${name}`);
  if (start < 0) {
    throw new Error(`dashboard.jsx no longer defines ${name}() — if it was `
                  + `renamed or moved, update this test to match`);
  }
  let depth = 0;
  let i = src.indexOf("{", start);
  for (; i < src.length; i++) {
    if (src[i] === "{") depth++;
    else if (src[i] === "}") { depth--; if (depth === 0) break; }
  }
  return src.slice(start, i + 1);
}

const { endpointHealthLine } = await import(
  "data:text/javascript;base64," + Buffer.from(
    liftFunction("endpointHealthLine") + "\nexport { endpointHealthLine };"
  ).toString("base64"));

let failures = 0;
function check(name, fn) {
  try { fn(); console.log(`  ok  ${name}`); }
  catch (e) { failures++; console.error(`FAIL  ${name}\n      ${e.message}`); }
}

// ── The two absences ────────────────────────────────────────────────────────

check("firmware that cannot report says nothing", () => {
  assert.strictEqual(endpointHealthLine(null, false), null);
  // Even handed a health object: without the capability the field is not
  // something this firmware maintains, so it is not evidence.
  assert.strictEqual(
    endpointHealthLine({ enabled: true, alive: false, restarts: 9 }, false), null);
});

check("capable firmware with no tick yet says nothing", () => {
  // Up to 30s after connecting there is no stats message. Rendering "not
  // running" here would accuse every Echo for the first half minute of
  // every reconnect.
  assert.strictEqual(endpointHealthLine(null, true), null);
  assert.strictEqual(endpointHealthLine(undefined, true), null);
});

check("an endpoint nobody enabled says nothing", () => {
  // Not a fault and not a state — the user did not ask for it to run.
  assert.strictEqual(
    endpointHealthLine({ enabled: false, alive: false, restarts: 0 }, true), null);
});

// ── Running ────────────────────────────────────────────────────────────────

check("a running endpoint reports how long it has been up", () => {
  const line = endpointHealthLine(
    { enabled: true, alive: true, restarts: 1, uptimeS: 7325 }, true);
  assert.match(line, /^running/);
  assert.match(line, /2h/, `uptime not shown in hours: ${line}`);
});

check("uptime scales down to minutes and seconds", () => {
  assert.match(endpointHealthLine(
    { enabled: true, alive: true, uptimeS: 300 }, true), /5m/);
  assert.match(endpointHealthLine(
    { enabled: true, alive: true, uptimeS: 12 }, true), /12s/);
  // "running" alone is also true of something that started 200ms ago and is
  // about to die again, which is why the age is never omitted.
  assert.match(endpointHealthLine(
    { enabled: true, alive: true }, true), /up 0s/);
});

// ── The case the feature exists for ────────────────────────────────────────

check("an endpoint that keeps failing to start says so loudly", () => {
  // The 2026-09-10 shape: supervisor up, process dead, restarting every
  // minute with exit status 1 because an orphan holds the port.
  const line = endpointHealthLine({
    enabled: true, alive: false, restarts: 118,
    lastExit: "exit status 1",
  }, true);
  assert.match(line, /NOT running/,
    `a repeatedly failing endpoint must be unmissable: ${line}`);
  assert.match(line, /118/, `the restart count is the evidence: ${line}`);
  assert.match(line, /exit status 1/, `the reason is dropped: ${line}`);
});

check("one restart is not yet an accusation", () => {
  // Between sessions, or just started. Real, worth showing, not alarming.
  const line = endpointHealthLine({
    enabled: true, alive: false, restarts: 1, lastExit: "exited cleanly",
  }, true);
  assert.ok(!/NOT running/.test(line),
    `a single quiet restart was reported as a failure: ${line}`);
  assert.match(line, /not running/);
});

check("a preemption reads differently from a crash", () => {
  // `signal: killed` is us taking the plane away; `exit status 1` is a port
  // it cannot bind. Same "not running", opposite meanings.
  const killed = endpointHealthLine({
    enabled: true, alive: false, restarts: 4, lastExit: "signal: killed",
  }, true);
  assert.match(killed, /signal: killed/);
});

process.exit(failures === 0 ? 0 : 1);
