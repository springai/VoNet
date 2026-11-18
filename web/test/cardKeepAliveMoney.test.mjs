import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/components/cards/cardKeepAliveMoney.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 },
}).outputText;
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled).toString("base64")}`;
const { centsToMoney, moneyDisplay, moneyToCents, sanitizeMoneyInput } = await import(moduleURL);

test("money inputs accept valid decimal values while editing", () => {
  assert.equal(sanitizeMoneyInput("1.26"), "1.26");
  assert.equal(sanitizeMoneyInput("1."), "1.");
  assert.equal(sanitizeMoneyInput("1.2"), "1.2");
  assert.equal(sanitizeMoneyInput("001.26"), "1.26");
  assert.equal(sanitizeMoneyInput(".26"), "0.26");
  assert.equal(moneyToCents("1.26"), 126);
  assert.equal(moneyToCents("1."), 100);
  assert.equal(centsToMoney(126), "1.26");
  assert.equal(moneyDisplay(1000), "10");
  assert.equal(moneyDisplay(1), "0.01");
});

test("money inputs reject misplaced punctuation and excess precision", () => {
  assert.equal(sanitizeMoneyInput("1.2.3"), "1.23");
  assert.equal(sanitizeMoneyInput("1.234"), "1.23");
  assert.equal(sanitizeMoneyInput("12,34.5"), "1234.5");
  assert.equal(sanitizeMoneyInput("abc"), "");
});
