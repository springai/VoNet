import { useCallback, useEffect, useRef, useState } from "react";
import {
  PhoneRegular,
  MicOffRegular,
  MicRegular,
  KeyboardRegular,
} from "@fluentui/react-icons";
import { message } from "../ui";
import { useI18n } from "../../lib/i18n";
import { cx } from "../../lib/utils";
import {
  listCalls,
  dialCall,
  answerCall,
  hangupCall,
  rejectCall,
  sendDTMF,
  connectCallMedia,
  recordCallHistory,
  type CallInfo,
  type CallsResponse,
} from "./callApi";

const DIAL_STORAGE_KEY = "vonet_dial_number";

const DTMF_KEYS = ["1","2","3","4","5","6","7","8","9","*","0","#"];
const DTMF_LABELS: Record<string, string> = {
  "1":"","2":"ABC","3":"DEF","4":"GHI","5":"JKL","6":"MNO","7":"PQRS","8":"TUV","9":"WXYZ","*":"","+":"","#":"#",
};


const DTMF_FREQS: Record<string, [number, number]> = {
  "1": [697, 1209], "2": [697, 1336], "3": [697, 1477],
  "4": [770, 1209], "5": [770, 1336], "6": [770, 1477],
  "7": [852, 1209], "8": [852, 1336], "9": [852, 1477],
  "*": [941, 1209], "0": [941, 1336], "#": [941, 1477],
};

function playDtmfTone(digit: string, duration = 0.15) {
  const freqs = DTMF_FREQS[digit];
  if (!freqs) return;
  try {
    const ctx = new AudioContext();
    const gain = ctx.createGain();
    gain.gain.value = 0.3;
    gain.connect(ctx.destination);
    for (const f of freqs) {
      const osc = ctx.createOscillator();
      osc.frequency.value = f;
      osc.connect(gain);
      osc.start();
      osc.stop(ctx.currentTime + duration);
    }
    setTimeout(() => ctx.close(), (duration + 0.1) * 1000);
  } catch {}
}
type CallPhase = "idle" | "dialing" | "ringing" | "connected" | "incoming";

function isIncomingDirection(direction: number | string | undefined): boolean {
  if (typeof direction === "number") return direction === 1;
  if (typeof direction === "string") return direction.toLowerCase() === "incoming";
  return false;
}

function isOutgoingDirection(direction: number | string | undefined): boolean {
  if (typeof direction === "number") return direction === 0;
  if (typeof direction === "string") return direction.toLowerCase() === "outgoing";
  return false;
}

function isTerminalState(state: number | string | undefined, endedAt?: string | null): boolean {
  if (endedAt) return true;
  if (typeof state === "number") return false;
  if (typeof state === "string") {
    const s = state.toLowerCase();
    return s === "ended" || s === "failed";
  }
  return false;
}

