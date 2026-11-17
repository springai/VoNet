import { useEffect } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { ExternalLink, Mic, MicOff, Phone, PhoneOff } from "lucide-react";
import { useI18n } from "../../lib/i18n";
import { cx } from "../../lib/utils";
import { fmtDur, useCallSession } from "./CallSessionContext";

export function GlobalCallWidget() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const location = useLocation();
  const {
    session,
    duration,
    ringDuration,
    muted,
    mediaState,
    answer,
    reject,
    hangup,
    toggleMute,
    resetEnded,
  } = useCallSession();

  useEffect(() => {
    if (session?.phase === "ended" && location.pathname !== "/call") resetEnded();
  }, [location.pathname, resetEnded, session?.phase]);

  if (!session || session.phase === "idle" || session.phase === "ended" || location.pathname === "/call") return null;

  const number = session.number || t("未知号码");
  const line = session.card?.displayName || session.card?.carrier || session.card?.iccid || t("通话线路");
  const connected = session.phase === "connected";
  const incoming = session.phase === "incoming";
  const canControl = session.phase !== "hanging_up";

  return (
    <aside className={cx("global-call-widget", connected && "is-connected", incoming && "is-incoming")} aria-live="polite">
      <button
        type="button"
        className="global-call-main"
        onClick={() => navigate(`/call?iccid=${encodeURIComponent(session.iccid)}`)}
      >
        <span className="global-call-icon"><Phone /></span>
        <span className="global-call-copy">
          <strong>{number}</strong>
          <small>
            {incoming ? t("来电") : session.phase === "dialing" ? t("正在接通") : session.phase === "ringing" ? t("对方响铃") : session.phase === "hanging_up" ? t("正在挂断") : t("通话中")}
            {" · "}
            {connected ? fmtDur(duration) : session.phase === "ringing" || incoming ? fmtDur(ringDuration) : line}
          </small>
        </span>
        <ExternalLink />
      </button>
      <div className="global-call-actions">
        {connected ? (
          <button type="button" className={cx("is-mute", muted && "is-active")} onClick={toggleMute} disabled={!canControl || mediaState !== "connected"} title={muted ? t("取消静音") : t("静音")}>
            {muted ? <MicOff /> : <Mic />}
          </button>
        ) : null}
        {incoming ? (
          <button type="button" className="is-answer" onClick={() => void answer()} title={t("接听")}>
            <Phone />
          </button>
        ) : null}
        {canControl ? (
          <button
            type="button"
            className="is-hangup"
            onClick={() => void (incoming ? reject() : hangup(session.phase === "dialing" || session.phase === "ringing" ? "cancelled" : "ended"))}
            title={incoming ? t("拒接") : t("挂断")}
          >
            <PhoneOff />
          </button>
        ) : null}
      </div>
    </aside>
  );
}
