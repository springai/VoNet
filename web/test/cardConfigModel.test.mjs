import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/components/cards/cardConfigModel.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 },
}).outputText;
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled).toString("base64")}`;
const { cardConfigDirty, cardConfigDraft, cardConfigPatch } = await import(moduleURL);

const card = {
  iccid: "8944300000000000001",
  displayName: "CTExcel",
  cardType: "physical_sim",
};

test("card configuration edits only the supported configuration fields", () => {
  const patch = cardConfigPatch({
    displayName: "  Travel eSIM  ",
    cardType: "esim_profile",
    notes: "  Data-only card  ",
  });

  assert.deepEqual(patch, {
    displayName: "Travel eSIM",
    cardType: "esim_profile",
    notes: "Data-only card",
  });
  assert.equal("iccid" in patch, false);
});

test("unchanged card configuration is not dirty", () => {
  assert.deepEqual(cardConfigDraft(card), {
    displayName: "CTExcel",
    cardType: "physical_sim",
    notes: "",
  });
  assert.equal(cardConfigDirty(card, cardConfigDraft(card)), false);
  assert.equal(cardConfigDirty(card, {
    displayName: "CTExcel",
    cardType: "esim_profile",
    notes: "",
  }), true);
  assert.equal(cardConfigDirty(card, {
    displayName: "CTExcel",
    cardType: "physical_sim",
    notes: "Voice line",
  }), true);
});
