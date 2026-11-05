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
  bindingIdentityIsEligible,
  bindingCardKey,
  buildInventoryBindingCards,
  buildLiveBindingCards,
  buildStoredBindingCards,
  createRequestGuard,
  failedInventorySnapshot,
  inventoryReadPlan,
  mergeBindingCards,
  proxyBindingReconnectOutcome,
  reconcileInventoryBindingCards,
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

test("keeps binding operations available when VoWiFi is offline with a known ICCID", () => {
  const offlineReader = {
    ...device("reader-a", "8944100000000000001", "Reader A"),
    physicalPresent: true,
    controlOnline: false,
    vowifiActive: false,
    vowifiRuntime: { iccid: "8944100000000000001", tunnelReady: false },
  };

  const [row] = buildLiveBindingCards([offlineReader], labels);

  assert.equal(row.online, false);
  assert.equal(row.bindingEligible, true);
  assert.equal(row.physicalPresent, true);
  assert.equal(row.controlOnline, false);
  assert.equal(row.profileKnown, true);
  assert.equal(row.vowifiReady, false);
});

test("stored bindings remain editable when their device is unavailable", () => {
  const [row] = buildStoredBindingCards([
    {
      id: "binding:reader-a:8988100000000000002",
      deviceId: "reader-a",
      iccid: "8988100000000000002",
      profileName: "Travel",
      routes: [],
    },
  ], []);

  assert.equal(row.online, false);
  assert.equal(row.bindingEligible, true);
  assert.equal(row.profileKnown, true);
});

test("cached profiles are bindable offline while devices without an ICCID stay read-only", () => {
  const offlineReader = {
    ...device("reader-a", "8944100000000000001"),
    controlOnline: false,
  };
  const overview = {
    profiles: [{
      profiles: [{ iccid: "8944100000000000002", name: "Travel" }],
    }],
  };

  const [cached] = buildInventoryBindingCards(overview, offlineReader, labels);
  const [withoutICCID] = buildLiveBindingCards([
    {
      ...device("reader-b", ""),
      controlOnline: false,
      vowifiRuntime: { iccid: "" },
    },
  ], labels);

  assert.equal(cached.online, false);
  assert.equal(cached.bindingEligible, true);
  assert.equal(withoutICCID, undefined);
});

test("rejects malformed ICCIDs for new binding operations", () => {
  const invalidReader = device("reader-a", "not-an-iccid");
  const [row] = buildInventoryBindingCards({
    profiles: [{ profiles: [{ iccid: "not-an-iccid", name: "Invalid" }] }],
  }, invalidReader, labels);

  assert.equal(row.bindingEligible, false);
  assert.equal(row.profileKnown, true);
});

test("matches the backend device ID rules before enabling binding operations", () => {
  const iccid = "8944100000000000001";

  assert.equal(bindingIdentityIsEligible("Reader-48EF22823536", iccid), true);
  assert.equal(bindingIdentityIsEligible("reader_01.test", iccid), true);
  assert.equal(bindingIdentityIsEligible("-reader", iccid), false);
  assert.equal(bindingIdentityIsEligible("reader space", iccid), false);
  assert.equal(bindingIdentityIsEligible("a".repeat(64), iccid), true);
  assert.equal(bindingIdentityIsEligible("a".repeat(65), iccid), false);
});

test("merge preserves binding eligibility independently from online state", () => {
  const onlineReader = device("reader-a", "8944100000000000001");
  const offlineReader = {
    ...device("reader-a", "8944100000000000002"),
    controlOnline: false,
  };
  const overview = {
    profiles: [{
      profiles: [
        { iccid: "8944100000000000001", name: "Online profile" },
        { iccid: "8944100000000000002", name: "Offline profile" },
      ],
    }],
  };

  const rows = mergeBindingCards(
    buildLiveBindingCards([onlineReader, offlineReader], labels),
    [],
    buildInventoryBindingCards(overview, offlineReader, labels),
  );

  assert.ok(rows.every((row) => row.bindingEligible));
  assert.equal(rows.find((row) => row.iccid === "8944100000000000002")?.online, false);
});

test("current offline state overrides an online profile cache during merge", () => {
  const iccid = "8944100000000000001";
  const onlineReader = device("reader-a", iccid, "Reader A");
  const offlineReader = {
    ...device("reader-a", iccid, "Reader A"),
    physicalPresent: true,
    controlOnline: false,
    vowifiActive: false,
    vowifiRuntime: { iccid, tunnelReady: false },
  };
  const overview = {
    profiles: [{
      profiles: [{ iccid, name: "Cached profile" }],
    }],
  };

  const [row] = mergeBindingCards(
    buildLiveBindingCards([offlineReader], labels),
    [],
    buildInventoryBindingCards(overview, onlineReader, labels),
  );

  assert.equal(row.bindingEligible, true);
  assert.equal(row.online, false);
  assert.equal(row.physicalPresent, true);
  assert.equal(row.controlOnline, false);
  assert.equal(row.vowifiReady, false);
});

