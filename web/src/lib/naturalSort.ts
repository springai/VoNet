const collator = new Intl.Collator("zh-CN", {
  caseFirst: "upper",
  numeric: false,
  sensitivity: "base",
  usage: "sort",
});

type NaturalPart = {
  numeric: boolean;
  value: string;
};

export interface NaturalCardSortInput {
  displayName?: string;
  carrier?: string;
  iccid: string;
}

export interface NaturalDeviceSortInput {
  id: string;
  name?: string;
}

function isDigit(value: string) {
  return value >= "0" && value <= "9";
}

function leadingSortClass(value: string) {
  const first = Array.from(value.trim())[0] || "";
  if (!first) return 1;
  if (/\p{L}/u.test(first)) return 0;
  if (isDigit(first)) return 2;
  return 1;
}

function naturalParts(value: string): NaturalPart[] {
  const parts: NaturalPart[] = [];
  let start = 0;
  while (start < value.length) {
    const numeric = isDigit(value[start]);
    let end = start + 1;
    while (end < value.length && isDigit(value[end]) === numeric) end += 1;
    parts.push({ numeric, value: value.slice(start, end).toLocaleLowerCase("zh-CN") });
    start = end;
  }
  return parts;
}

function compareNumericPart(left: string, right: string) {
  const leftDigits = left.replace(/^0+/, "");
  const rightDigits = right.replace(/^0+/, "");
  if (leftDigits.length !== rightDigits.length) return leftDigits.length - rightDigits.length;
  const compared = leftDigits.localeCompare(rightDigits);
  if (compared !== 0) return compared;
  return left.length - right.length || left.localeCompare(right);
}

export function compareNaturalText(left: string, right: string) {
  const leftClass = leadingSortClass(left);
  const rightClass = leadingSortClass(right);
  if (leftClass !== rightClass) return leftClass - rightClass;

  const leftParts = naturalParts(left);
  const rightParts = naturalParts(right);
  const length = Math.min(leftParts.length, rightParts.length);
  for (let index = 0; index < length; index += 1) {
    const leftPart = leftParts[index];
    const rightPart = rightParts[index];
    if (leftPart.numeric !== rightPart.numeric) return leftPart.numeric ? 1 : -1;
    const compared = leftPart.numeric
      ? compareNumericPart(leftPart.value, rightPart.value)
      : collator.compare(leftPart.value, rightPart.value);
    if (compared !== 0) return compared;
  }
  return leftParts.length - rightParts.length || collator.compare(left, right);
}

export function compareNaturalCards(left: NaturalCardSortInput, right: NaturalCardSortInput) {
  const leftName = left.displayName?.trim() || left.carrier?.trim() || left.iccid;
  const rightName = right.displayName?.trim() || right.carrier?.trim() || right.iccid;
  const byName = compareNaturalText(leftName, rightName);
  if (byName !== 0) return byName;
  const byCarrier = compareNaturalText(left.carrier?.trim() || "", right.carrier?.trim() || "");
  return byCarrier || compareNaturalText(left.iccid, right.iccid);
}

export function compareNaturalDevices(left: NaturalDeviceSortInput, right: NaturalDeviceSortInput) {
  const leftName = left.name?.trim() || left.id;
  const rightName = right.name?.trim() || right.id;
  return compareNaturalText(leftName, rightName) || compareNaturalText(left.id, right.id);
}
