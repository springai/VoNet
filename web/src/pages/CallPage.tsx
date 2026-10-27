import { useCallback, useEffect, useRef, useState } from "react";
import { PageHeader, Spinner, ErrorState, message } from "../components/ui";
import { useI18n } from "../lib/i18n";
import { usePolling } from "../lib/usePolling";
import { api } from "../api";
import { cx } from "../lib/utils";
import { CallPanel } from "../components/call/CallPanel";
import { dialCall, hangupCall, answerCall, rejectCall, sendDTMF, listCalls, connectCallMedia, type CallInfo, type CallsResponse, getCallHistory, deleteCallRecord, type CallRecord } from "../components/call/callApi";
import type { DeviceListItem, DevicesResponse } from "../types";

const MOBILE_BREAKPOINT = 980;

const DTMF_KEYS = ["1","2","3","4","5","6","7","8","9","*","0","#"];
const DTMF_LABELS: Record<string, string> = {
  "1":"","2":"ABC","3":"DEF","4":"GHI","5":"JKL","6":"MNO","7":"PQRS","8":"TUV","9":"WXYZ","*":"","+":"","#":"#",
};

function fmtDur(sec: number) {
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return m + ":" + String(s).padStart(2, "0");
}

function isIncomingCall(calls: CallInfo[]): CallInfo | undefined {
  return calls.find((c) => {
    if (c.endedAt) return false;
    const s = typeof c.state === "string" ? c.state.toLowerCase() : "";
    if (s === "ringing" || s === "waiting") return true;
    if (typeof c.state === "number" && (c.state === 4 || c.state === 5)) return true;
    return false;
  });
}

function CallHistoryItem({ record, t, onSelect, onDelete }: { record: CallRecord; t: (s: string) => string; onSelect: (n: string) => void; onDelete: (id: number) => void }) {
  const isIncoming = record.direction === "incoming";
  const isMissed = record.status === "missed";
  const date = new Date(record.startedAt * 1000);
  const now = new Date();
  const isToday = date.toDateString() === now.toDateString();
  const timeStr = isToday
    ? date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : date.toLocaleDateString([], { month: "short", day: "numeric" }) + " " + date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  const durMin = Math.floor(record.durationSeconds / 60);
  const durSec = String(record.durationSeconds % 60).padStart(2, "0");
  const durStr = record.durationSeconds > 0 ? durMin + ":" + durSec : "";

  return (
    <div
      className="group flex items-center gap-3 px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors cursor-pointer"
      onClick={() => record.number && onSelect(record.number)}
    >
      <div className={"flex h-10 w-10 items-center justify-center rounded-full text-sm font-bold shrink-0 " + (isIncoming ? (isMissed ? "bg-red-100 text-red-500 dark:bg-red-900/30 dark:text-red-400" : "bg-blue-100 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400") : "bg-green-100 text-green-600 dark:bg-green-900/30 dark:text-green-400")}>
        {isIncoming ? "\u2190" : "\u2192"}
      </div>
      <div className="flex-1 min-w-0">
        <div className={"text-sm font-semibold truncate " + (isMissed ? "text-red-500" : "text-gray-900 dark:text-white")}>
          {record.number || "未知号码"}
        </div>
        <div className="flex items-center gap-1.5 mt-0.5">
          <span className="text-[11px] text-gray-400">{isIncoming ? t("来电") : t("拨出")}</span>
          <span className="text-[11px] text-gray-300">{"\u00B7"}</span>
          <span className="text-[11px] text-gray-400">{timeStr}</span>
        </div>
      </div>
      <div className="flex items-center gap-2 shrink-0">
        <div className="text-right">
          {isMissed && <div className="text-xs font-bold text-red-500">{t("未接")}</div>}
          {durStr && <div className="text-[11px] text-gray-400">{durStr}</div>}
          {!isMissed && !durStr && <div className="text-[11px] text-green-500">{t("已接通")}</div>}
        </div>
        <button
          className="call-delete-btn opacity-0 pointer-events-none transition-opacity duration-150 flex h-7 w-7 items-center justify-center rounded-full text-gray-400 hover:bg-red-50 hover:text-red-500"
          onClick={(e) => { e.stopPropagation(); onDelete(record.id); }}
        >{"\u2715"}</button>
      </div>
    </div>
  );
}

