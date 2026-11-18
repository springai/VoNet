import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/components/cards/cardConfigModel.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 },
}).outputText;
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled).toString("base64")}`;
const { cardConfigDirty, cardConfigDraft, cardConfigPatch, defaultKeepAliveConfig } = await import(moduleURL);

const keepalive = {
  ...defaultKeepAliveConfig(),
  planStartAt: 1767225600,
  currentCycleStartAt: 1767225600,
};

const card = {
  iccid: "8944300000000000001",
  displayName: "CTExcel",
  cardType: "physical_sim",
  msisdn: "+8613800138000",
  carrier: "CTExcel",
  keepalive,
};

test("card configuration edits only the supported configuration fields", () => {
  const patch = cardConfigPatch({
    displayName: "  Travel eSIM  ",
    cardType: "esim_profile",
    notes: "  Data-only card  ",
    phoneNumber: "  +447700900456  ",
    operatorName: "  EE  ",
    keepalive,
  });

  assert.deepEqual(patch, {
    displayName: "Travel eSIM",
    cardType: "esim_profile",
    notes: "Data-only card",
    msisdn: "+447700900456",
    carrier: "EE",
    keepalive,
  });
  assert.equal("iccid" in patch, false);
});

test("unchanged card configuration is not dirty", () => {
  assert.deepEqual(cardConfigDraft(card), {
    displayName: "CTExcel",
    cardType: "physical_sim",
    notes: "",
    phoneNumber: "+8613800138000",
    operatorName: "CTExcel",
    keepalive,
  });
  assert.equal(cardConfigDirty(card, cardConfigDraft(card)), false);
  assert.equal(cardConfigDirty(card, {
    displayName: "CTExcel",
    cardType: "esim_profile",
    notes: "",
    phoneNumber: "+8613800138000",
    operatorName: "CTExcel",
    keepalive,
  }), true);
  assert.equal(cardConfigDirty(card, {
    displayName: "CTExcel",
    cardType: "physical_sim",
    notes: "Voice line",
    phoneNumber: "+8613800138000",
    operatorName: "CTExcel",
    keepalive,
  }), true);
  assert.equal(cardConfigDirty(card, {
    displayName: "CTExcel",
    cardType: "physical_sim",
    notes: "",
    phoneNumber: "+8613800138001",
    operatorName: "CTExcel",
    keepalive,
  }), true);
  assert.equal(cardConfigDirty(card, {
    displayName: "CTExcel",
    cardType: "physical_sim",
    notes: "",
    phoneNumber: "+8613800138000",
    operatorName: "EE",
    keepalive,
  }), true);
  assert.equal(cardConfigDirty(card, {
    displayName: "CTExcel",
    cardType: "physical_sim",
    notes: "",
    phoneNumber: "+8613800138000",
    operatorName: "CTExcel",
    keepalive: { ...keepalive, autoRenew: true },
  }), true);
});
