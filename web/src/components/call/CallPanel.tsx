import { useEffect, useMemo, useState, type ReactNode } from "react";
import {
  ArrowLeft,
  Delete,
  Globe2,
  Mic,
  MicOff,
  Phone,
  PhoneOff,
  RotateCcw,
  Server,
  ShieldCheck,
  Signal,
  Volume2,
} from "lucide-react";
import { Button, confirmDialog, message } from "../ui";
import { useI18n } from "../../lib/i18n";
import { cx } from "../../lib/utils";
import type { SimCard } from "../../types";
import { activeCardDevice } from "../cards/cardApi";
import { fmtDur, useCallSession, type CallPhase } from "./CallSessionContext";

const DTMF_KEYS = ["1","2","3","4","5","6","7","8","9","*","0","#"];
const DTMF_LABELS: Record<string, string> = {
  "1":"","2":"ABC","3":"DEF","4":"GHI","5":"JKL","6":"MNO",
  "7":"PQRS","8":"TUV","9":"WXYZ","*":"","0":"+","#":"",
};

function lineMode(card: SimCard, transport: string): string {
  if (transport === "vowifi" || card.policy?.runtimeMode === "pcsc" || card.policy?.vowifiEnabled) return "VoWiFi";
  return "蜂窝";
}

function statusLabel(phase: CallPhase, t: (value: string) => string): string {
  switch (phase) {
    case "dialing": return t("正在接通");
    case "ringing": return t("对方响铃");
    case "incoming": return t("来电");
    case "connected": return t("通话中");
    case "hanging_up": return t("正在挂断");
    case "ended": return t("通话已结束");
    default: return t("可拨号");
  }
}

function Step({ icon, label, value, tone = "ready" }: { icon: ReactNode; label: string; value: string; tone?: "ready" | "waiting" | "off" }) {
  return (
    <div className={cx("call-readiness-step", `is-${tone}`)}>
      <span className="call-readiness-icon">{icon}</span>
      <div>
        <small>{label}</small>
        <strong>{value}</strong>
      </div>
    </div>
  );
}

function Metric({ label, value, tone }: { label: string; value: ReactNode; tone?: "success" | "warning" | "danger" }) {
  return (
    <div className="call-metric">
      <span>{label}</span>
      <strong className={tone ? `is-${tone}` : undefined}>{value}</strong>
    </div>
  );
}

function RoundAction({
  icon,
  label,
  tone,
  active,
  disabled,
  onClick,
}: {
  icon: ReactNode;
  label: string;
  tone: "blue" | "green" | "red" | "dark";
  active?: boolean;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <div className="call-round-action">
      <button type="button" className={cx(`is-${tone}`, active && "is-active")} disabled={disabled} onClick={onClick} title={label}>
        {icon}
      </button>
      <span>{label}</span>
    </div>
  );
}

export interface CallPanelProps {
  card: SimCard;
  externalNumber?: string;
  onNumberConsumed?: () => void;
}

