// Tests for _pmVerdict() in dashboard.jsx — what the wizard concludes from a
// `pm disable` / `pm hide` reply.
//
//     node controller/tests/pm_verdict.test.mjs
//
// Source extraction rather than import, for the reason wifi_scan.test.mjs
// gives: the dashboard compiles to a single classic script with no module
// boundary, so the alternative is a second copy that drifts.
//
// The distinction under test is between pm REFUSING a call and pm ANSWERING
// that the package is not installed. Counting only successes made those two
// identical, so a device whose image genuinely lacks the Alexa packages was
// told the package manager had rejected every call and to wait longer and
// retry, which can never help — and the wizard could never be completed (#91).

import { readFileSync } from "fs";
import { fileURLToPath } from "url";
import { dirname, join } from "path";

const HERE = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(HERE, "..", "static", "dashboard.jsx"), "utf8");

// Handles both arrow forms in this file: `= (x) => { ... };` with a body, and
// `= (x) => expr || expr;` without one. Scanning for a matching brace only
// works for the first, and silently swallows the following declaration for the
// second, which is how this test first failed with "already declared".
function liftArrow(name) {
  const start = src.indexOf(`const ${name} = (`);
  if (start < 0) {
    throw new Error(`dashboard.jsx no longer defines ${name}() — if it was `
                  + `renamed or moved, update this test to match`);
  }
  let depth = 0;
  for (let i = start; i < src.length; i++) {
    const ch = src[i];
    if (ch === "{" || ch === "(" || ch === "[") depth++;
    else if (ch === "}" || ch === ")" || ch === "]") depth--;
    else if (ch === ";" && depth === 0) return src.slice(start, i + 1);
  }
  throw new Error(`could not find the end of ${name}`);
}

const { _pmVerdict, _pmNotReady } = await import(
  "data:text/javascript;base64," + Buffer.from(
    liftArrow("_pmVerdict") + "\n" + liftArrow("_pmNotReady")
    + "\nexport { _pmVerdict, _pmNotReady };"
  ).toString("base64"));

let failures = 0;
function check(name, cond, detail) {
  if (cond) return;
  failures++;
  console.error(`FAIL: ${name}${detail ? `\n      ${detail}` : ""}`);
}

// Real output, from a successful run on 2026-08-08.
check("a disabled package is a success",
  _pmVerdict("Package amazon.speech.sim new state: disabled") === "disabled");
check("a hidden package is a success",
  _pmVerdict("Package com.amazon.whad new hidden state: true") === "disabled");

// Real output from #91. pm answered; the package is not installed.
const UNKNOWN =
  "Error: java.lang.IllegalArgumentException: Unknown package: com.amazon.echo.csm.oobe";
check("an unknown package is absent, not a rejection",
  _pmVerdict(UNKNOWN) === "absent", _pmVerdict(UNKNOWN));

// The two shapes of pm not being ready. Both must count as rejections, since
// waiting and retrying IS the right advice for them.
for (const out of [
  "Error: Could not access the Package Manager. Is the system running?",
  "java.lang.NullPointerException: Attempt to invoke interface method "
  + "'int java.util.ArrayList.size()' on a null object reference",
]) {
  check(`not-ready is a rejection: ${out.slice(0, 40)}`,
        _pmVerdict(out) === "rejected", _pmVerdict(out));
  check(`_pmNotReady still matches: ${out.slice(0, 40)}`, _pmNotReady(out));
}

// Anything unrecognised is treated as the BAD case on purpose: the cost of
// being wrong is continuing to WiFi with the Alexa stack live.
for (const out of ["", "Killed", "segfault", "Permission denied"]) {
  check(`unrecognised output is a rejection: ${out || "(empty)"}`,
        _pmVerdict(out) === "rejected", _pmVerdict(out));
}

// An absent package must not be mistaken for not-ready, or the retry path
// fires eleven times against a device that answered correctly every time.
check("absent is not confused with not-ready", !_pmNotReady(UNKNOWN));

if (failures) {
  console.error(`\n${failures} check(s) failed.`);
  process.exit(1);
}
console.log("pm_verdict: all checks passed.");
