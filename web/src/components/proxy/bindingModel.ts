import type { DeviceListItem, EsimOverview } from "../../types";
import type { ProxyBindingGroup } from "../../proxy_types";

export type BindingCardSource = "live" | "binding" | "inventory";

export interface BindingCard {
  key: string;
  deviceId: string;
  deviceName: string;
  iccid: string;
  profileName: string;
  stateText?: string;
  current: boolean;
  online: boolean;
  source: BindingCardSource;
}

export interface InventorySnapshot {
  cards: BindingCard[];
  fetchedAt: number;
  error?: string;
}

export interface BindingLabels {
  currentCard: string;
  currentInUse: string;
}

export function bindingCardKey(deviceId: string, iccid: string) {
  return `${deviceId}:${iccid}`;
}

export function deviceIsOnline(device: Pick<DeviceListItem, "physicalPresent" | "controlOnline">) {
  return device.physicalPresent !== false && device.controlOnline;
}

export function currentDeviceICCID(device: DeviceListItem) {
  return String(device.modem?.iccid || device.vowifiRuntime?.iccid || "").trim();
}

export function profileLabel(profile: { name?: string; serviceProviderName?: string; iccid: string }) {
  return String(profile.name || profile.serviceProviderName || profile.iccid).trim();
}

export function buildLiveBindingCards(devices: DeviceListItem[], labels: BindingLabels): BindingCard[] {
  const cards: BindingCard[] = [];
  for (const device of devices) {
    const iccid = currentDeviceICCID(device);
    if (!iccid) continue;
    cards.push({
      key: bindingCardKey(device.id, iccid),
      deviceId: device.id,
      deviceName: device.name || device.id,
      iccid,
      profileName: labels.currentCard,
      stateText: labels.currentInUse,
      current: true,
      online: deviceIsOnline(device),
      source: "live",
    });
  }
  return cards;
}

export function buildStoredBindingCards(groups: ProxyBindingGroup[], devices: DeviceListItem[]): BindingCard[] {
  const deviceById = new Map(devices.map((device) => [device.id, device]));
  return groups.map((group) => {
    const device = deviceById.get(group.deviceId);
    const liveICCID = device ? currentDeviceICCID(device) : "";
    return {
      key: bindingCardKey(group.deviceId, group.iccid),
      deviceId: group.deviceId,
      deviceName: device?.name || group.deviceId,
      iccid: group.iccid,
      profileName: group.profileName || group.iccid,
      current: !!liveICCID && liveICCID === group.iccid,
      online: !!device && deviceIsOnline(device),
      source: "binding" as const,
    };
  });
}

export function buildInventoryBindingCards(
  overview: EsimOverview,
  device: DeviceListItem,
  labels: BindingLabels,
): BindingCard[] {
  const liveICCID = currentDeviceICCID(device);
  const cards: BindingCard[] = [];
  for (const group of overview.profiles || []) {
    for (const profile of group.profiles || []) {
      const iccid = String(profile.iccid || "").trim();
      if (!iccid) continue;
      cards.push({
        key: bindingCardKey(device.id, iccid),
        deviceId: device.id,
        deviceName: device.name || device.id,
        iccid,
        profileName: profileLabel(profile),
        stateText: profile.stateText,
        current: !!liveICCID && liveICCID === iccid,
        online: deviceIsOnline(device),
        source: "inventory",
      });
    }
  }
  if (liveICCID && !cards.some((card) => card.iccid === liveICCID)) {
    cards.push({
      key: bindingCardKey(device.id, liveICCID),
      deviceId: device.id,
      deviceName: device.name || device.id,
      iccid: liveICCID,
      profileName: labels.currentCard,
      stateText: labels.currentInUse,
      current: true,
      online: deviceIsOnline(device),
      source: "inventory",
    });
  }
  return cards;
}

function mergeCard(current: BindingCard | undefined, next: BindingCard): BindingCard {
  if (!current) return next;
  const sourceRank: Record<BindingCardSource, number> = { live: 0, binding: 1, inventory: 2 };
  const preferNextName = sourceRank[next.source] >= sourceRank[current.source] && !!next.profileName;
  return {
    ...current,
    profileName: preferNextName && next.profileName ? next.profileName : current.profileName || next.profileName,
    stateText: next.stateText || current.stateText,
    current: current.current || next.current,
    online: current.online || next.online,
    source: sourceRank[next.source] > sourceRank[current.source] ? next.source : current.source,
  };
}

export function mergeBindingCards(
  liveCards: BindingCard[],
  storedCards: BindingCard[],
  inventoryCards: BindingCard[],
): BindingCard[] {
  const currentKeys = new Set(liveCards.filter((card) => card.current).map((card) => card.key));
  const merged = new Map<string, BindingCard>();
  for (const card of [...liveCards, ...storedCards, ...inventoryCards]) {
    merged.set(card.key, mergeCard(merged.get(card.key), card));
  }
  return Array.from(merged.values()).map((card) => ({ ...card, current: currentKeys.has(card.key) })).sort((a, b) =>
    a.deviceName.localeCompare(b.deviceName) ||
    a.profileName.localeCompare(b.profileName) ||
    a.iccid.localeCompare(b.iccid),
  );
}

export function inventoryReadPlan(
  cachedDeviceIds: Iterable<string>,
  devices: DeviceListItem[],
  force = false,
): DeviceListItem[] {
  const cached = new Set(cachedDeviceIds);
  return devices.filter((device) => deviceIsOnline(device) && (force || !cached.has(device.id)));
}

export function selectionAfterRefresh(cards: BindingCard[], selectedKey: string) {
  return cards.some((card) => card.key === selectedKey) ? selectedKey : "";
}

export interface RequestGuard {
  begin: () => number;
  isCurrent: (token: number) => boolean;
  invalidate: () => void;
}

export function createRequestGuard(): RequestGuard {
  let current = 0;
  return {
    begin: () => ++current,
    isCurrent: (token) => token === current,
    invalidate: () => {
      current += 1;
    },
  };
}
