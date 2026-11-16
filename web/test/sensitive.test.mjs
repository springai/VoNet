import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/lib/sensitive.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 },
}).outputText;
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled).toString("base64")}`;
const { displayICCID, displaySecret, maskICCID, maskSecret } = await import(moduleURL);

test("ICCID is partially masked by default", () => {
  const iccid = "8942001184016744";
  const masked = maskICCID(iccid);

  assert.equal(masked, "894200******6744");
  assert.equal(displayICCID(iccid, false), masked);
  assert.notEqual(masked, iccid);
});

test("sensitive display reveals only the normalized ICCID", () => {
  assert.equal(displayICCID("8942 0011 8401 6744", true), "8942001184016744");
  assert.equal(displayICCID("", true), "--");
});

test("proxy secrets are fully hidden by default", () => {
  assert.equal(maskSecret("proxy-user"), "********");
  assert.equal(maskSecret(" "), "--");
  assert.equal(displaySecret("127.0.0.1:1080", false), "********");
  assert.equal(displaySecret("127.0.0.1:1080", true), "127.0.0.1:1080");
});
