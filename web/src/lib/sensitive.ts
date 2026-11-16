export function maskICCID(value?: string | null): string {
  const iccid = String(value ?? "").replace(/\s+/g, "");
  if (!iccid) return "--";
  if (iccid.length <= 8) return `${iccid.slice(0, 2)}${"*".repeat(Math.max(2, iccid.length - 4))}${iccid.slice(-2)}`;
  return `${iccid.slice(0, 6)}${"*".repeat(Math.max(4, iccid.length - 10))}${iccid.slice(-4)}`;
}

export function displayICCID(value: string | undefined | null, showSensitive: boolean): string {
  return showSensitive ? String(value ?? "").replace(/\s+/g, "") || "--" : maskICCID(value);
}

export function maskSecret(value?: string | null): string {
  return String(value ?? "").trim() ? "********" : "--";
}

export function displaySecret(value: string | undefined | null, showSensitive: boolean): string {
  return showSensitive ? String(value ?? "").trim() || "--" : maskSecret(value);
}
