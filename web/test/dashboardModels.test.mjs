import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/components/dashboard/dashboardModels.ts", import.meta.url), "utf8");
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 },
}).outputText;
const moduleURL = `data:text/javascript;base64,${Buffer.from(compiled).toString("base64")}`;
const { buildDashboardCardViews, moduleOnlineRate } = await import(moduleURL);

const devices = [
  { id: "reader-1", healthy: true, deviceType: "usb_sim_reader", vowifiActive: true, networkConnected: false },
  { id: "ec20-1", healthy: true, deviceType: "pcie_ec20_ec25", vowifiActive: false, networkConnected: true },
  { id: "offline-1", healthy: false, deviceType: "ml307", vowifiActive: false, networkConnected: false },
];

test("module online rate is calculated from devices only", () => {
  assert.deepEqual(moduleOnlineRate(devices), { online: 2, total: 3 });
});

test("dashboard cards separate VoWiFi from cellular data", () => {
  const views = buildDashboardCardViews([
    {
      iccid: "8944300000000000001",
      status: "active",
      cardType: "physical_sim",
      binding: { deviceId: "reader-1", online: true },
      policy: { vowifiEnabled: true, airplaneEnabled: true, runtimeMode: "pcsc" },
    },
    {
      iccid: "8944300000000000002",
      status: "active",
      cardType: "physical_sim",
      binding: { deviceId: "ec20-1", online: true },
      policy: { vowifiEnabled: false, networkEnabled: true },
    },
    {
      iccid: "8944300000000000003",
      status: "active",
      cardType: "physical_sim",
      binding: { deviceId: "offline-1", online: true },
      policy: { vowifiEnabled: true },
    },
  ], devices);

  assert.equal(views[0].service, "vowifi");
  assert.equal(views[0].online, true);
  assert.equal(views[1].service, "cellular_data");
  assert.equal(views[1].online, true);
  assert.equal(views[2].service, "vowifi");
  assert.equal(views[2].online, false);
  assert.deepEqual(moduleOnlineRate(devices), { online: 2, total: 3 });
});
