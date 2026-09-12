// The wizard must be able to cross a device that is ALREADY in the fleet to
// emOS without deleting its controller entry.
//
//     node controller/tests/emos_migration.test.mjs
//
// Source guard rather than a unit test, for the reason the other dashboard
// tests give: the file compiles to one classic script with no module
// boundary, and the behaviour here lives in a React component's closure that
// cannot be lifted out of it. What can be pinned is the SHAPE of the rule,
// and the shape is what a refactor loses.
//
// Why this matters enough to guard. /data survives a boot-partition write, so
// a device crossing from FireOS to emOS keeps its Revoice install, its link
// credentials, its remembered controller and its WiFi configuration, and its
// serial — and therefore its device_id — does not change. Deleting the row
// first, which is what the refusal used to require, throws away the
// per-device config for nothing AND changes every Home Assistant entity id,
// because HA keys entities on the device's identity and a re-added device is
// a new one.
//
// The opposite mistake is the dangerous one: re-provisioning FireOS over
// FireOS rewrites the partitions the device is running from, and that is the
// destructive case the refusal exists for. So migration must be reachable
// ONLY from the emOS flow.

import { readFileSync } from "fs";
import { fileURLToPath } from "url";
import { dirname, join } from "path";
import assert from "assert";

const HERE = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(HERE, "..", "static", "dashboard.jsx"), "utf8");

// Comments quote the rule they explain, so a guard that reads the raw file
// matches its own justification rather than the code obeying it — this
// tree's recurring source-guard trap. Strip them first.
const code = src
  .replace(/\/\*[\s\S]*?\*\//g, "")
  .split("\n")
  .map((l) => l.replace(/(^|[^:])\/\/.*$/, "$1"))
  .join("\n");

let failures = 0;
function check(name, fn) {
  try {
    fn();
    console.log(`  ok  ${name}`);
  } catch (e) {
    failures++;
    console.log(`FAIL  ${name}\n      ${e.message}`);
  }
}

check("the duplicate-device refusal has a migration branch", () => {
  assert.ok(
    /if \(match && isEmos && migrating\)/.test(code),
    "the refusal no longer has an `isEmos && migrating` escape — a fielded " +
      "device can only be crossed to emOS by deleting it first, which loses " +
      "its config and every Home Assistant entity id"
  );
});

check("migration cannot be reached from the FireOS flow", () => {
  const i = code.indexOf("if (match && isEmos && migrating)");
  assert.ok(i > 0, "the migration branch is gone");
  const cond = code.slice(i, code.indexOf("{", i));
  assert.ok(
    cond.includes("isEmos"),
    "migration is no longer gated on the emOS flow — re-provisioning FireOS " +
      "over FireOS rewrites the partitions the device is running from, which " +
      "is exactly what the refusal exists to prevent"
  );
});

check("a device that is NOT being migrated is still refused", () => {
  assert.ok(
    /\} else if \(match\) \{/.test(code),
    "the plain `match` refusal is gone — an ordinary re-provision of a " +
      "registered device would now proceed and wipe through it"
  );
});

check("the offer is shown only on the emOS flow", () => {
  assert.ok(
    /step === 0 && duplicateDeviceId && isEmos &&/.test(code),
    "the Migrate button is not gated on isEmos, so it would be offered for " +
      "a FireOS re-provision as well"
  );
});

check("switching flows clears the intent to migrate", () => {
  const i = code.indexOf("function chooseFlow(");
  assert.ok(i > 0, "chooseFlow is gone");
  const body = code.slice(i, code.indexOf("\n  }", i));
  assert.ok(
    body.includes("setMigrating(false)"),
    "a migration intent expressed in the emOS flow survives a switch to the " +
      "FireOS flow, where it would let a stale click past the refusal"
  );
});

check("the migration branch does not delete the device", () => {
  const i = code.indexOf("if (match && isEmos && migrating)");
  const branch = code.slice(i, code.indexOf("} else if (match)", i));
  assert.ok(
    !/API\.del|DELETE/.test(branch),
    "the migration branch deletes something — keeping the row IS the migration"
  );
});

console.log(failures ? `\n${failures} failed` : "\nall ok");
process.exit(failures ? 1 : 0);