test("reconciles stale cache status when the live ICCID is no longer available", () => {
  const onlineReader = device("reader-a", "8944100000000000001", "Reader A");
  const offlineReader = {
    ...device("reader-a", "", "Reader A"),
    physicalPresent: true,
    controlOnline: false,
    vowifiActive: false,
    vowifiRuntime: { iccid: "", tunnelReady: true },
  };
  const overview = {
    profiles: [{
      profiles: [{ iccid: "8944100000000000001", name: "Cached profile" }],
    }],
  };
  const cached = buildInventoryBindingCards(overview, onlineReader, labels);

  const [row] = reconcileInventoryBindingCards(cached, [offlineReader]);

  assert.equal(row.bindingEligible, true);
  assert.equal(row.online, false);
  assert.equal(row.physicalPresent, true);
  assert.equal(row.controlOnline, false);
  assert.equal(row.vowifiReady, false);
});

test("reconciling a removed device disables cached profile binding", () => {
  const reader = device("reader-a", "8944100000000000001", "Reader A");
  const overview = {
    profiles: [{
      profiles: [{ iccid: "8944100000000000001", name: "Cached profile" }],
    }],
  };
  const cached = buildInventoryBindingCards(overview, reader, labels);

  const [row] = reconcileInventoryBindingCards(cached, []);

  assert.equal(row.bindingEligible, false);
  assert.equal(row.online, false);
  assert.equal(row.physicalPresent, false);
});

test("explicit inactive VoWiFi state overrides a stale tunnel-ready runtime", () => {
  const reader = {
    ...device("reader-a", "8944100000000000001"),
    vowifiActive: false,
    vowifiRuntime: { iccid: "8944100000000000001", tunnelReady: true },
  };

  const [row] = buildLiveBindingCards([reader], labels);

  assert.equal(row.vowifiReady, false);
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

test("inventory restores uncached offline profiles without forcing live hardware reads", () => {
  const devices = [
    device("reader-a", "8944100000000000001"),
    device("reader-b", "8944100000000000002"),
    { ...device("reader-off", "8944100000000000003"), controlOnline: false },
  ];

  assert.deepEqual(
    inventoryReadPlan(["reader-a"], devices).map((item) => item.id),
    ["reader-b", "reader-off"],
  );
  assert.deepEqual(
    inventoryReadPlan(["reader-a", "reader-b"], devices, true).map((item) => item.id),
    ["reader-a", "reader-b", "reader-off"],
  );
});

test("inventory refreshes stale cache only after its device is online again", () => {
  const onlineReader = device("reader-online", "8944100000000000001");
  const offlineReader = {
    ...device("reader-offline", "8944100000000000002"),
    controlOnline: false,
  };

  assert.deepEqual(
    inventoryReadPlan(
      ["reader-online", "reader-offline"],
      [onlineReader, offlineReader],
      false,
      ["reader-online", "reader-offline"],
    ).map((item) => item.id),
    ["reader-online"],
  );
});

test("failed inventory refresh preserves stale state and cached cards", () => {
  const onlineReader = device("reader-a", "8944100000000000001");
  const previous = {
    cards: [{ key: "cached-card" }],
    fetchedAt: 1000,
    stale: true,
  };

  assert.deepEqual(
    failedInventorySnapshot(previous, onlineReader, "read failed", 2000),
    {
      cards: previous.cards,
      fetchedAt: 1000,
      stale: true,
      error: "read failed",
    },
  );

  const offlineReader = { ...onlineReader, controlOnline: false };
  assert.equal(failedInventorySnapshot(undefined, offlineReader, "read failed", 2000).stale, true);
  assert.equal(failedInventorySnapshot(undefined, onlineReader, "read failed", 2000).stale, false);
  const missingReader = { ...onlineReader, physicalPresent: false };
  assert.equal(failedInventorySnapshot(undefined, missingReader, "read failed", 2000).stale, true);
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

test("classifies proxy binding reconnect feedback without treating deferred work as failure", () => {
  assert.equal(proxyBindingReconnectOutcome({ reconnectRequested: true }), "requested");
  assert.equal(proxyBindingReconnectOutcome({}), "deferred");
  assert.equal(proxyBindingReconnectOutcome({ reconnectRequested: false }), "deferred");
  assert.equal(
    proxyBindingReconnectOutcome({ reconnectRequested: true, reconnectError: "timeout" }),
    "failed",
  );
});
