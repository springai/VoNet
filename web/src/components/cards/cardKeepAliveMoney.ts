export function centsToMoney(cents: number): string {
  return (Math.max(0, Math.round(cents || 0)) / 100).toFixed(2);
}

export function moneyDisplay(cents: number): string {
  return centsToMoney(cents).replace(/\.00$/, "");
}

export function moneyToCents(value: string): number {
  const parsed = Number(String(value).replace(/[^\d.]/g, ""));
  return Number.isFinite(parsed) ? Math.max(0, Math.round(parsed * 100)) : 0;
}

export function sanitizeMoneyInput(value: string): string {
  let normalized = String(value).replace(/[^\d.]/g, "");
  if (normalized.startsWith(".")) normalized = `0${normalized}`;
  const firstDot = normalized.indexOf(".");
  if (firstDot >= 0) {
    normalized = normalized.slice(0, firstDot + 1) + normalized.slice(firstDot + 1).replace(/\./g, "");
  }
  const [whole = "", decimals] = normalized.split(".");
  const cleanWhole = whole.replace(/^0+(?=\d)/, "").slice(0, 10);
  if (decimals === undefined) return cleanWhole;
  return `${cleanWhole}.${decimals.slice(0, 2)}`;
}

export function dateInputValue(timestamp: number): string {
  if (!timestamp) return "";
  const date = new Date(timestamp * 1000);
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

export function dateInputTimestamp(value: string): number {
  if (!value) return 0;
  return Math.floor(new Date(`${value}T00:00:00`).getTime() / 1000);
}
