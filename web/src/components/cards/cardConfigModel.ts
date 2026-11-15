import type { SimCard, SimCardInput, SimCardType } from "../../types";

export interface CardConfigDraft {
  displayName: string;
  cardType: SimCardType;
  notes: string;
}

export function cardConfigDraft(card: SimCard): CardConfigDraft {
  return {
    displayName: card.displayName || "",
    cardType: card.cardType,
    notes: card.notes || "",
  };
}

export function cardConfigDirty(card: SimCard, draft: CardConfigDraft): boolean {
  return (
    draft.displayName.trim() !== (card.displayName || "").trim() ||
    draft.cardType !== card.cardType ||
    draft.notes.trim() !== (card.notes || "").trim()
  );
}

export function cardConfigPatch(draft: CardConfigDraft): Pick<SimCardInput, "displayName" | "cardType" | "notes"> {
  return {
    displayName: draft.displayName.trim(),
    cardType: draft.cardType,
    notes: draft.notes.trim(),
  };
}
