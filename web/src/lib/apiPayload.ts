export function camelizeKey(key: string) {
  return key.replace(/_([a-z0-9])/g, (_, char: string) => char.toUpperCase());
}

export function camelize<T>(value: unknown): T {
  if (Array.isArray(value)) return value.map((item) => camelize(item)) as T;
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>).map(([key, item]) => [
        camelizeKey(key),
        camelize(item),
      ]),
    ) as T;
  }
  return value as T;
}

export function selectAPIPayload<T>(
  normalized: Record<string, unknown>,
  unwrapData = true,
): T {
  if (unwrapData && Object.prototype.hasOwnProperty.call(normalized, "data")) {
    return normalized.data as T;
  }
  return normalized as T;
}
