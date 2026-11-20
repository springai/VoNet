import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/lib/naturalSort.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 },
}).outputText;
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled).toString("base64")}`;
const { compareNaturalCards, compareNaturalDevices, compareNaturalText } = await import(moduleURL);

test("natural text sorting puts letters first and compares number groups by value", () => {
  const values = [
    "10-GG",
    "2-ONE",
    "Tmobile-1-3539",
    "ASDA-10-1",
    "UK-1-O2",
    "ASDA-2-10",
    "GG-2-2200",
    "ASDA-2-2",
    "GG-1-0339",
  ];
  assert.deepEqual([...values].sort(compareNaturalText), [
    "ASDA-2-2",
    "ASDA-2-10",
    "ASDA-10-1",
    "GG-1-0339",
    "GG-2-2200",
    "Tmobile-1-3539",
    "UK-1-O2",
    "2-ONE",
    "10-GG",
  ]);
});

test("natural card sorting uses card name, then carrier and ICCID", () => {
  const values = [
    { displayName: "Travel-10", carrier: "B", iccid: "2" },
    { displayName: "Travel-2", carrier: "C", iccid: "3" },
    { displayName: "Travel-2", carrier: "A", iccid: "1" },
    { displayName: "", carrier: "Roaming", iccid: "9" },
  ];
  assert.deepEqual([...values].sort(compareNaturalCards), [
    { displayName: "", carrier: "Roaming", iccid: "9" },
    { displayName: "Travel-2", carrier: "A", iccid: "1" },
    { displayName: "Travel-2", carrier: "C", iccid: "3" },
    { displayName: "Travel-10", carrier: "B", iccid: "2" },
  ]);
});

test("natural device sorting uses device name and falls back to device ID", () => {
  const values = [
    { id: "z", name: "Reader-10" },
    { id: "b", name: "Reader-2" },
    { id: "10", name: "Reader-2" },
    { id: "2", name: "Reader-2" },
  ];
  assert.deepEqual([...values].sort(compareNaturalDevices), [
    { id: "b", name: "Reader-2" },
    { id: "2", name: "Reader-2" },
    { id: "10", name: "Reader-2" },
    { id: "z", name: "Reader-10" },
  ]);
});
