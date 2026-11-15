import type { DashboardDevice, SimCard } from "../../types";

export type DashboardCardService = "vowifi" | "cellular_data" | "none";

export interface DashboardCardView {
  card: SimCard;
  device?: DashboardDevice;
  online: boolean;
  service: DashboardCardService;
}

export function moduleOnlineRate(devices: DashboardDevice[]): { online: number; total: number } {
  return {
    online: devices.filter((device) => device.healthy).length,
    total: devices.length,
  };
}

export function buildDashboardCardViews(
  cards: SimCard[],
  devices: DashboardDevice[],
): DashboardCardView[] {
  const deviceById = new Map(devices.map((device) => [device.id, device]));
  return cards.map((card) => {
    const deviceID = card.binding?.deviceId?.trim() || "";
    const device = deviceById.get(deviceID);
    const online = (
      card.status === "active" &&
      !card.conflict &&
      !!deviceID &&
      card.binding?.online !== false &&
      !!device?.healthy
    );
    const wifiCallingOnly =
      card.hostType === "usb_sim_reader" ||
      card.binding?.deviceType === "usb_sim_reader" ||
      card.binding?.backendMode === "pcsc" ||
      card.policy?.runtimeMode === "pcsc" ||
      device?.deviceType === "usb_sim_reader";
    const usesVoWiFi =
      wifiCallingOnly ||
      card.policy?.vowifiEnabled === true ||
      device?.vowifiActive === true;
    const usesCellularData =
      card.policy?.networkEnabled === true ||
      device?.networkConnected === true;
    const service: DashboardCardService = usesVoWiFi
      ? "vowifi"
      : usesCellularData
        ? "cellular_data"
        : "none";
    return { card, device, online, service };
  });
}
