import type { SimCard, SimCardInput, SimCardType } from "../../types";

export type CardTransferRecord = Pick<SimCardInput, "iccid" | "displayName" | "cardType" | "notes">;

export interface CardTransferFile {
  cards: CardTransferRecord[];
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
  };
}

export function buildCardTransfer(cards: SimCard[]): CardTransferFile {
  return {
    cards: cards.map((card) => ({
      iccid: normalizedICCID(card.iccid),
      displayName: card.displayName || "",
      cardType: card.cardType,
      notes: card.notes || "",
    })),
  };
}

export function parseCardTransfer(value: unknown): CardTransferRecord[] {
  const source = Array.isArray(value)
    ? value
    : value && typeof value === "object"
      ? (value as { cards?: unknown }).cards
      : undefined;
  if (!Array.isArray(source)) {
    throw new Error("card import must contain a cards array");
  }
  const seen = new Set<string>();
  return source.map((item, index) => {
    const record = normalizeRecord(item, index);
    if (seen.has(record.iccid)) {
      throw new Error(`duplicate ICCID ${record.iccid}`);
    }
    seen.add(record.iccid);
    return record;
  });
}

export function cardTransferFilename(now = new Date()): string {
  return `vonet-cards-${now.toISOString().slice(0, 10)}.json`;
}
