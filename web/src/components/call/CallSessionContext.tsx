import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { activeCardDevice, listCards } from "../cards/cardApi";
import type { SimCard } from "../../types";
import {
  answerCall,
  connectCallMedia,
  dialCall,
  findIncomingCall,
  hangupCall,
  listCalls,
  rejectCall,
  sendDTMF,
  type CallInfo,
} from "./callApi";

export type CallPhase =
  | "idle"
  | "dialing"
  | "ringing"
  | "incoming"
  | "connected"
  | "hanging_up"
  | "ended";

export type CallOutcome = "ended" | "cancelled" | "rejected" | "failed";
export type CallMediaState = "idle" | "connecting" | "connected" | "unavailable" | "error";

export interface CallSession {
  phase: CallPhase;
  outcome?: CallOutcome;
  card?: SimCard;
  deviceId: string;
  iccid: string;
  transport: string;
  call?: CallInfo;
  number: string;
  startedAt: number;
  ringStartedAt: number;
  connectedAt: number;
  endedAt: number;
}

interface CallSessionContextValue {
  session: CallSession | null;
  duration: number;
  ringDuration: number;
  muted: boolean;
  speaker: boolean;
  mediaState: CallMediaState;
  dial: (card: SimCard, number: string) => Promise<void>;
  answer: () => Promise<void>;
  reject: () => Promise<void>;
  hangup: (outcome?: CallOutcome) => Promise<void>;
  sendDtmf: (digit: string) => Promise<void>;
  toggleMute: () => void;
  toggleSpeaker: () => Promise<void>;
  redial: () => Promise<void>;
  resetEnded: () => void;
}

const CallSessionContext = createContext<CallSessionContextValue | null>(null);

function isIncoming(call?: CallInfo): boolean {
  if (!call) return false;
  const direction = typeof call.direction === "string" ? call.direction.toLowerCase() : call.direction;
  return direction === "incoming" || direction === 1;
}

function isLive(call?: CallInfo): boolean {
  if (!call || call.endedAt) return false;
  const state = typeof call.state === "string" ? call.state.toLowerCase() : call.state;
  return state !== "ended" && state !== "failed" && state !== "released";
}

function deriveOutgoingPhase(call?: CallInfo): CallPhase {
  if (!call) return "dialing";
  const state = typeof call.state === "string" ? call.state.toLowerCase() : call.state;
  if (state === "connected" || state === "active" || state === 0 || state === 1) return "connected";
  if (state === "ringing" || state === "alerting" || state === 3) return "ringing";
  return "dialing";
}

function firstLiveCall(calls: CallInfo[]): CallInfo | undefined {
  return calls.find((call) => isLive(call));
}

function callKey(call?: CallInfo, iccid = ""): string {
  return call?.id || `${iccid}:${call?.index ?? call?.number ?? ""}`;
}

function fmtDur(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

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
    gain.gain.value = 0.22;
    gain.connect(ctx.destination);
    for (const frequency of freqs) {
      const oscillator = ctx.createOscillator();
      oscillator.frequency.value = frequency;
      oscillator.connect(gain);
      oscillator.start();
      oscillator.stop(ctx.currentTime + duration);
    }
    window.setTimeout(() => void ctx.close(), (duration + 0.1) * 1000);
  } catch {
    // DTMF is still sent to the device even if local tone playback is unavailable.
  }
}

