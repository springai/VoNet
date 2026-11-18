import type { CardKeepAliveCharge, CardKeepAliveConfig, SimCard, SimCardInput, SimCardType } from "../../types";

export type CardTransferRecord = Pick<
  SimCardInput,
  | "iccid"
  | "displayName"
  | "cardType"
  | "notes"
  | "msisdn"
  | "carrier"
  | "keepalive"
>;

export interface CardTransferFile {
  cards: CardTransferRecord[];
  chargeRecords: CardKeepAliveCharge[];
}

function normalizedICCID(value: unknown): string {
  return String(value ?? "").replace(/\s+/g, "");
}

function requiredText(value: unknown, field: string, maxLength: number): string {
  const text = String(value ?? "").trim();
  if (text.length > maxLength) {
    throw new Error(`${field} exceeds ${maxLength} characters`);
  }
  return text;
}

function optionalText(value: unknown, field: string, maxLength: number): string {
  if (value === undefined || value === null) return "";
  return requiredText(value, field, maxLength);
}

function optionalNumber(value: unknown, fallback = 0): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? Math.max(0, Math.floor(parsed)) : fallback;
}

function normalizeKeepAlive(value: unknown, index: number): CardKeepAliveConfig {
  const record = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const currency = String(record.currency || "CNY").toUpperCase();
  const cycleRule = String(record.cycleRule || record.cycle_rule || "monthly");
  return {
    enabled: record.enabled === true,
    planInfo: optionalText(record.planInfo ?? record.plan_info, `card ${index + 1} keep-alive plan`, 500),
    currency: (["CNY", "USD", "GBP", "EUR", "NZD"].includes(currency) ? currency : "CNY") as CardKeepAliveConfig["currency"],
    balanceCents: optionalNumber(record.balanceCents ?? record.balance_cents),
    planStartAt: optionalNumber(record.planStartAt ?? record.plan_start_at),
    currentCycleStartAt: optionalNumber(record.currentCycleStartAt ?? record.current_cycle_start_at),
    expiresAt: optionalNumber(record.expiresAt ?? record.expires_at),
    cycleRule: (["custom_days", "monthly", "quarterly", "half_yearly", "yearly"].includes(cycleRule) ? cycleRule : "monthly") as CardKeepAliveConfig["cycleRule"],
    cycleDays: optionalNumber(record.cycleDays ?? record.cycle_days),
    deductionCents: optionalNumber(record.deductionCents ?? record.deduction_cents),
    autoRenew: record.autoRenew === true,
    notificationEnabled: record.notificationEnabled === true,
    notifyDaysBefore: Math.max(1, Math.min(365, optionalNumber(record.notifyDaysBefore ?? record.notify_days_before, 3) || 3)),
  };
}

function normalizeRecord(value: unknown, index: number): CardTransferRecord {
  if (!value || typeof value !== "object") {
    throw new Error(`card ${index + 1} must be an object`);
  }
  const record = value as Record<string, unknown>;
  const iccid = normalizedICCID(record.iccid);
  if (!/^\d{18,22}$/.test(iccid)) {
    throw new Error(`card ${index + 1} has an invalid ICCID`);
  }
  const cardType = String(record.cardType ?? "").trim();
  if (cardType !== "physical_sim" && cardType !== "esim_profile") {
    throw new Error(`card ${index + 1} has an invalid card type`);
  }
  return {
    iccid,
    displayName: requiredText(record.displayName, `card ${index + 1} name`, 128),
    cardType: cardType as SimCardType,
    notes: requiredText(record.notes, `card ${index + 1} notes`, 2000),
    msisdn: optionalText(record.msisdn, `card ${index + 1} phone number`, 64),
    carrier: optionalText(record.carrier, `card ${index + 1} carrier`, 128),
    keepalive: normalizeKeepAlive(record.keepalive, index),
  };
}

