import type { LoginResponse } from "../types";

export interface AuthUser {
  username: string;
  role: string;
}

export function loginIdentityFromResponse(
  response: LoginResponse,
  fallbackUsername: string,
): AuthUser {
  return {
    username: response.user?.username || response.username || fallbackUsername,
    role: response.user?.role || response.role || "Administrator",
  };
}

export function isStaleAuthRequest(
  requestStartedAt: number | undefined,
  loginCompletedAt: number,
) {
  return typeof requestStartedAt === "number" && requestStartedAt < loginCompletedAt;
}