export function CallPanel({ card, externalNumber, onNumberConsumed }: CallPanelProps) {
  const { t } = useI18n();
  const {
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
  } = useCallSession();
  const [phoneNumber, setPhoneNumber] = useState("");
  const [dtmfLog, setDtmfLog] = useState("");
  const [busy, setBusy] = useState(false);

  const selectedDeviceId = activeCardDevice(card);
  const cardOperable = card.status === "active" && !!selectedDeviceId;
  const active = session && session.iccid === card.iccid ? session : null;
  const phase: CallPhase = active?.phase || "idle";
  const inCall = phase !== "idle" && phase !== "ended";
  const number = active?.number || phoneNumber.trim();
  const mode = lineMode(card, active?.transport || "");
  const localNumber = card.msisdn || card.policy?.customPhoneNumber || "";

  useEffect(() => {
    if (externalNumber) {
      setPhoneNumber(externalNumber);
      onNumberConsumed?.();
    }
  }, [externalNumber, onNumberConsumed]);

  const readiness = useMemo(() => ({
    audio: mediaState === "connected"
      ? t("已连接")
      : mediaState === "connecting"
        ? t("连接中")
        : mediaState === "error"
          ? t("不可用")
          : t("拨号时连接"),
    backend: active?.transport ? active.transport.toUpperCase() : cardOperable ? t("就绪") : t("不可用"),
    registration: cardOperable ? t("卡片在线") : t("卡片离线"),
    call: phase === "idle" ? t("未连接") : statusLabel(phase, t),
  }), [active?.transport, cardOperable, mediaState, phase, t]);

  async function run(action: () => Promise<void>, failure: string) {
    if (busy) return;
    setBusy(true);
    try {
      await action();
    } catch {
      message.error(t(failure));
    } finally {
      setBusy(false);
    }
  }

  async function handleDial() {
    const value = phoneNumber.trim();
    if (!value || !cardOperable) return;
    await run(() => dial(card, value), "拨号失败");
  }

  async function handleHangup() {
    const wasRinging = phase === "dialing" || phase === "ringing";
    if (wasRinging) {
      const confirmed = await confirmDialog(
        t("对方尚未接听，取消后本次呼叫将立即结束。"),
        t("取消呼叫"),
        { type: "warning", confirmText: t("取消呼叫"), cancelText: t("返回") },
      );
      if (!confirmed) return;
    }
    await run(() => hangup(wasRinging ? "cancelled" : "ended"), "挂断失败");
  }

  async function handleReject() {
    await run(reject, "拒接失败");
  }

  async function handleDtmf(digit: string) {
    setDtmfLog((value) => (value + digit).slice(-24));
    await run(() => sendDtmf(digit), "发送 DTMF 失败");
  }

  const callStatus = statusLabel(phase, t);

  return (
    <section className="call-reference-panel">
      <header className="call-line-hero">
        <div className="call-line-title">
          <span className="call-line-phone"><Phone /></span>
          <div>
            <h2>{card.displayName || card.carrier || card.iccid}</h2>
            <p>{card.binding?.deviceName || card.binding?.deviceId || t("远程通话线路")}</p>
          </div>
        </div>
        <span className={cx("call-line-status", phase !== "idle" && "is-live")}>{callStatus}{phase === "idle" ? ` · ${mode}` : ""}</span>
        <div className="call-line-summary">
          <div><small>{t("本机号码")}</small><strong>{localNumber || "—"}</strong></div>
          <div><small>{t("运营商")}</small><strong>{card.carrier || card.spn || "—"}</strong></div>
          <div><small>IMS {t("模式")}</small><strong>{mode}</strong></div>
        </div>
      </header>

      <div className="call-readiness-bar">
        <Step icon={<ShieldCheck />} label={t("浏览器音频")} value={readiness.audio} tone={mediaState === "error" ? "off" : mediaState === "connecting" ? "waiting" : "ready"} />
        <Step icon={<Server />} label={t("语音后端")} value={readiness.backend} tone={cardOperable ? "ready" : "off"} />
        <Step icon={<Signal />} label={t("运营商注册")} value={readiness.registration} tone={cardOperable ? "ready" : "off"} />
        <Step icon={<Globe2 />} label={t("通话连接")} value={readiness.call} tone={phase === "connected" ? "ready" : inCall ? "waiting" : "off"} />
      </div>

      <div className="call-stage">
        <div className="call-stage-primary">
          {phase === "idle" && (
            <div className="call-dialer">
              <div className="call-number-display">
                <input
                  value={phoneNumber}
                  onChange={(event) => setPhoneNumber(event.target.value)}
                  onKeyDown={(event) => { if (event.key === "Enter") void handleDial(); }}
                  placeholder={t("输入号码")}
                  inputMode="tel"
                  disabled={!cardOperable}
                />
                <button type="button" onClick={() => setPhoneNumber((value) => value.slice(0, -1))} disabled={!phoneNumber} title={t("退格")}>
                  <Delete />
                </button>
              </div>
              <div className="call-keypad">
                {DTMF_KEYS.map((key) => (
                  <button key={key} type="button" onClick={() => setPhoneNumber((value) => value + key)} disabled={!cardOperable}>
                    <strong>{key}</strong>
                    {DTMF_LABELS[key] ? <small>{DTMF_LABELS[key]}</small> : null}
                  </button>
                ))}
              </div>
              <button type="button" className="call-primary-button is-call" onClick={() => void handleDial()} disabled={!cardOperable || !phoneNumber.trim() || busy}>
                <Phone />
              </button>
              <p>{cardOperable ? t("经当前线路发起真实通话") : t("当前线路离线，无法拨号")}</p>
            </div>
          )}

          {phase === "incoming" && (
            <div className="call-state">
              <span className="call-state-icon is-incoming"><Phone /></span>
              <h3>{active?.number || t("未知号码")}</h3>
              <strong>{t("来电")}</strong>
              <p>{t("已响铃")} {fmtDur(ringDuration)}</p>
              <div className="call-state-actions">
                <RoundAction icon={<PhoneOff />} label={t("拒接")} tone="red" onClick={() => void handleReject()} />
                <RoundAction icon={<Phone />} label={t("接听")} tone="green" onClick={() => void run(answer, "接听失败")} />
              </div>
            </div>
          )}

          {(phase === "dialing" || phase === "ringing") && (
            <div className="call-state">
              <span className={cx("call-state-icon", phase === "ringing" ? "is-ringing" : "is-dialing")}>
                {phase === "ringing" ? <Volume2 /> : <Phone />}
              </span>
              <h3>{active?.number || number}</h3>
              <strong>{phase === "dialing" ? t("正在接通") : t("对方响铃")}</strong>
              <p>{phase === "ringing" ? `${t("已响铃")} ${fmtDur(ringDuration)}` : t("呼出电话")}</p>
              <button type="button" className="call-primary-button is-cancel" onClick={() => void handleHangup()} disabled={busy}>
                <PhoneOff />
                <span>{phase === "ringing" ? t("取消呼叫") : t("取消呼叫")}</span>
              </button>
            </div>
          )}

          {phase === "connected" && (
            <div className="call-connected">
              <div className="call-connected-heading">
                <span><Volume2 /> {t("通话中")}</span>
                <strong>{active?.number || number}</strong>
                <small>{t("通话时长")} {fmtDur(duration)}</small>
              </div>
              <div className="call-keypad is-compact">
                {DTMF_KEYS.map((key) => (
                  <button key={key} type="button" onClick={() => void handleDtmf(key)} disabled={busy}>
                    <strong>{key}</strong>
                    {DTMF_LABELS[key] ? <small>{DTMF_LABELS[key]}</small> : null}
                  </button>
                ))}
              </div>
              <div className="call-dtmf-log">{dtmfLog || "—"}</div>
              <div className="call-connected-actions">
                <RoundAction icon={muted ? <MicOff /> : <Mic />} label={muted ? t("取消静音") : t("静音")} tone="dark" active={muted} disabled={mediaState !== "connected"} onClick={toggleMute} />
                <RoundAction icon={<PhoneOff />} label={t("结束")} tone="red" onClick={() => void handleHangup()} />
                <RoundAction icon={<Volume2 />} label={t("免提")} tone="blue" active={speaker} disabled={mediaState !== "connected"} onClick={() => void run(toggleSpeaker, "切换免提失败")} />
              </div>
            </div>
          )}

          {phase === "hanging_up" && (
            <div className="call-state">
              <span className="call-state-icon is-hangup"><PhoneOff /></span>
              <h3>{active?.number || number}</h3>
              <strong>{t("正在挂断")}</strong>
              <p>{t("正在结束真实通话...")}</p>
            </div>
          )}

          {phase === "ended" && (
            <div className="call-state">
              <span className="call-state-icon is-ended"><PhoneOff /></span>
              <h3>{active?.number || number}</h3>
              <strong>{active?.outcome === "cancelled" ? t("呼叫已取消") : active?.outcome === "rejected" ? t("已拒接") : t("通话已结束")}</strong>
              <p>{t("本次通话时长")} {fmtDur(duration)}</p>
              <div className="call-ended-actions">
                <Button icon={<RotateCcw />} onClick={() => void run(redial, "重新拨号失败")}>{t("重新拨号")}</Button>
                <Button icon={<ArrowLeft />} onClick={resetEnded}>{t("拨打其他号码")}</Button>
              </div>
            </div>
          )}
        </div>

        <aside className="call-media-panel">
          <div className="call-media-head">
            <strong><Volume2 /> {t("媒体连接")}</strong>
            <span>{t("真实音频通道")}</span>
          </div>
          <div className="call-metric-grid">
            <Metric label={t("传输通道")} value={active?.transport ? active.transport.toUpperCase() : mode} />
            <Metric
              label={t("浏览器音频")}
              value={mediaState === "connected" ? t("已连接") : mediaState === "connecting" ? t("连接中") : mediaState === "error" ? t("连接失败") : t("未连接")}
              tone={mediaState === "connected" ? "success" : mediaState === "error" ? "danger" : "warning"}
            />
            <Metric label={t("麦克风")} value={mediaState === "connected" ? muted ? t("已静音") : t("正常") : t("未连接")} tone={muted ? "warning" : undefined} />
            <Metric label={t("免提")} value={mediaState === "connected" ? speaker ? t("已开启") : t("默认输出") : t("未连接")} tone={speaker ? "success" : undefined} />
            <Metric label={t("连接状态")} value={statusLabel(phase, t)} tone={phase === "connected" ? "success" : inCall ? "warning" : undefined} />
            <Metric label={t("通话时长")} value={phase === "connected" ? fmtDur(duration) : phase === "ringing" ? fmtDur(ringDuration) : "—"} />
            <Metric label="Call ID" value={active?.call?.id || "—"} />
            <Metric label={t("线路")} value={card.displayName || card.iccid} />
          </div>
          <p>{t("蜂窝通话的麦克风与扬声器由模块硬件处理时，浏览器媒体状态会保持未连接。")}</p>
        </aside>
      </div>

      <footer className="call-line-details">
        <div className="call-line-details-head">
          <strong><Signal /> {t("线路详情")}</strong>
          <span>{cardOperable ? t("线路可用") : t("线路离线")}</span>
        </div>
        <div className="call-line-details-grid">
          <div><small>IMS {t("模式")}</small><strong>{mode}</strong></div>
          <div><small>{t("卡片状态")}</small><strong>{cardOperable ? t("在线") : t("离线")}</strong></div>
          <div><small>{t("语音服务")}</small><strong>{cardOperable ? t("就绪") : t("不可用")}</strong></div>
          <div><small>{t("当前通话")}</small><strong>{statusLabel(phase, t)}</strong></div>
          <div><small>{t("主叫号码")}</small><strong>{localNumber || "—"}</strong></div>
          <div><small>Call ID</small><strong>{active?.call?.id || "—"}</strong></div>
        </div>
      </footer>
    </section>
  );
}