export function buildCardTransfer(
  cards: SimCard[],
  chargesByIccid: Record<string, CardKeepAliveCharge[]> = {},
): CardTransferFile {
  const chargeRecords = cards.flatMap((card) => chargesByIccid[card.iccid] || []);
  return {
    cards: cards.map((card) => ({
      iccid: normalizedICCID(card.iccid),
      displayName: card.displayName || "",
      cardType: card.cardType,
      notes: card.notes || "",
      msisdn: card.msisdn || "",
      carrier: card.carrier || "",
      keepalive: card.keepalive ? {
        enabled: card.keepalive.enabled,
        planInfo: card.keepalive.planInfo || "",
        currency: card.keepalive.currency,
        balanceCents: card.keepalive.balanceCents,
        planStartAt: card.keepalive.planStartAt || 0,
        currentCycleStartAt: card.keepalive.currentCycleStartAt || 0,
        expiresAt: card.keepalive.expiresAt || 0,
        cycleRule: card.keepalive.cycleRule,
        cycleDays: card.keepalive.cycleDays,
        deductionCents: card.keepalive.deductionCents,
        autoRenew: card.keepalive.autoRenew,
        notificationEnabled: card.keepalive.notificationEnabled,
        notifyDaysBefore: card.keepalive.notifyDaysBefore,
      } : {
        enabled: false,
        planInfo: "",
        currency: "CNY",
        balanceCents: 0,
        planStartAt: 0,
        currentCycleStartAt: 0,
        expiresAt: 0,
        cycleRule: "monthly",
        cycleDays: 0,
        deductionCents: 0,
        autoRenew: false,
        notificationEnabled: false,
        notifyDaysBefore: 3,
      },
    })),
    chargeRecords,
  };
}

export function parseCardTransferFile(value: unknown): CardTransferFile {
  const source = Array.isArray(value)
    ? value
    : value && typeof value === "object"
      ? (value as { cards?: unknown }).cards
      : undefined;
  if (!Array.isArray(source)) {
    throw new Error("card import must contain a cards array");
  }
  const seen = new Set<string>();
  const cards = source.map((item, index) => {
    const record = normalizeRecord(item, index);
    if (seen.has(record.iccid)) {
      throw new Error(`duplicate ICCID ${record.iccid}`);
    }
    seen.add(record.iccid);
    return record;
  });
  const rawCharges = !Array.isArray(value) && value && typeof value === "object"
    ? (value as { chargeRecords?: unknown }).chargeRecords
    : undefined;
  const chargeRecords = Array.isArray(rawCharges)
    ? rawCharges.map((item) => normalizeCharge(item))
    : [];
  return { cards, chargeRecords };
}

function normalizeCharge(value: unknown): CardKeepAliveCharge {
  const record = value && typeof value === "object" ? value as Record<string, unknown> : {};
  const id = optionalNumber(record.id);
  const iccid = normalizedICCID(record.iccid);
  if (!/^\d{18,22}$/.test(iccid)) throw new Error("charge record has an invalid ICCID");
  const status = String(record.status || "success");
  const source = String(record.source || "automatic");
  return {
    id,
    iccid,
    status: (["success", "insufficient", "failed"].includes(status) ? status : "failed") as CardKeepAliveCharge["status"],
    source: (["automatic", "manual"].includes(source) ? source : "automatic") as CardKeepAliveCharge["source"],
    amountCents: optionalNumber(record.amountCents ?? record.amount_cents),
    balanceBeforeCents: optionalNumber(record.balanceBeforeCents ?? record.balance_before_cents),
    balanceAfterCents: optionalNumber(record.balanceAfterCents ?? record.balance_after_cents),
    currency: (["CNY", "USD", "GBP", "EUR", "NZD"].includes(String(record.currency).toUpperCase()) ? String(record.currency).toUpperCase() : "CNY") as CardKeepAliveCharge["currency"],
    cycleStartAt: optionalNumber(record.cycleStartAt ?? record.cycle_start_at),
    cycleEndAt: optionalNumber(record.cycleEndAt ?? record.cycle_end_at),
    note: optionalText(record.note, "charge note", 500),
    createdAt: optionalNumber(record.createdAt ?? record.created_at),
  };
}

export function parseCardTransfer(value: unknown): CardTransferRecord[] {
  return parseCardTransferFile(value).cards;
}

export function cardTransferFilename(now = new Date()): string {
  return `vonet-cards-${now.toISOString().slice(0, 10)}.json`;
}
