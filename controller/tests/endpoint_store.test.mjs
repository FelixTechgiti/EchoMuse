// Tests for publishedStoreState() in dashboard.jsx — what the fleet's binary
// store holds against what the published release offers.
//
//     node controller/tests/endpoint_store.test.mjs
//
// Source extraction rather than import, for the reason boot_target.test.mjs
// gives.
//
// The state worth the whole feature is `unmanaged`. The automatic fetch
// replaces only what the controller can prove it wrote — right, because it
// must never overwrite a patched build somebody is testing — but that same
// rule covers every store filled before provenance existed, which is all of
// them. A user who uploaded a binary by hand once was stuck with it for ever,
// with nothing on screen saying why the published build never arrived.

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

const { publishedStoreState } = await import(
  "data:text/javascript;base64," + Buffer.from(
    liftFunction("publishedStoreState") + "\nexport { publishedStoreState };"
  ).toString("base64"));

let failures = 0;
function check(name, fn) {
  try { fn(); console.log(`  ok  ${name}`); }
  catch (e) { failures++; console.error(`FAIL  ${name}\n      ${e.message}`); }
}

check("a hand-uploaded binary says it will never be replaced, and offers the way out", () => {
  const st = publishedStoreState({ kind: "librespot", state: "unmanaged" },
                                 "endpoints-v1.2.0");
  assert.match(st.text, /by hand/,
    `the reason the published build never arrives is not stated: ${st.text}`);
  assert.match(st.text, /leave this alone|leaves this alone/,
    `it does not say the automatic fetch skips it: ${st.text}`);
  assert.ok(st.action, "no way back to the published build — this is #47");
  assert.match(st.action, /published build/);
});

check("the published build is named as such and offers nothing", () => {
  const st = publishedStoreState({ kind: "librespot", state: "published" },
                                 "endpoints-v1.2.0");
  assert.match(st.text, /published build/);
  assert.match(st.text, /endpoints-v1\.2\.0/, "the tag is not shown");
  assert.strictEqual(st.action, null,
    "a button that would replace the current build with itself");
});

check("an outdated store names both tags", () => {
  const st = publishedStoreState(
    { kind: "librespot", state: "outdated", stored_tag: "endpoints-v1.0.0" },
    "endpoints-v1.2.0");
  assert.match(st.text, /endpoints-v1\.0\.0/, "what is stored is not named");
  assert.match(st.text, /endpoints-v1\.2\.0/, "what is published is not named");
  assert.ok(st.action, "no way to take the newer build now");
});

check("an empty store says the fetch is automatic", () => {
  // The button only makes the next poll happen sooner. Saying so stops
  // somebody concluding that nothing will happen unless they press it.
  const st = publishedStoreState({ kind: "librespot", state: "empty" },
                                 "endpoints-v1.2.0");
  assert.match(st.text, /automatically/);
  assert.ok(st.action);
});

check("with no published release nothing is offered", () => {
  // A button that cannot work is worse than none: pressing it produces an
  // error about a release, which is not what the user was asking about.
  for (const state of ["empty", "outdated", "unmanaged"]) {
    const st = publishedStoreState({ kind: "librespot", state }, null);
    assert.strictEqual(st.action, null,
      `${state} offered an action with no release to take it from`);
  }
  const st = publishedStoreState({ kind: "librespot", state: "empty" }, null);
  assert.match(st.text, /no published release/,
    `an empty store with no release must say why nothing is coming: ${st.text}`);
});

check("an unknown or missing kind renders nothing at all", () => {
  // The endpoint list and the store's kinds are fetched separately, so one
  // can arrive first. A row with no store entry must stay silent rather than
  // claim a state.
  assert.strictEqual(publishedStoreState(null, "endpoints-v1.2.0").text, null);
  assert.strictEqual(publishedStoreState(undefined, null).text, null);
  assert.strictEqual(
    publishedStoreState({ kind: "librespot", state: "something_new" }, "t").text,
    null, "an unrecognised state invented a sentence");
});

process.exit(failures === 0 ? 0 : 1);
