import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/components/cards/cardTransfer.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 },
}).outputText;
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled).toString("base64")}`;
const { buildCardTransfer, cardTransferFilename, parseCardTransfer } = await import(moduleURL);

const cards = [{
  iccid: "894430 0000 0000 00001",
  displayName: "UK2",
  cardType: "esim_profile",
  notes: "Voice and SMS",
  status: "active",
  binding: { deviceId: "Reader-1" },
}];

test("card transfer exports only editable card configuration", () => {
  assert.deepEqual(buildCardTransfer(cards), {
    cards: [{
      iccid: "8944300000000000001",
      displayName: "UK2",
      cardType: "esim_profile",
      notes: "Voice and SMS",
    }],
  });
  assert.match(cardTransferFilename(new Date("2026-11-15T00:00:00Z")), /^vonet-cards-2026-11-15\.json$/);
});

test("card transfer imports normalized cards and rejects duplicate ICCIDs", () => {
  assert.deepEqual(parseCardTransfer({
    cards: [{
      iccid: "894430 0000 0000 00001",
      displayName: " UK2 ",
      cardType: "physical_sim",
      notes: " Updated ",
    }],
  }), [{
    iccid: "8944300000000000001",
    displayName: "UK2",
    cardType: "physical_sim",
    notes: "Updated",
  }]);

  assert.throws(() => parseCardTransfer({
    cards: [
      { iccid: "8944300000000000001", cardType: "physical_sim" },
      { iccid: "894430 0000 0000 00001", cardType: "physical_sim" },
    ],
  }), /duplicate ICCID/);
  assert.throws(() => parseCardTransfer({ cards: [{ iccid: "123", cardType: "physical_sim" }] }), /invalid ICCID/);
});
