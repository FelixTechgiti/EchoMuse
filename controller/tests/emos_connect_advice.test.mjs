// Tests for what the wizard says when its first step cannot reach a device.
//
//     node controller/tests/emos_connect_advice.test.mjs
//
// Same source-extraction approach as web_usb_blocked.test.mjs: the function is
// lifted out of dashboard.jsx rather than imported, because the dashboard
// compiles to a single classic script. If it is renamed or moved, the lift
// fails loudly and this file must follow.
//
// **What matters here is the ADVICE, not the detection** — the same thing
// web_usb_blocked.test.mjs says about itself, and for a sharper reason.
// `No device selected.` covers two situations that look identical:
//
//   - a dismissed picker, a Dot that is off, a charge-only cable — retrying is
//     the right response;
//   - a Dot ALREADY RUNNING emOS, where retrying can never work, because emOS
//     has no adbd and never will.
//
// Nothing in the page can tell them apart, so the only thing standing between
// somebody and a dead end is the wording. Leaving out `/init recovery` is what
// #134 was filed for: the route has existed since emOS 0.4 and appeared in no
// dialog.

import { readFileSync } from "fs";
import { fileURLToPath } from "url";
import { dirname, join } from "path";

const HERE = dirname(fileURLToPath(import.meta.url));
const JSX = join(HERE, "..", "static", "dashboard.jsx");
const src = readFileSync(JSX, "utf8");

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

const connectFailureAdvice = new Function(
  `${liftFunction("connectFailureAdvice")}; return connectFailureAdvice;`)();

let failures = 0;
function check(label, pass, detail) {
  if (pass) {
    console.log(`ok    ${label}`);
  } else {
    console.log(`FAIL  ${label}${detail ? "  — " + detail : ""}`);
    failures++;
  }
}

const joined = (msg, step) =>
  connectFailureAdvice(msg, step).map(l => l.text).join("\n");

// ── The case the whole thing exists for ────────────────────────────────────

{
  const out = joined("No device selected.", "connect_android");
  check("an empty picker says something at all", out.length > 0);
  check("it names emOS as a possible cause", /emOS/.test(out), out);
  check("it says retrying cannot help there", /cannot help|never will/i.test(out), out);
  // The route. Without it this is a diagnosis with no next step, which is
  // exactly the state #134 describes.
  check("it names /init recovery", out.includes("/init recovery"), out);
  check("it says the step accepts a device already in TWRP",
        /TWRP/.test(out) && /accepts/i.test(out), out);
  // The ordinary cause must survive too. Somebody whose cable is charge-only
  // should not be sent to a console they do not need.
  check("it still names the ordinary causes",
        /charge-only|dismissed/i.test(out), out);
  check("it still says replug and retry", /replug/i.test(out), out);
}

// Chrome's own wording for a cancelled picker differs from the wrapper's, and
// neither string belongs to us.
check("Chrome's NotFoundError phrasing is covered",
      /emOS/.test(joined("NotFoundError: No device found.", "connect_android")));

// ── Where it must stay quiet ───────────────────────────────────────────────

// The secure-context refusal already carries its own fix, from webUsbBlocked.
// A second wording here is a second thing to keep in step.
check("nothing extra for a secure-context refusal",
      connectFailureAdvice(
        "WebUSB needs a secure context, and this page is on http://x. Use …",
        "connect_android").length === 0);

// A real failure mid-step is not a picker problem, and advice about emOS in
// front of it would be noise on top of a genuine error.
check("nothing extra for an unrelated failure",
      connectFailureAdvice("Failed to execute 'transferOut' on 'USBDevice'",
                           "connect_android").length === 0);

// Keyed on the step ID: every later step already has a device, so the same
// message there means something else entirely.
check("nothing on a later step",
      connectFailureAdvice("No device selected.", "flash_emos").length === 0);
check("nothing when the step id is missing",
      connectFailureAdvice("No device selected.", undefined).length === 0);

// Defensive: the catch block passes `e.message`, which is not always a string.
check("a non-string message does not throw",
      connectFailureAdvice(null, "connect_android").length === 0);

// ── The step description carries it too ────────────────────────────────────
//
// The advice above only appears AFTER a failure. Somebody on emOS should not
// have to fail first, and both flows start with this step — so both strings
// have to say it, which a single search-and-replace would miss if only one
// were changed.

{
  const descs = [...src.matchAll(/id: 'connect_android',[\s\S]*?desc: '([^']*)'/g)]
    .map(m => m[1]);
  check("both flows define the connect step", descs.length === 2,
        `found ${descs.length}`);
  check("both descriptions mention emOS",
        descs.length === 2 && descs.every(d => d.includes("emOS")),
        JSON.stringify(descs));
  check("both descriptions name /init recovery",
        descs.length === 2 && descs.every(d => d.includes("/init recovery")),
        JSON.stringify(descs));
}

// Every line is rendered by addLog, which takes a level. A typo there renders
// as the default class and the line stops looking like a warning.
{
  const levels = new Set(
    connectFailureAdvice("No device selected.", "connect_android")
      .map(l => l.level));
  check("every line carries a known level",
        [...levels].every(l => ["warn", "error", "ok"].includes(l)),
        [...levels].join(","));
}

if (failures) {
  console.log(`\n${failures} check(s) failed`);
  process.exit(1);
}
console.log("\nthe wizard names the emOS dead end and the way out of it");
