import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/components/proxy/bindingModel.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ES2022,
    target: ts.ScriptTarget.ES2022,
  },
});
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled.outputText).toString("base64")}`;
const {
  bindingCardKey,
  buildInventoryBindingCards,
  buildLiveBindingCards,
  buildStoredBindingCards,
  createRequestGuard,
  inventoryReadPlan,
  mergeBindingCards,
  selectionAfterRefresh,
} = await import(moduleURL);

const labels = { currentCard: "当前 SIM 卡", currentInUse: "当前使用中" };

function device(id, iccid, name = id) {
  return {
    id,
    name,
    physicalPresent: true,
    controlOnline: true,
    modem: { iccid },
    vowifiRuntime: { iccid: "" },
  };
}

test("shows stored bindings globally without selecting a device first", () => {
  const devices = [device("reader-a", "8944100000000000001", "Reader A")];
  const groups = [
    {
      id: "binding:reader-a:8988100000000000002",
      deviceId: "reader-a",
      iccid: "8988100000000000002",
      profileName: "Travel",
      routes: [],
    },
  ];

  const rows = mergeBindingCards(
    buildLiveBindingCards(devices, labels),
    buildStoredBindingCards(groups, devices),
    [],
  );

  assert.equal(rows.length, 2);
  assert.ok(rows.some((row) => row.iccid === "8988100000000000002" && row.profileName === "Travel"));
});

test("live ICCID changes correct current flags in cached profile rows", () => {
  const oldCurrent = device("reader-a", "8944100000000000001", "Reader A");
  const newCurrent = device("reader-a", "8944100000000000002", "Reader A");
  const overview = {
    profiles: [{
      profiles: [
        { iccid: "8944100000000000001", name: "Old profile" },
        { iccid: "8944100000000000002", name: "New profile" },
      ],
    }],
  };

  const cachedCards = buildInventoryBindingCards(overview, oldCurrent, labels);
  const rows = mergeBindingCards(buildLiveBindingCards([newCurrent], labels), [], cachedCards);

  assert.equal(rows.find((row) => row.iccid === "8944100000000000001")?.current, false);
  assert.equal(rows.find((row) => row.iccid === "8944100000000000002")?.current, true);
});

test("cached profile inventory replaces the generic live SIM label", () => {
  const reader = device("reader-a", "8944100000000000001", "Reader A");
  const overview = {
    profiles: [{
      profiles: [{
        iccid: "8944100000000000001",
        name: "One NZ",
        serviceProviderName: "One NZ",
        stateText: "已启用",
      }],
    }],
  };

  const rows = mergeBindingCards(
    buildLiveBindingCards([reader], labels),
    [],
    buildInventoryBindingCards(overview, reader, labels),
  );

  assert.equal(rows.length, 1);
  assert.equal(rows[0].profileName, "One NZ");
  assert.equal(rows[0].current, true);
});

test("inventory is read only for uncached online devices until forced", () => {
  const devices = [
    device("reader-a", "8944100000000000001"),
    device("reader-b", "8944100000000000002"),
    { ...device("reader-off", "8944100000000000003"), controlOnline: false },
  ];

  assert.deepEqual(inventoryReadPlan(["reader-a"], devices).map((item) => item.id), ["reader-b"]);
  assert.deepEqual(
    inventoryReadPlan(["reader-a", "reader-b"], devices, true).map((item) => item.id),
    ["reader-a", "reader-b"],
  );
});

test("selection survives refresh only while its card remains present", () => {
  const cards = [
    { key: bindingCardKey("reader-a", "8944100000000000001") },
    { key: bindingCardKey("reader-a", "8944100000000000002") },
  ];

  assert.equal(selectionAfterRefresh(cards, bindingCardKey("reader-a", "8944100000000000002")), cards[1].key);
  assert.equal(selectionAfterRefresh(cards.slice(0, 1), cards[1].key), "");
});

test("only the latest asynchronous inventory response may update state", () => {
  const guard = createRequestGuard();
  const first = guard.begin();
  const second = guard.begin();

  assert.equal(guard.isCurrent(first), false);
  assert.equal(guard.isCurrent(second), true);
  guard.invalidate();
  assert.equal(guard.isCurrent(second), false);
});
