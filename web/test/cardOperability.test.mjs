import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/components/cards/cardApi.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
const exports = {};
new Function("require", "exports", compiled)((name) => {
  assert.equal(name, "../../api");
  return { api: async () => ({}) };
}, exports);

test("an active card remains operable when binding lifecycle flags lag behind", () => {
  const card = {
    status: "active",
    binding: {
      deviceId: "Reader-1",
      online: false,
      running: false,
    },
  };

  assert.equal(exports.activeCardDevice(card), "Reader-1");
  assert.equal(exports.isCardOperable(card), true);
});

test("offline and conflicting cards remain non-operable", () => {
  assert.equal(exports.isCardOperable({
    status: "offline",
    binding: { deviceId: "Reader-1", online: true, running: true },
  }), false);
  assert.equal(exports.isCardOperable({
    status: "active",
    conflict: true,
    binding: { deviceId: "Reader-1", online: true, running: true },
  }), false);
});

test("USSD uses the same operability source as the card online state", async () => {
  const source = await readFile(new URL("../src/pages/CardsPage.tsx", import.meta.url), "utf8");

  assert.match(source, /operable=\{selectedDeviceOnline\}/);
  assert.doesNotMatch(source, /operable=\{[\s\S]*binding\?\.online/);
});
