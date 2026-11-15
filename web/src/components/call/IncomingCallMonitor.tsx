import { useCallback, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useI18n } from "../../lib/i18n";
import { usePolling } from "../../lib/usePolling";
import type { SimCard } from "../../types";
import { Button, Modal } from "../ui";
import { activeCardDevice, listCards } from "../cards/cardApi";
import {
  answerCall,
  findIncomingCall,
  listCalls,
  rejectCall,
  type CallInfo,
} from "./callApi";

interface IncomingCallTarget {
  card: SimCard;
  call: CallInfo;
}

function incomingCallKey(card: SimCard, call: CallInfo): string {
  return call.id || `${card.iccid}:${call.index ?? call.number ?? ""}`;
}

export function IncomingCallMonitor() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const requestIdRef = useRef(0);
  const handledCallKeysRef = useRef(new Set<string>());
  const [target, setTarget] = useState<IncomingCallTarget | null>(null);
  const [busy, setBusy] = useState(false);

  const pollIncomingCalls = useCallback(async () => {
    const requestId = ++requestIdRef.current;
    try {
      const cards = await listCards();
      const activeCards = cards.filter((card) => card.status === "active" && activeCardDevice(card));
      const results = await Promise.allSettled(
        activeCards.map(async (card) => ({
          card,
          deviceId: activeCardDevice(card)!,
          calls: (await listCalls(activeCardDevice(card)!, card.iccid)).calls || [],
        })),
      );
      if (requestId !== requestIdRef.current) return;

      for (const result of results) {
        if (result.status !== "fulfilled") continue;
        const call = findIncomingCall(result.value.calls);
        if (call && !handledCallKeysRef.current.has(incomingCallKey(result.value.card, call))) {
          setTarget({ card: result.value.card, call });
          return;
        }
      }
      setTarget(null);
    } catch {
      // Keep the current dialog when a transient API error hides call state.
    }
  }, []);

  usePolling(pollIncomingCalls, 2000);

  const answer = useCallback(async () => {
    if (!target || busy) return;
    setBusy(true);
    try {
      const deviceId = activeCardDevice(target.card);
      if (!deviceId || target.card.status !== "active") return;
      handledCallKeysRef.current.add(incomingCallKey(target.card, target.call));
      await answerCall(deviceId, target.call.id, target.card.iccid);
      try {
        sessionStorage.setItem("vonet_has_answered", "1");
      } catch {}
      setTarget(null);
      navigate(`/call?iccid=${encodeURIComponent(target.card.iccid)}`);
    } finally {
      setBusy(false);
    }
  }, [busy, navigate, target]);

  const reject = useCallback(async () => {
    if (!target || busy) return;
    setBusy(true);
    try {
      const deviceId = activeCardDevice(target.card);
      if (!deviceId || target.card.status !== "active") return;
      handledCallKeysRef.current.add(incomingCallKey(target.card, target.call));
      await rejectCall(deviceId, target.call.id, target.card.iccid);
      setTarget(null);
    } finally {
      setBusy(false);
    }
  }, [busy, target]);

  return (
    <Modal
      open={!!target}
      onClose={() => {}}
      closeOnOverlay={false}
      showClose={false}
      title={t("来电")}
      width="max-w-md"
      className="incoming-call-modal"
      bodyClassName="incoming-call-modal-body"
    >
      <div className="incoming-call-identity">
        <div className="incoming-call-icon">{"\u260E"}</div>
        <div>
          <div className="incoming-call-number">{target?.call.number || t("未知号码")}</div>
          <div className="incoming-call-label">
            {target?.card.displayName || target?.card.iccid || t("通话")}
          </div>
        </div>
      </div>
      <div className="incoming-call-actions">
        <Button variant="danger" block loading={busy} onClick={reject}>{t("拒接")}</Button>
        <Button variant="success" block loading={busy} onClick={answer}>{t("接听")}</Button>
      </div>
    </Modal>
  );
}
