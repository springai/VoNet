import { api } from "../../api";

export interface CallInfo {
  id?: string;
  index?: number;
  direction?: number | string;
  state?: number | string;
  mode?: number;
  number?: string;
  raw?: string;
  startedAt?: string;
  endedAt?: string | null;
}

export interface CallsResponse {
  deviceId: string;
  iccid?: string;
  transport: string;
  calls: CallInfo[];
  raw?: string;
}

export interface CallActionResponse {
  accepted: boolean;
  iccid?: string;
  action: string;
  number?: string;
  callId?: string;
  durationSeconds?: number;
  transport: string;
  call?: CallInfo;
}

export interface CallRecord {
  id: number;
  deviceId: string;
  number: string;
  direction: string;
  status: string;
  transport: string;
  durationSeconds: number;
  startedAt: number;
  endedAt: number;
  createdAt: number;
}

function isIncomingDirection(direction: number | string | undefined): boolean {
  return direction === 1 || (typeof direction === "string" && direction.toLowerCase() === "incoming");
}

export function findIncomingCall(calls: CallInfo[]): CallInfo | undefined {
  return calls.find((call) => {
    if (call.endedAt) return false;
    const direction = call.direction;
    const state = typeof call.state === "string" ? call.state.toLowerCase() : call.state;
    if (state === "incoming") return true;
    if (state === "ringing" || state === "waiting") {
      return isIncomingDirection(direction) || direction == null;
    }
    if (typeof state === "number") {
      if (state === 4 || state === 5) return true;
      return isIncomingDirection(direction) && (state === 0 || state === 3);
    }
    return false;
  });
}

export interface CallHistoryResponse {
  records: CallRecord[];
  total: number;
}

function callPath(deviceId: string, suffix: string, iccid?: string) {
  const query = iccid ? `?iccid=${encodeURIComponent(iccid)}` : "";
  return `/devices/${encodeURIComponent(deviceId)}/calls${suffix}${query}`;
}

export async function listCalls(deviceId: string, iccid?: string): Promise<CallsResponse> {
  return api<CallsResponse>(callPath(deviceId, "", iccid));
}

export async function dialCall(deviceId: string, number: string, durationSeconds = 0, iccid?: string): Promise<CallActionResponse> {
  return api<CallActionResponse>(callPath(deviceId, "/dial", iccid), {
    method: "POST",
    body: { number, duration_seconds: durationSeconds },
  });
}

export async function answerCall(deviceId: string, callId?: string, iccid?: string): Promise<CallActionResponse> {
  return api<CallActionResponse>(callPath(deviceId, "/answer", iccid), {
    method: "POST",
    body: { call_id: callId || "" },
  });
}

export async function hangupCall(deviceId: string, callId?: string, iccid?: string): Promise<CallActionResponse> {
  return api<CallActionResponse>(callPath(deviceId, "/hangup", iccid), {
    method: "POST",
    body: { call_id: callId || "" },
  });
}

export async function rejectCall(deviceId: string, callId?: string, iccid?: string): Promise<CallActionResponse> {
  return api<CallActionResponse>(callPath(deviceId, "/reject", iccid), {
    method: "POST",
    body: { call_id: callId || "" },
  });
}

export async function sendDTMF(deviceId: string, digit: string, callId?: string, iccid?: string): Promise<CallActionResponse> {
  return api<CallActionResponse>(callPath(deviceId, "/dtmf", iccid), {
    method: "POST",
    body: { digit, call_id: callId || "" },
  });
}

export function connectCallMedia(deviceId: string, callId: string, iccid?: string): WebSocket {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const query = new URLSearchParams({ call_id: callId });
  if (iccid) query.set("iccid", iccid);
  return new WebSocket(`${proto}//${location.host}/api/devices/${encodeURIComponent(deviceId)}/calls/media?${query}`);
}

export async function getCallHistory(deviceId: string, query?: string, limit = 50, offset = 0, iccid?: string): Promise<CallHistoryResponse> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) });
  if (query) params.set("q", query);
  if (iccid) params.set("iccid", iccid);
  return api<CallHistoryResponse>(`/devices/${encodeURIComponent(deviceId)}/calls/history?${params}`);
}
export async function deleteCallRecord(deviceId: string, id: number, iccid?: string): Promise<void> {
  const query = new URLSearchParams({ id: String(id) });
  if (iccid) query.set("iccid", iccid);
  await api(`/devices/${encodeURIComponent(deviceId)}/calls/history/delete?${query}`, { method: "DELETE" });
}

export async function deleteAllCallHistory(deviceId: string, iccid?: string): Promise<void> {
  const query = new URLSearchParams();
  if (iccid) query.set("iccid", iccid);
  const suffix = query.toString() ? `?${query.toString()}` : "";
  await api("/devices/" + deviceId + "/calls/history/delete" + suffix, { method: "DELETE" });
}

export async function recordMissedCall(deviceId: string, number: string, iccid?: string): Promise<void> {
  await api("/devices/" + deviceId + "/calls/history/missed", {
    method: "POST",
    body: { number, iccid: iccid || "" },
  });
}

export async function recordCallHistory(
  deviceId: string,
  number: string,
  direction: "incoming" | "outgoing",
  status: "answered" | "missed" | "rejected",
  iccid?: string,
): Promise<void> {
  await api("/devices/" + deviceId + "/calls/history", {
    method: "POST",
    body: { number, direction, status, iccid: iccid || "" },
  });
}