export function CallSessionProvider({ children }: { children: ReactNode }) {
  const [cards, setCards] = useState<SimCard[]>([]);
  const [session, setSession] = useState<CallSession | null>(null);
  const [muted, setMuted] = useState(false);
  const [speaker, setSpeaker] = useState(false);
  const [mediaState, setMediaState] = useState<CallMediaState>("idle");
  const [now, setNow] = useState(() => Date.now());

  const cardsRef = useRef(cards);
  const sessionRef = useRef(session);
  const mutedRef = useRef(muted);
  const speakerRef = useRef(speaker);
  const transitionUntilRef = useRef(0);
  const answeredRef = useRef(false);
  const lastCardRef = useRef<SimCard | null>(null);
  const lastNumberRef = useRef("");
  const wsRef = useRef<WebSocket | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const processorRef = useRef<ScriptProcessorNode | null>(null);
  const remoteGainRef = useRef<GainNode | null>(null);

  cardsRef.current = cards;
  sessionRef.current = session;
  mutedRef.current = muted;
  speakerRef.current = speaker;

  const stopAudio = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.onclose = null;
      wsRef.current.onerror = null;
      wsRef.current.close();
      wsRef.current = null;
    }
    processorRef.current?.disconnect();
    processorRef.current = null;
    remoteGainRef.current?.disconnect();
    remoteGainRef.current = null;
    void audioCtxRef.current?.close();
    audioCtxRef.current = null;
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
    setMediaState("idle");
  }, []);

  const applyOutput = useCallback(async (enabled: boolean) => {
    const context = audioCtxRef.current as (AudioContext & { setSinkId?: (id: string) => Promise<void> }) | null;
    const gain = remoteGainRef.current;
    if (gain) gain.gain.value = enabled ? 1 : 0.35;
    if (!context?.setSinkId || !navigator.mediaDevices?.enumerateDevices) return;
    try {
      const devices = await navigator.mediaDevices.enumerateDevices();
      const outputs = devices.filter((device) => device.kind === "audiooutput");
      const speakerOutput = outputs.find((device) => /speaker|扬声器|loudspeaker/i.test(device.label) && !/headphone|耳机|earpiece|听筒/i.test(device.label));
      await context.setSinkId(enabled ? speakerOutput?.deviceId || "" : "");
    } catch {
      // Gain control remains active when the browser does not expose output routing.
    }
  }, []);

  const startAudio = useCallback((deviceId: string, callId: string, iccid: string) => {
    if (wsRef.current) return;
    setMediaState("connecting");
    try {
      const ws = connectCallMedia(deviceId, callId, iccid);
      wsRef.current = ws;
      void navigator.mediaDevices.getUserMedia({ audio: true }).then(async (stream) => {
        if (wsRef.current !== ws) return;
        streamRef.current = stream;
        const context = new AudioContext();
        audioCtxRef.current = context;
        const nativeRate = context.sampleRate;
        const targetRate = 8000;
        const source = context.createMediaStreamSource(stream);
        const processor = context.createScriptProcessor(4096, 1, 1);
        const remoteGain = context.createGain();
        const localMuteGain = context.createGain();
        processorRef.current = processor;
        remoteGainRef.current = remoteGain;
        localMuteGain.gain.value = 0;
        remoteGain.gain.value = speakerRef.current ? 1 : 0.35;
        processor.onaudioprocess = (event) => {
          if (ws.readyState !== WebSocket.OPEN) return;
          const data = event.inputBuffer.getChannelData(0);
          const outLength = Math.round(data.length * targetRate / nativeRate);
          const pcm16 = new Int16Array(outLength);
          for (let i = 0; i < outLength; i++) {
            const sourceIndex = i * nativeRate / targetRate;
            const low = Math.floor(sourceIndex);
            const high = Math.min(low + 1, data.length - 1);
            const fraction = sourceIndex - low;
            const sample = data[low] * (1 - fraction) + data[high] * fraction;
            pcm16[i] = mutedRef.current ? 0 : Math.max(-32768, Math.min(32767, sample * 32768));
          }
          ws.send(pcm16.buffer);
        };
        source.connect(processor);
        processor.connect(localMuteGain);
        localMuteGain.connect(context.destination);
        ws.onmessage = (event) => {
          if (!(event.data instanceof Blob)) return;
          void event.data.arrayBuffer().then((buffer) => {
            const samples = new Int16Array(buffer);
            const outLength = Math.round(samples.length * nativeRate / targetRate);
            const audioBuffer = context.createBuffer(1, outLength, nativeRate);
            const output = audioBuffer.getChannelData(0);
            for (let i = 0; i < outLength; i++) {
              const sourceIndex = i * targetRate / nativeRate;
              const low = Math.floor(sourceIndex);
              const high = Math.min(low + 1, samples.length - 1);
              const fraction = sourceIndex - low;
              output[i] = (samples[low] / 32768) * (1 - fraction) + (samples[high] / 32768) * fraction;
            }
            const source = context.createBufferSource();
            source.buffer = audioBuffer;
            source.connect(remoteGain);
            remoteGain.connect(context.destination);
            source.start();
          });
        };
        ws.onclose = () => stopAudio();
        ws.onerror = () => {
          setMediaState("error");
          stopAudio();
        };
        await applyOutput(speakerRef.current);
        setMediaState("connected");
      }).catch(() => setMediaState("error"));
    } catch {
      setMediaState("error");
    }
  }, [applyOutput, stopAudio]);

  const refresh = useCallback(async () => {
    try {
      const cardList = await listCards();
      setCards(cardList);
      const activeCards = cardList.filter((card) => card.status === "active" && activeCardDevice(card));
      const results = await Promise.allSettled(activeCards.map(async (card) => ({
        card,
        deviceId: activeCardDevice(card)!,
        response: await listCalls(activeCardDevice(card)!, card.iccid),
      })));
      const candidates: Array<{ card: SimCard; deviceId: string; transport: string; call: CallInfo }> = [];
      for (const result of results) {
        if (result.status !== "fulfilled") continue;
        const incoming = findIncomingCall(result.value.response.calls || []);
        const live = incoming || firstLiveCall(result.value.response.calls || []);
        if (live) candidates.push({
          card: result.value.card,
          deviceId: result.value.deviceId,
          transport: result.value.response.transport || "",
          call: live,
        });
      }
      candidates.sort((a, b) => Number(isIncoming(b.call)) - Number(isIncoming(a.call)));
      const current = candidates.find((item) => callKey(item.call, item.card.iccid) === callKey(sessionRef.current?.call, sessionRef.current?.iccid)) || candidates[0];
      if (!current) {
        const previous = sessionRef.current;
        if (previous && Date.now() < transitionUntilRef.current) return;
        if (previous && previous.phase !== "idle" && previous.phase !== "ended") {
          setSession({ ...previous, phase: "ended", outcome: previous.outcome || "ended", endedAt: previous.endedAt || Date.now() });
          transitionUntilRef.current = Date.now() + 2500;
        }
        return;
      }
      const incoming = isIncoming(current.call) && !answeredRef.current;
      const phase: CallPhase = incoming ? "incoming" : deriveOutgoingPhase(current.call);
      const previous = sessionRef.current;
      const connectedAt = phase === "connected" ? previous?.connectedAt || Date.now() : previous?.connectedAt || 0;
      const ringStartedAt = phase === "ringing" || phase === "incoming" ? previous?.ringStartedAt || Date.now() : previous?.ringStartedAt || 0;
      const next: CallSession = {
        phase,
        card: current.card,
        deviceId: current.deviceId,
        iccid: current.card.iccid,
        transport: current.transport,
        call: current.call,
        number: current.call.number || previous?.number || "",
        startedAt: previous?.startedAt || Date.now(),
        ringStartedAt,
        connectedAt,
        endedAt: 0,
      };
      setSession(next);
      if (phase === "connected" && current.transport === "vowifi" && current.call.id && wsRef.current == null) {
        startAudio(current.deviceId, current.call.id, current.card.iccid);
      }
      if (phase !== "connected") stopAudio();
    } catch {
      // Keep the current real call state on transient API errors.
    }
  }, [startAudio, stopAudio]);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => () => stopAudio(), [stopAudio]);

  const dial = useCallback(async (card: SimCard, number: string) => {
    const deviceId = activeCardDevice(card);
    if (!deviceId || card.status !== "active") throw new Error("CARD_OFFLINE");
    const value = number.trim();
    if (!value) throw new Error("EMPTY_NUMBER");
    lastCardRef.current = card;
    lastNumberRef.current = value;
    answeredRef.current = false;
    transitionUntilRef.current = Date.now() + 5000;
    setMuted(false);
    setSpeaker(false);
    setSession({
      phase: "dialing",
      card,
      deviceId,
      iccid: card.iccid,
      transport: "",
      number: value,
      startedAt: Date.now(),
      ringStartedAt: 0,
      connectedAt: 0,
      endedAt: 0,
    });
    await dialCall(deviceId, value, 0, card.iccid);
  }, []);

  const answer = useCallback(async () => {
    const target = sessionRef.current;
    if (!target || target.phase !== "incoming") return;
    answeredRef.current = true;
    transitionUntilRef.current = Date.now() + 5000;
    await answerCall(target.deviceId, target.call?.id, target.iccid);
    setSession({ ...target, phase: "connected", connectedAt: Date.now() });
    if (target.transport === "vowifi" && target.call?.id) startAudio(target.deviceId, target.call.id, target.iccid);
  }, [startAudio]);

  const finish = useCallback(async (outcome: CallOutcome, reject = false) => {
    const target = sessionRef.current;
    if (!target) return;
    transitionUntilRef.current = Date.now() + 3500;
    setSession({ ...target, phase: "hanging_up" });
    stopAudio();
    try {
      if (reject) await rejectCall(target.deviceId, target.call?.id, target.iccid);
      else await hangupCall(target.deviceId, target.call?.id, target.iccid);
      setSession({ ...target, phase: "ended", outcome, endedAt: Date.now() });
    } catch {
      setSession({ ...target, phase: "ended", outcome: "failed", endedAt: Date.now() });
      throw new Error("HANGUP_FAILED");
    } finally {
      answeredRef.current = false;
    }
  }, [stopAudio]);

  const hangup = useCallback(async (outcome: CallOutcome = "ended") => {
    await finish(outcome, false);
  }, [finish]);

  const reject = useCallback(async () => {
    await finish("rejected", true);
  }, [finish]);

  const sendDtmf = useCallback(async (digit: string) => {
    const target = sessionRef.current;
    if (!target || target.phase !== "connected") return;
    playDtmfTone(digit);
    await sendDTMF(target.deviceId, digit, target.call?.id, target.iccid);
  }, []);

  const toggleMute = useCallback(() => {
    setMuted((current) => {
      const next = !current;
      streamRef.current?.getAudioTracks().forEach((track) => { track.enabled = !next; });
      return next;
    });
  }, []);

  const toggleSpeaker = useCallback(async () => {
    const next = !speakerRef.current;
    setSpeaker(next);
    await applyOutput(next);
  }, [applyOutput]);

  const redial = useCallback(async () => {
    const card = lastCardRef.current || sessionRef.current?.card;
    const number = lastNumberRef.current || sessionRef.current?.number;
    if (card && number) await dial(card, number);
  }, [dial]);

  const resetEnded = useCallback(() => {
    const current = sessionRef.current;
    if (current?.phase === "ended") setSession(null);
  }, []);

  const duration = session?.connectedAt ? Math.max(0, Math.floor((now - session.connectedAt) / 1000)) : 0;
  const ringDuration = session?.ringStartedAt ? Math.max(0, Math.floor((now - session.ringStartedAt) / 1000)) : 0;

  const value = useMemo<CallSessionContextValue>(() => ({
    session,
    duration,
    ringDuration,
    muted,
    speaker,
    mediaState,
    dial,
    answer,
    reject,
    hangup,
    sendDtmf,
    toggleMute,
    toggleSpeaker,
    redial,
    resetEnded,
  }), [answer, dial, duration, hangup, mediaState, muted, redial, reject, resetEnded, ringDuration, sendDtmf, session, speaker, toggleMute, toggleSpeaker]);

  return <CallSessionContext.Provider value={value}>{children}</CallSessionContext.Provider>;
}

export function useCallSession(): CallSessionContextValue {
  const value = useContext(CallSessionContext);
  if (!value) throw new Error("useCallSession must be used within CallSessionProvider");
  return value;
}

export { fmtDur };
