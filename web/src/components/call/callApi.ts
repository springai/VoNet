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
  transport: string;
  calls: CallInfo[];
  raw?: string;
}

export interface CallActionResponse {
  accepted: boolean;
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

export async function listCalls(deviceId: string): Promise<CallsResponse> {
  return api<CallsResponse>("/devices/" + deviceId + "/calls");
}

export async function dialCall(deviceId: string, number: string, durationSeconds = 0): Promise<CallActionResponse> {
  return api<CallActionResponse>("/devices/" + deviceId + "/calls/dial", {
    method: "POST",
    body: { number, duration_seconds: durationSeconds },
  });
}

export async function answerCall(deviceId: string, callId?: string): Promise<CallActionResponse> {
  return api<CallActionResponse>("/devices/" + deviceId + "/calls/answer", {
    method: "POST",
    body: { call_id: callId || "" },
  });
}

export async function hangupCall(deviceId: string, callId?: string): Promise<CallActionResponse> {
  return api<CallActionResponse>("/devices/" + deviceId + "/calls/hangup", {
    method: "POST",
    body: { call_id: callId || "" },
  });
}

export async function rejectCall(deviceId: string, callId?: string): Promise<CallActionResponse> {
  return api<CallActionResponse>("/devices/" + deviceId + "/calls/reject", {
    method: "POST",
    body: { call_id: callId || "" },
  });
}

export async function sendDTMF(deviceId: string, digit: string, callId?: string): Promise<CallActionResponse> {
  return api<CallActionResponse>("/devices/" + deviceId + "/calls/dtmf", {
    method: "POST",
    body: { digit, call_id: callId || "" },
  });
}

export function connectCallMedia(deviceId: string, callId: string): WebSocket {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return new WebSocket(proto + "//" + location.host + "/api/devices/" + deviceId + "/calls/media?call_id=" + encodeURIComponent(callId));
}

export async function getCallHistory(deviceId: string, query?: string, limit = 50, offset = 0): Promise<CallHistoryResponse> {
  let url = "/devices/" + deviceId + "/calls/history?limit=" + limit + "&offset=" + offset;
  if (query) url += "&q=" + encodeURIComponent(query);
  return api<CallHistoryResponse>(url);
}
export async function deleteCallRecord(deviceId: string, id: number): Promise<void> {
  await api("/devices/" + deviceId + "/calls/history/delete?id=" + id, { method: "DELETE" });
}

export async function deleteAllCallHistory(deviceId: string): Promise<void> {
  await api("/devices/" + deviceId + "/calls/history/delete", { method: "DELETE" });
}

export async function recordMissedCall(deviceId: string, number: string): Promise<void> {
  await api("/devices/" + deviceId + "/calls/history/missed", {
    method: "POST",
    body: { number },
  });
}

export async function recordCallHistory(
  deviceId: string,
  number: string,
  direction: "incoming" | "outgoing",
  status: "answered" | "missed" | "rejected",
): Promise<void> {
  await api("/devices/" + deviceId + "/calls/history", {
    method: "POST",
    body: { number, direction, status },
  });
}
