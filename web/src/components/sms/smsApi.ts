import { api } from "../../api";
import type { SMSContact, SMSMessage } from "../../types";

// Build a query string keeping snake_case keys (api() only snakeizes JSON bodies, not paths).
function qs(params: Record<string, string | number | undefined>): string {
  const sp = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === "") continue;
    sp.set(key, String(value));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

export function listContacts(deviceId: string, iccid?: string): Promise<SMSContact[]> {
  return api<SMSContact[]>(
    `/sms/contacts${qs({
      limit: 200,
      device_id: deviceId && deviceId !== "all" ? deviceId : undefined,
      iccid: iccid || undefined,
    })}`,
  );
}

export interface ThreadQuery {
  peer: string;
  limit: number;
  deviceId?: string;
  modemImei?: string;
  iccid?: string;
  imsi?: string;
  beforeTs?: string;
  beforeId?: number;
}

export function getThread(q: ThreadQuery): Promise<SMSMessage[]> {
  return api<SMSMessage[]>(
    `/sms/thread${qs({
      peer: q.peer,
      limit: q.limit,
      device_id: q.deviceId,
      modem_imei: q.modemImei,
			iccid: q.iccid,
      imsi: q.imsi,
      before_ts: q.beforeTs,
      before_id: q.beforeId,
    })}`,
  );
}

export interface SendSmsPayload {
	deviceId?: string;
	iccid?: string;
	phone: string;
	message: string;
}

export interface SmsSendResult {
	id?: number;
	messageId?: string;
	partsTotal: number;
	partsAttempted: number;
	partsAccepted: number;
	allPartsAccepted: boolean;
	submissionAccepted: boolean;
	deliveryConfirmed: boolean;
	outcome: "delivered" | "accepted_unconfirmed" | "partial" | "failed";
	submissionState?: string;
	deliveryState?: string;
	transport?: "ims" | "cellular_at";
	warning?: string;
}

export function sendSms(payload: SendSmsPayload): Promise<SmsSendResult> {
	const { iccid, ...body } = payload;
	const query = iccid ? `?iccid=${encodeURIComponent(iccid)}` : "";
	return api<SmsSendResult>(`/sms/send${query}`, { method: "POST", body });
}

export function deleteMessage(id: number): Promise<{ threadEmpty?: boolean }> {
  return api<{ threadEmpty?: boolean }>(`/sms/messages/${id}`, { method: "DELETE" });
}

export interface DeleteThreadQuery {
  peer: string;
  deviceId?: string;
  modemImei?: string;
  iccid?: string;
  imsi?: string;
}

export function deleteThread(q: DeleteThreadQuery): Promise<unknown> {
  return api(`/sms/thread${qs({ peer: q.peer, device_id: q.deviceId, modem_imei: q.modemImei, iccid: q.iccid, imsi: q.imsi })}`, { method: "DELETE" });
}
