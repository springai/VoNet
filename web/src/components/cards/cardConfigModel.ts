import type { CardKeepAliveConfig, SimCard, SimCardInput, SimCardType } from "../../types";

export function defaultKeepAliveConfig(): CardKeepAliveConfig {
  return {
    enabled: false,
    planInfo: "",
    currency: "CNY",
    balanceCents: 0,
    planStartAt: 0,
    currentCycleStartAt: 0,
    expiresAt: 0,
    cycleRule: "monthly",
    cycleDays: 30,
    deductionCents: 0,
    autoRenew: false,
    notificationEnabled: false,
    notifyDaysBefore: 3,
  };
}

function editableKeepAlive(value: CardKeepAliveConfig): CardKeepAliveConfig {
  return {
    enabled: value.enabled,
    planInfo: value.planInfo || "",
    currency: value.currency,
    balanceCents: value.balanceCents,
    planStartAt: value.planStartAt,
    currentCycleStartAt: value.currentCycleStartAt || value.planStartAt,
    expiresAt: value.expiresAt || 0,
    cycleRule: value.cycleRule,
    cycleDays: value.cycleDays,
    deductionCents: value.deductionCents,
    autoRenew: value.autoRenew,
    notificationEnabled: value.notificationEnabled,
    notifyDaysBefore: value.notifyDaysBefore,
  };
}

export interface CardConfigDraft {
  displayName: string;
  cardType: SimCardType;
  notes: string;
  phoneNumber: string;
  operatorName: string;
  keepalive: CardKeepAliveConfig;
}

export function cardConfigDraft(card: SimCard): CardConfigDraft {
  return {
    displayName: card.displayName || "",
    cardType: card.cardType,
    notes: card.notes || "",
    phoneNumber: card.msisdn || "",
    operatorName: card.carrier || "",
    keepalive: card.keepalive ? { ...card.keepalive } : defaultKeepAliveConfig(),
  };
}

export function cardConfigDirty(card: SimCard, draft: CardConfigDraft): boolean {
  return (
    draft.displayName.trim() !== (card.displayName || "").trim() ||
    draft.cardType !== card.cardType ||
    draft.notes.trim() !== (card.notes || "").trim() ||
    draft.phoneNumber.trim() !== (card.msisdn || "").trim() ||
    draft.operatorName.trim() !== (card.carrier || "").trim() ||
    JSON.stringify(editableKeepAlive(draft.keepalive)) !== JSON.stringify(editableKeepAlive(card.keepalive || defaultKeepAliveConfig()))
  );
}

export function cardConfigPatch(draft: CardConfigDraft): Pick<SimCardInput, "displayName" | "cardType" | "notes" | "msisdn" | "carrier" | "keepalive"> {
  return {
    displayName: draft.displayName.trim(),
    cardType: draft.cardType,
    notes: draft.notes.trim(),
    msisdn: draft.phoneNumber.trim(),
    carrier: draft.operatorName.trim(),
    keepalive: editableKeepAlive(draft.keepalive),
  };
}
