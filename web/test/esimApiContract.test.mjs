import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

async function loadTS(path, exports) {
  const source = await readFile(new URL(path, import.meta.url), "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ES2022,
      target: ts.ScriptTarget.ES2022,
    },
  });
  const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled.outputText).toString("base64")}`;
  return import(moduleURL).then((module) => Object.fromEntries(exports.map((name) => [name, module[name]])));
}

const {
  esimDownloadDisabled,
  esimMutationDisabled,
  createEsimMutationGuard,
} = await loadTS("../src/components/devices/esimActionState.ts", [
  "esimDownloadDisabled",
  "esimMutationDisabled",
  "createEsimMutationGuard",
]);
const { camelize, camelizeKey, selectAPIPayload } = await loadTS("../src/lib/apiPayload.ts", ["camelize", "camelizeKey", "selectAPIPayload"]);

test("cached or offline eSIM profiles disable mutating actions", () => {
  assert.equal(esimMutationDisabled({ deviceOnline: true, profilesStale: true }), true);
  assert.equal(esimMutationDisabled({ deviceOnline: false, profilesStale: false }), true);
  assert.equal(esimMutationDisabled({ deviceOnline: true, profilesStale: false }), false);
  assert.equal(esimDownloadDisabled({ deviceOnline: true, profilesStale: true }, false), true);
  assert.equal(esimDownloadDisabled({ deviceOnline: true, profilesStale: false }, true), true);
  assert.equal(esimDownloadDisabled({ deviceOnline: true, profilesStale: false }, false), false);
  let state = { deviceOnline: true, profilesStale: false };
  const guard = createEsimMutationGuard(() => state);
  assert.equal(guard(), false);
  state = { deviceOnline: true, profilesStale: true };
  assert.equal(guard(), true);
});

test("api envelope keeps outer cache metadata while normal api unwraps data", () => {
  const normalized = {
    data: [{ profiles: [] }],
    stale: true,
    cachedAt: 123,
  };

  assert.deepEqual(selectAPIPayload(normalized), normalized.data);
  assert.deepEqual(selectAPIPayload(normalized, false), normalized);
  assert.equal(selectAPIPayload(normalized, false).cachedAt, 123);
  assert.equal(camelizeKey("cached_at"), "cachedAt");
  assert.deepEqual(camelize({
    data: [{ cached_at: 123, nested_key: { inner_value: true } }],
  }), {
    data: [{ cachedAt: 123, nestedKey: { innerValue: true } }],
  });
});