// CLCC numeric states: 0=active, 1=held, 2=dialing(MO), 3=alerting(MO), 4=incoming(MT), 5=waiting(MT)
function callStateToPhase(calls: CallInfo[], hasAnswered: boolean, isDialing: boolean, hasSeenDialing: boolean, dialStartTime: number): { phase: CallPhase; active?: CallInfo; incoming?: CallInfo } {
  // If the modem never reports state=2/3, fall back after 10 seconds:
  // assume state=0 is real if enough time has passed since dialing started.
  const dialElapsed = dialStartTime > 0 ? Date.now() - dialStartTime : Infinity;
  const dialGraceExpired = dialElapsed > 10000;
  const live = calls.filter((c) => !isTerminalState(c.state, c.endedAt));
  for (const c of live) {
    const state = c.state;
    const dir = c.direction;

    // String states (VoWiFi)
    if (typeof state === "string") {
      const s = state.toLowerCase();
      if (s === "ringing" || s === "waiting") {
        if (isIncomingDirection(dir)) return { phase: "incoming", incoming: c };
        return { phase: "ringing", active: c };
      }
      if (s === "dialing" || s === "early_media") return { phase: "dialing", active: c };
      if (s === "connected" || s === "active") return { phase: "connected", active: c };
    }

    // Numeric states (CLCC cellular)
    if (typeof state === "number") {
      if (state === 4 || state === 5) { if (isDialing) return { phase: "dialing", active: c }; return { phase: "incoming", incoming: c }; }
      if (state === 2 || state === 3) return { phase: "dialing", active: c };
      if (state === 0) {
        // state=0 means "active". For incoming calls the modem may report
        // state=0 before the user answers. If direction is incoming and the
        // user hasn't clicked answer yet, show as "incoming" — but only if
        // the user did NOT just dial (isDialing). dialedNumberRef is
        // persisted in sessionStorage so it survives page refreshes,
        // preventing outgoing calls from being mislabelled as incoming.
        if (isIncomingDirection(dir) && !hasAnswered && !isDialing) return { phase: "incoming", incoming: c };
        // For user-initiated outgoing calls, stay in "dialing" until we've
        // seen state=2/3 (confirming the modem acknowledged the dial) or
        // the 10-second grace period expires.
        if (isDialing && !hasSeenDialing && !dialGraceExpired) return { phase: "dialing", active: c };
        return { phase: "connected", active: c };
      }
      if (state === 1) return { phase: "connected", active: c }; // held
    }
  }
  return { phase: "idle" };
}

