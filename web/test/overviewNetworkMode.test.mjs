import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/components/devices/shared.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

const modules = {
  react: { useSyncExternalStore: () => false },
  "../ui": { message: {} },
  "../../lib/i18n": { tl: (value) => value },
  "../../lib/carrier": { lookupCarrier: () => undefined },
  "../../api": { notifyUnauthorized: () => {} },
};
const exports = {};
new Function("require", "exports", compiled)((name) => {
  assert.ok(name in modules, `unexpected dependency ${name}`);
  return modules[name];
}, exports);

test("USB SIM readers keep the VoWiFi overview while the runtime is starting", () => {
  const startingReader = {
    vowifiEnabled: false,
    vowifiRuntime: { enabled: false },
  };

  assert.equal(exports.isVoWiFiInUse(startingReader, true), true);
});

test("cellular hosts still honor an explicitly disabled VoWiFi runtime", () => {
  assert.equal(exports.isVoWiFiInUse({
    vowifiEnabled: true,
    vowifiRuntime: { enabled: false },
  }), false);
  assert.equal(exports.isVoWiFiInUse({
    vowifiEnabled: true,
    vowifiRuntime: { enabled: true },
  }), true);
});
