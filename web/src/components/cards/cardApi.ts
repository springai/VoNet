import { api } from "../../api";
import type { SimCard, SimCardCandidate, SimCardInput, SimCardsResponse } from "../../types";
import type { CardTransferRecord } from "./cardTransfer";

type CardsPayload = SimCard[] | SimCardsResponse;

function normalizeCard(value: SimCard): SimCard {
  const bindings = value.bindings || (value.binding ? [value.binding] : []);
  const activeBinding = bindings.find((binding) => binding.deviceId) || bindings[0];
  return {
    ...value,
    registered: true,
    status: value.status || "offline",
    bindings,
    binding: activeBinding,
  };
}

function cardList(payload: CardsPayload): SimCard[] {
  return (Array.isArray(payload) ? payload : payload.cards || []).map(normalizeCard);
}

export async function listCards(): Promise<SimCard[]> {
  return cardList(await api<CardsPayload>("/cards"));
}

export async function getCard(iccid: string): Promise<SimCard> {
  return normalizeCard(await api<SimCard>(`/cards/${encodeURIComponent(iccid)}`));
}

export async function createCard(input: SimCardInput): Promise<SimCard> {
  return normalizeCard(await api<SimCard>("/cards", { method: "POST", body: input }));
}

export async function updateCard(iccid: string, input: Partial<SimCardInput>): Promise<SimCard> {
  return normalizeCard(
    await api<SimCard>(`/cards/${encodeURIComponent(iccid)}`, {
      method: "PATCH",
      body: input,
    }),
  );
}

export interface CardImportResult {
  imported: number;
  created: number;
  updated: number;
}

export async function importCards(cards: CardTransferRecord[]): Promise<CardImportResult> {
  return api<CardImportResult>("/cards/import", {
    method: "POST",
    body: { cards },
  });
}

export async function listCardCandidates(registeredCards: SimCard[] = []): Promise<SimCardCandidate[]> {
  const registered = new Set(registeredCards.map((card) => card.iccid.replace(/\s+/g, "")));
  const payload = await api<SimCardCandidate[] | { cards?: SimCardCandidate[] }>("/cards/candidates");
  const candidates = Array.isArray(payload) ? payload : payload.cards || [];
  return candidates.map((candidate) => {
    const iccid = candidate.iccid.replace(/\s+/g, "");
    return {
      ...candidate,
      iccid,
      state: candidate.state || "pending",
      conflict: candidate.conflict === true,
      registered: registered.has(iccid),
    };
  });
}

export async function deleteCard(iccid: string): Promise<void> {
  await api(`/cards/${encodeURIComponent(iccid)}`, { method: "DELETE" });
}

export function activeCardDevice(card: SimCard | null | undefined): string | null {
  if (!card || card.status !== "active" || card.conflict) return null;
  return card.binding?.deviceId?.trim() || null;
}

export function isCardOperable(card: SimCard | null | undefined): boolean {
  return !!activeCardDevice(card);
}