function fmtDur(s: number) {
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

function Avatar({ color = "#6366f1", size = 96 }: { color?: string; size?: number }) {
  return (
    <div className="mx-auto flex items-center justify-center rounded-full"
      style={{
        width: size, height: size,
        background: color + "22",
        border: `2px solid ${color}55`,
        fontSize: size * 0.42,
        color,
      }}>{"☎"}</div>
  );
}

function RoundBtn({ icon, label, color, bg, onClick, active }: {
  icon: React.ReactNode; label: string; color?: string; bg?: string;
  onClick: () => void; active?: boolean;
}) {
  return (
    <div className="flex flex-col items-center gap-1.5">
      <button onClick={onClick} className={cx(
        "flex h-14 w-14 items-center justify-center rounded-full border text-xl transition-colors cursor-pointer",
        active ? "border-current" : "border-gray-200 dark:border-gray-700",
      )} style={{
        background: bg || (active ? (color || "#6366f1") + "22" : undefined),
        color: active ? color : undefined,
      }}>{icon}</button>
      <span className="text-[11px] text-gray-500">{label}</span>
    </div>
  );
}

interface CallPanelProps {
  deviceId: string;
  iccid?: string;
  externalNumber?: string;
  onNumberConsumed?: () => void;
  onPhaseChange?: (phase: CallPhase) => void;
}

export function CallPanel({ deviceId, iccid, externalNumber, onNumberConsumed, onPhaseChange }: CallPanelProps) {
  const { t } = useI18n();
  const [phoneNumber, setPhoneNumber] = useState("");
  const [phase, setPhase] = useState<CallPhase>("idle");
  const [activeCall, setActiveCall] = useState<CallInfo | undefined>();
  const [muted, setMuted] = useState(false);
  const [showDtmf, setShowDtmf] = useState(false);
  const [callDuration, setCallDuration] = useState(0);
  const [transport, setTransport] = useState("");
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const processorRef = useRef<ScriptProcessorNode | null>(null);
  const durationRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const userHangupRef = useRef(false);
  const hasAnsweredRef = useRef<boolean>((() => { try { return sessionStorage.getItem("vonet_has_answered") === "1"; } catch { return false; } })());
  const prevPhaseRef = useRef<CallPhase>("idle");
  const lastIncomingRef = useRef<{number: string; time: number} | null>(null);
  // Track pending incoming call for history recording.
  // status: "ringing" -> "answered" | "rejected" | "missed"
  const pendingIncomingRef = useRef<{number: string; time: number; status: "ringing" | "answered" | "rejected" | "missed"} | null>(null);
  const callNumberRef = useRef("");
  const hasSeenDialingRef = useRef(false);
  const dialStartTimeRef = useRef<number>((() => { try { const v = sessionStorage.getItem("vonet_dial_start"); return v ? parseInt(v, 10) || 0 : 0; } catch { return 0; } })());
  // Restore dial state from sessionStorage so it survives page refreshes.
  // This lets us distinguish outgoing calls (user dialed) from incoming calls
  // even after the page reloads and refs are reset.
  const dialedNumberRef = useRef<string>((() => { try { return sessionStorage.getItem(DIAL_STORAGE_KEY) || ""; } catch { return ""; } })());


  const inCall = phase !== "idle";

  // Fill number from history click
  useEffect(() => {
    if (externalNumber && !inCall) {
      setPhoneNumber(externalNumber);
      onNumberConsumed?.();
    }
  }, [externalNumber]);

  useEffect(() => {
    onPhaseChange?.(phase);
  }, [phase, onPhaseChange]);

  const pollCalls = useCallback(async () => {
    try {
      const resp = await listCalls(deviceId, iccid);
      setTransport(resp.transport || "");
      const calls = resp.calls || [];
      console.log("[CallPanel] pollCalls:", calls.length, "calls", JSON.stringify(calls), "hasAnswered:", hasAnsweredRef.current);

      // Track if we've seen dialing (state=2) or alerting (state=3) —
      // confirms the modem acknowledged the outgoing call properly.
      for (const c of calls) {
        if (typeof c.state === "number" && (c.state === 2 || c.state === 3)) {
          hasSeenDialingRef.current = true;
        }
      }

      if (userHangupRef.current) {
        const live = calls.filter((c) => !isTerminalState(c.state, c.endedAt));
        if (live.length === 0) {
          userHangupRef.current = false;
          hasAnsweredRef.current = false; try { sessionStorage.removeItem("vonet_has_answered"); } catch {}
          dialedNumberRef.current = ""; try { sessionStorage.removeItem(DIAL_STORAGE_KEY); sessionStorage.removeItem("vonet_dial_start"); } catch {}
          hasSeenDialingRef.current = false;
          if (prevPhaseRef.current !== "idle") { setPhoneNumber(""); }
          setPhase("idle");
          setActiveCall(undefined);
          setCallDuration(0);
          if (durationRef.current) { clearInterval(durationRef.current); durationRef.current = null; }
          return;
        }
      }

      const { phase: newPhase, active, incoming } = callStateToPhase(calls, hasAnsweredRef.current, dialedNumberRef.current !== "", hasSeenDialingRef.current, dialStartTimeRef.current);
      console.log("[CallPanel] computed phase:", newPhase);

      if (!userHangupRef.current) {
        prevPhaseRef.current = phase;
          // When incoming call appears, record it.
          if (newPhase === "incoming" && incoming && !pendingIncomingRef.current) {
            pendingIncomingRef.current = { number: incoming.number || "", time: Date.now(), status: "ringing" };
          }
          // When phase leaves "incoming" (caller hung up, answered, rejected, etc.)
          // and we have a pending incoming that is still "ringing", save as missed.
          if (pendingIncomingRef.current && pendingIncomingRef.current.status === "ringing" && phase === "incoming" && newPhase !== "incoming") {
            recordCallHistory(deviceId, pendingIncomingRef.current.number, "incoming", "missed", iccid).catch(() => {});
            pendingIncomingRef.current = null;
          }
          // Also timeout: if still "incoming" after 60s, save as missed (stale CLCC)
          if (pendingIncomingRef.current && pendingIncomingRef.current.status === "ringing" && newPhase === "incoming" && Date.now() - pendingIncomingRef.current.time > 60000) {
            recordCallHistory(deviceId, pendingIncomingRef.current.number, "incoming", "missed", iccid).catch(() => {});
            pendingIncomingRef.current = null;
          }
        setPhase(newPhase);
        setActiveCall(active || incoming);
        if (newPhase === "connected" && active) {
          if (!durationRef.current) {
            setCallDuration(0);
            durationRef.current = setInterval(() => setCallDuration((d) => d + 1), 1000);
          }
          // Start audio for outgoing calls (incoming calls start in handleAnswer)
          if (active.id && !wsRef.current) {
            console.log("[CallPanel] outgoing call connected, starting audio for", active.id);
            startAudio(active.id);
          }
        } else if (newPhase === "idle") {
          // Save answered calls to history when call ends
          if (pendingIncomingRef.current && pendingIncomingRef.current.status === "answered") {
            recordCallHistory(deviceId, pendingIncomingRef.current.number, "incoming", "answered", iccid).catch(() => {});
          }
          pendingIncomingRef.current = null;
          lastIncomingRef.current = null;
          setCallDuration(0);
          hasAnsweredRef.current = false; try { sessionStorage.removeItem("vonet_has_answered"); } catch {}
          dialedNumberRef.current = ""; try { sessionStorage.removeItem(DIAL_STORAGE_KEY); sessionStorage.removeItem("vonet_dial_start"); } catch {}
          hasSeenDialingRef.current = false;
          dialStartTimeRef.current = 0;
          if (prevPhaseRef.current !== "idle") { setPhoneNumber(""); }
          if (durationRef.current) { clearInterval(durationRef.current); durationRef.current = null; }
        }
      }
    } catch (err) {
      console.error("[CallPanel] pollCalls error:", err);
    }
  }, [deviceId, iccid]);

  useEffect(() => {
    pollCalls();
    pollRef.current = setInterval(pollCalls, 2000);
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
      if (durationRef.current) clearInterval(durationRef.current);
      stopAudio();
    };
  }, [pollCalls]);

  const stopAudio = useCallback(() => {
    if (processorRef.current) { processorRef.current.disconnect(); processorRef.current = null; }
    if (streamRef.current) { streamRef.current.getTracks().forEach((trk) => trk.stop()); streamRef.current = null; }
    if (wsRef.current) { wsRef.current.close(); wsRef.current = null; }
    if (audioCtxRef.current) { audioCtxRef.current.close(); audioCtxRef.current = null; }
  }, []);

  const startAudio = useCallback((callId: string) => {
    try {
      const ws = connectCallMedia(deviceId, callId, iccid);
      wsRef.current = ws;
      navigator.mediaDevices.getUserMedia({ audio: true }).then((stream) => {
        streamRef.current = stream;
        const audioCtx = new AudioContext();
        audioCtxRef.current = audioCtx;
        const nativeRate = audioCtx.sampleRate;
        const TARGET_RATE = 8000;
        const source = audioCtx.createMediaStreamSource(stream);
        const processor = audioCtx.createScriptProcessor(4096, 1, 1);
        processorRef.current = processor;
        processor.onaudioprocess = (e) => {
          if (ws.readyState === WebSocket.OPEN && !muted) {
            const data = e.inputBuffer.getChannelData(0);
            const outLen = Math.round(data.length * TARGET_RATE / nativeRate);
            const pcm16 = new Int16Array(outLen);
            for (let i = 0; i < outLen; i++) {
              const srcIdx = i * nativeRate / TARGET_RATE;
              const lo = Math.floor(srcIdx);
              const hi = Math.min(lo + 1, data.length - 1);
              const frac = srcIdx - lo;
              const sample = data[lo] * (1 - frac) + data[hi] * frac;
              pcm16[i] = Math.max(-32768, Math.min(32767, sample * 32768));
            }
            ws.send(pcm16.buffer);
          }
        };
        source.connect(processor);
        const muteGain = audioCtx.createGain();
        muteGain.gain.value = 0;
        processor.connect(muteGain);
        muteGain.connect(audioCtx.destination);
        ws.onmessage = (ev) => {
          if (ev.data instanceof Blob) {
            ev.data.arrayBuffer().then((buf) => {
              const samples = new Int16Array(buf);
              const outLen = Math.round(samples.length * nativeRate / TARGET_RATE);
              const audioBuffer = audioCtx.createBuffer(1, outLen, nativeRate);
              const out = audioBuffer.getChannelData(0);
              for (let i = 0; i < outLen; i++) {
                const srcIdx = i * TARGET_RATE / nativeRate;
                const lo = Math.floor(srcIdx);
                const hi = Math.min(lo + 1, samples.length - 1);
                const frac = srcIdx - lo;
                out[i] = (samples[lo] / 32768) * (1 - frac) + (samples[hi] / 32768) * frac;
              }
              const bufSource = audioCtx.createBufferSource();
              bufSource.buffer = audioBuffer;
              bufSource.connect(audioCtx.destination);
              bufSource.start();
            });
          }
        };
        ws.onclose = () => stopAudio();
        ws.onerror = () => stopAudio();
      }).catch((err) => {
        console.error("[CallPanel] getUserMedia error:", err);
        message.error(t("无法访问麦克风"));
      });
    } catch (err) {
      console.error("[CallPanel] startAudio error:", err);
    }
  }, [deviceId, iccid, muted, stopAudio, t]);

  const handleDial = useCallback(async () => {
    const num = phoneNumber.trim();
    if (!num) return;
    try {
      setPhase("dialing");
      setCallDuration(0);
      dialedNumberRef.current = num;
      dialStartTimeRef.current = Date.now();
      try { sessionStorage.setItem(DIAL_STORAGE_KEY, num); sessionStorage.setItem("vonet_dial_start", String(dialStartTimeRef.current)); } catch {}
      await dialCall(deviceId, num, 0, iccid);
      message.success(t("正在拨号"));
    } catch (err: any) {
      setPhase("idle");
      message.error(err.message || t("拨号失败"));
    }
  }, [phoneNumber, deviceId, iccid, t]);

  const handleAnswer = useCallback(async () => {
    try {
      hasAnsweredRef.current = true;
      try { sessionStorage.setItem("vonet_has_answered", "1"); } catch {}
      if (pendingIncomingRef.current) {
        pendingIncomingRef.current.status = "answered";
      }
      await answerCall(deviceId, activeCall?.id, iccid);
      message.success(t("已接听"));
      setPhase("connected");
      setCallDuration(0);
      if (!durationRef.current) {
        durationRef.current = setInterval(() => setCallDuration((d) => d + 1), 1000);
      }
      if (transport === "vowifi" && activeCall?.id) startAudio(activeCall.id);
    } catch (err: any) {
      hasAnsweredRef.current = false; try { sessionStorage.removeItem("vonet_has_answered"); } catch {}
      message.error(err.message || t("接听失败"));
    }
  }, [deviceId, iccid, activeCall, transport, startAudio, t]);

  const handleHangup = useCallback(async () => {
    try {
      userHangupRef.current = true;
      await hangupCall(deviceId, activeCall?.id, iccid);
      stopAudio();
      setPhase("idle");
      setActiveCall(undefined);
      setCallDuration(0);
      hasAnsweredRef.current = false; try { sessionStorage.removeItem("vonet_has_answered"); } catch {}
      dialedNumberRef.current = ""; try { sessionStorage.removeItem(DIAL_STORAGE_KEY); sessionStorage.removeItem("vonet_dial_start"); } catch {}
      hasSeenDialingRef.current = false;
      dialStartTimeRef.current = 0;
      if (durationRef.current) { clearInterval(durationRef.current); durationRef.current = null; }
      message.success(t("已挂断"));
      setTimeout(() => { userHangupRef.current = false; }, 5000);
    } catch (err: any) {
      userHangupRef.current = false;
      message.error(err.message || t("挂断失败"));
    }
  }, [deviceId, iccid, activeCall, stopAudio, t]);

  const handleReject = useCallback(async () => {
    try {
      userHangupRef.current = true;
      await rejectCall(deviceId, activeCall?.id, iccid);
      setPhase("idle");
      setActiveCall(undefined);
      hasAnsweredRef.current = false; try { sessionStorage.removeItem("vonet_has_answered"); } catch {}
      dialedNumberRef.current = ""; try { sessionStorage.removeItem(DIAL_STORAGE_KEY); sessionStorage.removeItem("vonet_dial_start"); } catch {}
      message.success(t("已拒接"));
      setTimeout(() => { userHangupRef.current = false; }, 5000);
    } catch (err: any) {
      userHangupRef.current = false;
      message.error(err.message || t("拒接失败"));
    }
  }, [deviceId, iccid, activeCall, t]);

  const handleDtmf = useCallback(async (digit: string) => {
    playDtmfTone(digit);
    try {
      await sendDTMF(deviceId, digit, activeCall?.id, iccid);
    } catch (err: any) {
      message.error(err.message || t("发送 DTMF 失败"));
    }
  }, [deviceId, iccid, activeCall, t]);

  const handleMute = useCallback(() => {
    setMuted((m) => {
      const next = !m;
      if (streamRef.current) streamRef.current.getAudioTracks().forEach((track) => { track.enabled = !next; });
      return next;
    });
  }, []);

  const appendDigit = useCallback((d: string) => {
    setPhoneNumber((p) => p + d);
  }, []);

  return (
    <div className="w-full px-4 py-6">
      <div className="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-700 dark:bg-gray-900">
        <div className="mb-4 flex items-center justify-between">
          <span className="text-xs text-gray-400">
            {transport === "vowifi" ? "VoWiFi" : transport === "cellular" ? "蜂窝" : "" }
          </span>
          {inCall && (
            <span className={cx(
              "text-xs font-medium",
              phase === "connected" ? "text-green-500" : "text-amber-500"
            )}>
              {phase === "dialing" && t("正在拨号")}
              {phase === "ringing" && t("正在响铃")}
              {phase === "connected" && t("通话中")}
              {phase === "incoming" && t("来电")}
            </span>
          )}
        </div>

        {phase === "incoming" && (
          <div className="flex flex-col items-center gap-4 py-4">
            <Avatar color="#f59e0b" size={84} />
            <div className="text-center">
              <div className="mono text-lg font-bold text-gray-900 dark:text-white">{activeCall?.number || t("未知号码")}</div>
              <div className="mt-1 text-sm text-gray-400">{t("蜂窝来电")}</div>
            </div>
            <div className="mt-2 flex justify-center gap-6">
              <RoundBtn icon="✕" label={t("拒接")} color="#fff" bg="#ef4444" onClick={handleReject} />
              <RoundBtn icon="📞" label={t("接听")} color="#fff" bg="#22c55e" onClick={handleAnswer} />
            </div>
          </div>
        )}

        {(phase === "dialing" || phase === "ringing") && (
          <div className="flex flex-col items-center gap-3 py-6">
            <Avatar color="#f59e0b" size={84} />
            <div className="text-center">
              <div className="mono text-lg font-bold text-gray-900 dark:text-white">{activeCall?.number || phoneNumber || dialedNumberRef.current}</div>
              <div className="mt-1 text-sm text-gray-400">
                {phase === "dialing" ? t("正在拨号...") : t("正在响铃...")}
              </div>
            </div>
            <div className="mt-4">
              <RoundBtn icon="✕" label={t("挂断")} color="#fff" bg="#ef4444" onClick={handleHangup} />
            </div>
          </div>
        )}

        {phase === "connected" && (
          <div className="flex flex-col items-center gap-3 py-6">
            <Avatar color="#22c55e" size={84} />
            <div className="text-center">
              <div className="mono text-lg font-bold text-gray-900 dark:text-white">{activeCall?.number || phoneNumber || dialedNumberRef.current}</div>
              <div className="mt-1 font-mono text-sm tabular-nums text-green-500">{fmtDur(callDuration)}</div>
              {transport === "cellular" && (
                <div className="mt-2 text-xs text-amber-500">
                  {t("蜂窝通话：麦克风和扬声器由模块硬件提供，浏览器音频不可用")}
                </div>
              )}
            </div>
            {showDtmf && (
              <div className="mx-auto mt-2 w-[220px]">
                <div className="grid grid-cols-3 gap-2">
                  {DTMF_KEYS.map((k) => (
                    <button key={k} onClick={() => handleDtmf(k)}
                      className="flex h-10 flex-col items-center justify-center rounded-lg border border-gray-200 bg-gray-50 text-base font-semibold text-gray-800 hover:bg-gray-100 dark:border-gray-700 dark:bg-gray-800 dark:text-white dark:hover:bg-gray-700">
                      <span>{k}</span>
                    </button>
                  ))}
                </div>
              </div>
            )}
            <div className="mt-2 flex justify-center gap-5">
              <RoundBtn icon={muted ? "🔇" : "🎤"} label={t(muted ? "取消静音" : "静音")} color="#60a5fa" onClick={handleMute} active={muted} />
              <RoundBtn icon="⌨" label={t("键盘")} color="#a78bfa" onClick={() => setShowDtmf((s) => !s)} active={showDtmf} />
            </div>
            <div className="mt-2">
              <RoundBtn icon="✕" label={t("挂断")} color="#fff" bg="#ef4444" onClick={handleHangup} />
            </div>
          </div>
        )}

        {!inCall && (
          <div className="flex flex-col">
            <input value={phoneNumber} onChange={(e) => setPhoneNumber(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter") handleDial(); }}
              placeholder={t("输入号码")}
              className="mono mb-4 border-none bg-transparent text-center text-2xl tracking-wide outline-none text-gray-900 dark:text-white" />
            <div className="grid grid-cols-3 gap-2.5">
              {DTMF_KEYS.map((k) => (
                <button key={k} onClick={() => appendDigit(k)}
                  className="flex flex-col items-center rounded-xl border border-gray-200 bg-gray-50 py-2.5 transition-colors hover:bg-gray-100 dark:border-gray-700 dark:bg-gray-800 dark:hover:bg-gray-700">
                  <span className="text-xl font-semibold text-gray-900 dark:text-white">{k}</span>
                  <span className="h-2.5 text-[9px] tracking-widest text-gray-400">{DTMF_LABELS[k]}</span>
                </button>
              ))}
            </div>
            <div className="mt-4 flex items-center justify-center gap-6">
              <button onClick={() => appendDigit("+")}
                className="flex h-[58px] w-[58px] items-center justify-center rounded-full border border-gray-200 bg-gray-50 text-xl font-semibold text-gray-600 transition-colors hover:bg-gray-100 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700">+</button>
              <button onClick={handleDial} disabled={!phoneNumber.trim()}
                className="flex h-16 w-16 items-center justify-center rounded-full bg-green-500 text-2xl text-white shadow-lg transition-colors hover:bg-green-600 disabled:cursor-not-allowed disabled:opacity-40">{"✆"}</button>
              <button onClick={() => setPhoneNumber((p) => p.slice(0, -1))}
                className="flex h-[58px] w-[58px] items-center justify-center rounded-full text-xl text-gray-400 transition-colors hover:bg-gray-100 dark:hover:bg-gray-800"
                style={{ visibility: phoneNumber ? "visible" : "hidden" }}>{"⌫"}</button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