export default function CallPage() {
  const { t } = useI18n();
  const [devices, setDevices] = useState<DeviceListItem[]>([]);
  const [selectedDevice, setSelectedDevice] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [history, setHistory] = useState<CallRecord[]>([]);
  const [searchQuery, setSearchQuery] = useState("");
  const [historyLoading, setHistoryLoading] = useState(false);
  const [dialNumber, setDialNumber] = useState("");
  const [isMobile, setIsMobile] = useState(() => window.innerWidth < MOBILE_BREAKPOINT);

  // Mobile-only state
  const [mobileView, setMobileView] = useState<"main" | "numpad" | "incall">("main");
  const [callPhase, setCallPhase] = useState<string>("idle");
  const [callDuration, setCallDuration] = useState(0);
  const [activeCallNumber, setActiveCallNumber] = useState("");
  const [showDtmf, setShowDtmf] = useState(false);
  const [muted, setMuted] = useState(false);
  const [calls, setCalls] = useState<CallInfo[]>([]);
  const [transport, setTransport] = useState<string>("");
  const durationRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const processorRef = useRef<ScriptProcessorNode | null>(null);

  const loadDevices = useCallback(async () => {
    try {
      const res = await api<DevicesResponse>("/devices");
      const running = (res.devices || []).filter((d) => d.running);
      setDevices(running);
      if (!selectedDevice && running.length > 0) setSelectedDevice(running[0].id);
      setError(null);
    } catch (e: any) {
      setError(e?.message || t("加载失败"));
    } finally {
      setLoading(false);
    }
  }, [selectedDevice, t]);

  usePolling(loadDevices, 10000);

  const loadHistory = useCallback(async () => {
    if (!selectedDevice) return;
    setHistoryLoading(true);
    try {
      const res = await getCallHistory(selectedDevice, searchQuery || undefined);
      setHistory(res.records || []);
    } catch {} finally { setHistoryLoading(false); }
  }, [selectedDevice, searchQuery]);

  useEffect(() => { loadHistory(); }, [loadHistory]);
  usePolling(loadHistory, 10000);

  useEffect(() => {
    const onResize = () => setIsMobile(window.innerWidth < MOBILE_BREAKPOINT);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  // Poll calls for mobile in-call detection
  const stopMobileAudio = useCallback(() => {
    if (wsRef.current) { wsRef.current.close(); wsRef.current = null; }
    if (processorRef.current) { processorRef.current.disconnect(); processorRef.current = null; }
    if (audioCtxRef.current) { audioCtxRef.current.close(); audioCtxRef.current = null; }
    if (streamRef.current) { streamRef.current.getTracks().forEach((t) => t.stop()); streamRef.current = null; }
  }, []);

  const startMobileAudio = useCallback((callId: string) => {
    if (!selectedDevice || wsRef.current) return;
    try {
      const ws = connectCallMedia(selectedDevice, callId);
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
          if (ws.readyState === WebSocket.OPEN) {
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
        ws.onclose = () => stopMobileAudio();
        ws.onerror = () => stopMobileAudio();
      }).catch((err) => {
        console.error("[CallPage] getUserMedia error:", err);
      });
    } catch (err) {
      console.error("[CallPage] startMobileAudio error:", err);
    }
  }, [selectedDevice, stopMobileAudio]);
  const loadCalls = useCallback(async () => {
    if (!selectedDevice || !isMobile) return;
    try {
      const res = await listCalls(selectedDevice);
      const callList = res.calls || [];
      setCalls(callList);
      setTransport(res.transport || "");
      const incoming = isIncomingCall(callList);
      const active = callList.find((c) => !c.endedAt);
      if (active) {
        const s = typeof active.state === "string" ? active.state.toLowerCase() : "";
        const n = typeof active.state === "number" ? active.state : -1;
        if (incoming) {
          setCallPhase("incoming");
          setActiveCallNumber(incoming.number || "");
          setMobileView("incall");
        } else if (s === "dialing" || s === "early_media" || n === 2 || n === 3) {
          setCallPhase("dialing");
          setActiveCallNumber(active.number || dialNumber);
          setMobileView("incall");
        } else if (s === "connected" || s === "active" || n === 0) {
          if (callPhase !== "connected") { setCallDuration(0); if (active.id) startMobileAudio(active.id); }
          setCallPhase("connected");
          setActiveCallNumber(active.number || dialNumber);
          setMobileView("incall");
        }
      } else if (callPhase !== "idle" && callPhase !== "incoming") {
        setCallPhase("idle");
        setMobileView("main");
        setCallDuration(0);
        setShowDtmf(false);
        setMuted(false);
      }
    } catch {}
  }, [selectedDevice, isMobile, callPhase, dialNumber, startMobileAudio, stopMobileAudio]);

  usePolling(loadCalls, 2000);

  // Duration counter
  useEffect(() => {
    if (callPhase === "connected") {
      durationRef.current = setInterval(() => setCallDuration((d) => d + 1), 1000);
    } else {
      if (durationRef.current) { clearInterval(durationRef.current); durationRef.current = null; }
    }
    return () => { if (durationRef.current) clearInterval(durationRef.current); };
  }, [callPhase]);

  const handleSelectNumber = useCallback((number: string) => {
    setDialNumber(number);
    if (isMobile) setMobileView("numpad");
  }, [isMobile]);

  const handleDeleteRecord = useCallback(async (id: number) => {
    if (!selectedDevice) return;
    try {
      await deleteCallRecord(selectedDevice, id);
      setHistory((prev) => prev.filter((r) => r.id !== id));
    } catch {}
  }, [selectedDevice]);


  const handleMobileDial = useCallback(async () => {
    const num = dialNumber.trim();
    if (!num || !selectedDevice) return;
    try {
      await dialCall(selectedDevice, num);
      setActiveCallNumber(num);
      setCallPhase("dialing");
      setCallDuration(0);
      setMobileView("incall");
    } catch (err: any) {
      setCallPhase("idle");
    }
  }, [dialNumber, selectedDevice]);

  const handleMobileHangup = useCallback(async () => {
    if (!selectedDevice) return;
    const active = calls.find((c) => !c.endedAt);
    try { await hangupCall(selectedDevice, active?.id); } catch {}
    stopMobileAudio();
    setCallPhase("idle");
    setMobileView("main");
    setCallDuration(0);
    setShowDtmf(false);
    setMuted(false);
  }, [selectedDevice, calls, stopMobileAudio]);

  const handleMobileAnswer = useCallback(async () => {
    if (!selectedDevice) return;
    const incoming = isIncomingCall(calls);
    try {
      await answerCall(selectedDevice, incoming?.id);
      if (incoming?.id) startMobileAudio(incoming.id);
      setCallPhase("connected");
      setCallDuration(0);
    } catch {}
  }, [selectedDevice, calls]);

  const handleMobileReject = useCallback(async () => {
    if (!selectedDevice) return;
    const incoming = isIncomingCall(calls);
    try { await rejectCall(selectedDevice, incoming?.id); } catch {}
    setCallPhase("idle");
    setMobileView("main");
  }, [selectedDevice, calls]);

  const handleMobileDtmf = useCallback(async (digit: string) => {
    if (!selectedDevice) return;
    const active = calls.find((c) => !c.endedAt);
    try { await sendDTMF(selectedDevice, digit, active?.id); } catch {}
  }, [selectedDevice, calls]);

  // Cleanup audio on unmount
  useEffect(() => { return () => stopMobileAudio(); }, [stopMobileAudio]);

  if (loading) return <div className="flex items-center justify-center py-20"><Spinner /></div>;
  if (error) return <ErrorState message={error} />;

  return (
    <div className="call-page workspace-page flex h-[calc(100vh-168px)] flex-col">
      <PageHeader title={t("设备通话")} />
      <div className="relative flex-1 overflow-hidden ui-card">
        <div className="call-main-layout h-full">
          {/* ---- Desktop: 3-column layout ---- */}
          <div className="call-device-col">
            <div className="border-b border-gray-100 p-4 dark:border-white/10">
              <div className="text-xs font-bold uppercase tracking-wider text-gray-500">{t("设备")}</div>
            </div>
            <div className="space-y-1 overflow-auto p-3">
              {devices.map((d) => (
                <button key={d.id} onClick={() => setSelectedDevice(d.id)}
                  className={cx("flex w-full items-center gap-3 rounded-xl border px-3 py-2.5 text-left transition-all", selectedDevice === d.id ? "border-indigo-200 bg-indigo-50/70 dark:border-indigo-500/30 dark:bg-indigo-500/10" : "border-transparent hover:bg-gray-50/60 dark:hover:bg-white/5")}>
                  <span className={cx("h-2 w-2 rounded-full shrink-0", d.healthy ? "bg-green-500" : "bg-red-500")} />
                  <div className="min-w-0">
                    <div className="truncate text-sm font-bold text-gray-800 dark:text-gray-100">{d.name || d.id}</div>
                    <div className="truncate text-[11px] text-gray-400">{d.id}</div>
                  </div>
                </button>
              ))}
            </div>
          </div>
          <div className="call-history-col">
            <div className="border-b border-gray-100 p-4 dark:border-white/10">
              <div className="text-xs font-bold uppercase tracking-wider text-gray-500 mb-3">{t("最近通话")}</div>
              <input type="text" value={searchQuery} onChange={(e) => setSearchQuery(e.target.value)} placeholder={t("搜索号码...")}
                className="w-full rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 text-sm outline-none focus:border-blue-400 focus:bg-white dark:border-gray-700 dark:bg-gray-800 dark:text-white" />
            </div>
            <div className="flex-1 overflow-auto">
              {historyLoading && history.length === 0 && <div className="flex items-center justify-center py-8"><Spinner className="h-5 w-5" /></div>}
              {!historyLoading && history.length === 0 && <div className="px-4 py-8 text-center text-sm text-gray-400">{searchQuery ? t("未找到匹配的通话记录") : t("暂无通话记录")}</div>}
              {history.map((r) => <CallHistoryItem key={r.id} record={r} t={t} onSelect={handleSelectNumber} onDelete={handleDeleteRecord} />)}
            </div>
          </div>
          <div className="call-dialer-col overflow-auto">
            {selectedDevice ? (
              isMobile ? (
                <div style={{ position: "absolute", width: 0, height: 0, overflow: "hidden", opacity: 0, pointerEvents: "none" }}>
                  <CallPanel deviceId={selectedDevice} externalNumber="" onNumberConsumed={() => {}} />
                </div>
              ) : (
                <CallPanel deviceId={selectedDevice} externalNumber={dialNumber} onNumberConsumed={() => setDialNumber("")} />
              )
            ) : (
              !isMobile && <div className="flex h-full items-center justify-center text-sm text-gray-400">{t("请先选择设备")}</div>
            )}
          </div>

          {/* ---- Mobile: new layout ---- */}
          {isMobile && (
            <div className="call-mobile-page">
              {/* Device chips */}
              <div className="call-mobile-devices">
                {devices.map((d) => (
                  <button key={d.id} onClick={() => setSelectedDevice(d.id)}
                    className={cx("call-mobile-chip", selectedDevice === d.id && "call-mobile-chip-active")}>
                    <span className="call-mobile-chip-dot" style={{ background: d.healthy ? "#22c55e" : "#ef4444" }} />
                    {d.name || d.id}
                  </button>
                ))}
              </div>

              {/* Search */}
              <div className="call-mobile-search">
                <div className="call-mobile-search-wrap">
                  <span className="call-mobile-search-icon">{"\u1F50D"}</span>
                  <input type="text" value={searchQuery} onChange={(e) => setSearchQuery(e.target.value)} placeholder={t("搜索通话记录...")} />
                </div>
              </div>

              {/* History list */}
              <div className="call-mobile-history">
                {historyLoading && history.length === 0 && <div className="flex items-center justify-center py-8"><Spinner className="h-5 w-5" /></div>}
                {!historyLoading && history.length === 0 && (
                  <div className="call-mobile-history-empty">
                    <div className="call-mobile-history-empty-icon">{"\u260E"}</div>
                    <div className="call-mobile-history-empty-text">{searchQuery ? t("未找到匹配的通话记录") : t("暂无通话记录")}</div>
                  </div>
                )}
                {history.map((r) => <CallHistoryItem key={r.id} record={r} t={t} onSelect={handleSelectNumber} onDelete={handleDeleteRecord} />)}
              </div>

              {/* Bottom dial bar */}
              <div className="call-mobile-dial-bar">
                <div className="call-mobile-dial-input" onClick={() => setMobileView("numpad")}
                  role="button" tabIndex={0}>
                  {dialNumber || <span style={{ color: "#94a3b8", fontWeight: 400 }}>{t("输入号码")}</span>}
                </div>
                {dialNumber && (
                  <button className="call-mobile-backspace-btn" onClick={() => setDialNumber((p) => p.slice(0, -1))}>{"\u232B"}</button>
                )}
                <button className="call-mobile-call-btn" onClick={handleMobileDial} disabled={!dialNumber.trim() || !selectedDevice}>{"\u2706"}</button>
              </div>

              {/* Numpad overlay */}
              {mobileView === "numpad" && (
                <div className="call-mobile-numpad-overlay">
                  <div className="call-mobile-numpad-header">
                    <button className="call-mobile-numpad-back" onClick={() => setMobileView("main")}>{"\u2190"} {t("返回")}</button>
                  </div>
                  <div className="call-mobile-numpad-display">
                    <div className="call-mobile-numpad-number">{dialNumber || <span style={{ color: "#94a3b8" }}>{t("输入号码")}</span>}</div>
                  </div>
                  <div className="call-mobile-numpad-grid">
                    {DTMF_KEYS.map((k) => (
                      <button key={k} className="call-mobile-numpad-key" onClick={() => setDialNumber((p) => p + k)}>
                        <span className="call-mobile-numpad-key-digit">{k}</span>
                        <span className="call-mobile-numpad-key-label">{DTMF_LABELS[k]}</span>
                      </button>
                    ))}
                  </div>
                  <div className="call-mobile-numpad-actions">
                    <button className="call-mobile-numpad-side-btn" onClick={() => setDialNumber((p) => p + "+")}>+</button>
                    <button className="call-mobile-numpad-call-btn" onClick={handleMobileDial} disabled={!dialNumber.trim()}>{"\u2706"}</button>
                    <button className="call-mobile-numpad-side-btn" onClick={() => setDialNumber((p) => p.slice(0, -1))} style={{ visibility: dialNumber ? "visible" : "hidden" }}>{"\u232B"}</button>
                  </div>
                </div>
              )}

              {/* In-call overlay */}
              {mobileView === "incall" && (
                <div className="call-mobile-incall">
                  <div style={{ marginTop: 40, marginBottom: 8 }}>
                    <div style={{ width: 80, height: 80, borderRadius: "50%", background: callPhase === "connected" ? "rgba(34,197,94,0.15)" : "rgba(245,158,11,0.15)", display: "flex", alignItems: "center", justifyContent: "center", margin: "0 auto" }}>
                      <span style={{ fontSize: 36 }}>{callPhase === "connected" ? "\u260E" : "\u1F4DE"}</span>
                    </div>
                  </div>
                  <div className="call-mobile-incall-number">{activeCallNumber || dialNumber}</div>
                  <div className="call-mobile-incall-status">
                    {callPhase === "incoming" && t("来电")}
                    {callPhase === "dialing" && t("正在拨号...")}
                    {callPhase === "ringing" && t("正在响铃...")}
                    {callPhase === "connected" && t("通话中")}
                  </div>
                  {callPhase === "connected" && <div className="call-mobile-incall-dur">{fmtDur(callDuration)}</div>}

                  <div className="call-mobile-incall-actions">
                    {callPhase === "incoming" && (
                      <>
                        <button className="call-mobile-incall-btn" style={{ background: "#ef4444", color: "#fff" }} onClick={handleMobileReject}>
                          {"\u2716"}<span className="call-mobile-incall-btn-label">{t("拒接")}</span>
                        </button>
                        <button className="call-mobile-incall-btn" style={{ background: "#22c55e", color: "#fff" }} onClick={handleMobileAnswer}>
                          {"\u2706"}<span className="call-mobile-incall-btn-label">{t("接听")}</span>
                        </button>
                      </>
                    )}
                    {callPhase === "connected" && (
                      <>
                        <button className="call-mobile-incall-btn" style={{ background: muted ? "#6366f1" : "#334155", color: "#fff" }} onClick={() => setMuted((m) => !m)}>
                          {muted ? "\u1F507" : "\u1F3A4"}<span className="call-mobile-incall-btn-label">{muted ? t("取消静音") : t("静音")}</span>
                        </button>
                        <button className="call-mobile-incall-btn" style={{ background: showDtmf ? "#6366f1" : "#334155", color: "#fff" }} onClick={() => setShowDtmf((s) => !s)}>
                          {"\u2328"}<span className="call-mobile-incall-btn-label">{t("键盘")}</span>
                        </button>
                      </>
                    )}
                  </div>

                  {/* DTMF grid during call */}
                  {callPhase === "connected" && showDtmf && (
                    <div className="call-mobile-numpad-grid" style={{ padding: "8px 32px", maxWidth: 280 }}>
                      {DTMF_KEYS.map((k) => (
                        <button key={k} className="call-mobile-numpad-key" onClick={() => handleMobileDtmf(k)}>
                          <span className="call-mobile-numpad-key-digit">{k}</span>
                        </button>
                      ))}
                    </div>
                  )}

                  <button className="call-mobile-incall-hangup" onClick={handleMobileHangup} style={{ marginTop: callPhase === "connected" && showDtmf ? 8 : 24 }}>
                    {"\u2716"}
                  </button>
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
