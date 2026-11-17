import { useState } from "react";
import { CallRegular, CopyRegular, SendRegular, WarningRegular } from "@fluentui/react-icons";
import { api, ApiError } from "../../api";
import { Button, Input, message } from "../ui";
import { UssdLogEntry, type UssdLogItem } from "./UssdLogEntry";
import { tf, useI18n } from "../../lib/i18n";

interface UssdResult {
  status?: string;
  text?: string;
  rawText?: string;
  dcs?: number;
  sessionId?: string;
  channel?: string;
}

export function DeviceUssdTab({
  deviceId,
  iccid,
  operable = true,
}: {
  deviceId: string;
  iccid?: string;
  operable?: boolean;
}) {
  const { t } = useI18n();
  const [input, setInput] = useState("");
  const [timeoutMs, setTimeoutMs] = useState<number>(45000);
  const [loading, setLoading] = useState(false);
  const [sessionId, setSessionId] = useState("");
  const [channel, setChannel] = useState("");
  const [log, setLog] = useState<UssdLogItem[]>([]);

  const inSession = !!sessionId;
  const canOperate = operable && !!deviceId;
  const placeholder = inSession ? t("输入菜单选项数字") : t("例如 *100# 或菜单回复数字");
  const channelLabel = channel === "vowifi" ? "VoWiFi" : "CS";
  const responseText = log.length
    ? log.map((item) => {
        if (item.type === "req") return `> ${item.content}`;
        if (item.type === "sys") return `* ${item.content}`;
        return item.content;
      }).join("\n\n")
    : t("尚未执行 USSD 命令。输入短码开始会话。");

  function clearSession() {
    setSessionId("");
    setChannel("");
  }

  async function callUssd(command: string): Promise<UssdResult> {
    const body = inSession
      ? { sessionId, input: command, timeoutMs: timeoutMs || 45000 }
      : { command, timeoutMs: timeoutMs || 45000 };
    const query = iccid ? `?iccid=${encodeURIComponent(iccid)}` : "";
    const path = inSession
      ? `/devices/${encodeURIComponent(deviceId)}/actions/ussd/continue${query}`
      : `/devices/${encodeURIComponent(deviceId)}/actions/ussd${query}`;
    const res = await api<{ result?: Record<string, unknown>; sessionId?: string; channel?: string }>(path, { method: "POST", body });
    const r = (res?.result || {}) as Record<string, unknown>;
    return {
      status: r.status as string | undefined,
      text: (r.text as string) || "",
      rawText: ((r.raw as string) || (r.rawText as string) || (r.rawXml as string) || "") as string,
      dcs: r.dcs as number | undefined,
      sessionId: res?.sessionId || (r.sessionId as string) || "",
      channel: res?.channel || "",
    };
  }

  async function send() {
    const command = String(input || "").trim();
    if (!command || loading || !canOperate) return;
    setLog((prev) => [...prev, { ts: Date.now(), type: "req", content: command }]);
    setLoading(true);
    setInput("");
    try {
      const v = await callUssd(command);
      if (v.channel) setChannel(v.channel);
      const text = v.text || v.rawText || t("[空响应]");
      if (v.status === "failed") {
        setLog((prev) => [...prev, { ts: Date.now(), type: "err", content: tf("[网络不支持/无响应]\n{text}", { text }), dcs: v.dcs, channel: v.channel }]);
        clearSession();
      } else if (v.status === "terminated") {
        setLog((prev) => [...prev, { ts: Date.now(), type: "err", content: tf("[被网络终止]\n{text}", { text }), dcs: v.dcs, channel: v.channel }]);
        clearSession();
      } else {
        setLog((prev) => [...prev, { ts: Date.now(), type: "res", content: text, dcs: v.dcs, channel: v.channel }]);
        if (v.status === "awaiting_input" && v.sessionId) setSessionId(v.sessionId);
        else clearSession();
      }
    } catch (e) {
      setLog((prev) => [...prev, { ts: Date.now(), type: "err", content: e instanceof Error ? e.message : t("请求异常") }]);
      // A failed turn may already have reached the network. Keep its binding
      // so the user can cancel it instead of accidentally starting a new query.
      if (e instanceof ApiError && e.code === "ussd_session_not_found") clearSession();
    } finally {
      setLoading(false);
    }
  }

  async function cancelSession() {
    if (!sessionId || loading) return;
    setLoading(true);
    try {
      const query = iccid ? `?iccid=${encodeURIComponent(iccid)}` : "";
      await api(`/devices/${encodeURIComponent(deviceId)}/actions/ussd/cancel${query}`, {
        method: "POST",
        body: { sessionId },
      });
      setLog((prev) => [...prev, { ts: Date.now(), type: "sys", content: t("会话已手动取消") }]);
      clearSession();
    } catch (e) {
      setLog((prev) => [...prev, { ts: Date.now(), type: "err", content: e instanceof Error ? e.message : t("请求异常") }]);
      if (e instanceof ApiError && e.code === "ussd_session_not_found") clearSession();
    } finally {
      setLoading(false);
    }
  }

  async function copyResponse() {
    try {
      await navigator.clipboard.writeText(responseText);
      message.success(t("USSD 响应已复制"));
    } catch {
      message.error(t("复制 USSD 响应失败"));
    }
  }

  return (
    <section className="terminal-module terminal-module-ussd">
      <div className="terminal-panel-header">
        <div className="terminal-panel-title">
          <CallRegular />
          <strong>{t("USSD 会话")}</strong>
        </div>
        <div className="terminal-panel-meta">
          {inSession ? (
            <>
              <span className="terminal-session-live"><i />{t("多轮会话中")}</span>
              <Button variant="warning" size="small" plain onClick={cancelSession} disabled={loading}>
                {t("取消会话")}
              </Button>
            </>
          ) : (
            <span>{canOperate ? `${t("设备在线")} · ${channelLabel}` : `${t("设备离线")} · ${channelLabel}`}</span>
          )}
        </div>
      </div>

      <p className="terminal-description">
        {t("单次等待最长 {seconds} 秒；菜单空闲 180 秒后自动结束").replace("{seconds}", String(Math.max(1, Math.round(timeoutMs / 1000))))}
      </p>

      <div className="terminal-response">
        <div className="terminal-response-head">
          <span>{t("USSD 响应预览")}</span>
          <Button
            size="small"
            plain
            disabled={!log.length}
            onClick={() => void copyResponse()}
            icon={<CopyRegular />}
          >
            {t("复制")}
          </Button>
        </div>
        <div className="terminal-response-body">
          {log.length ? (
            log.map((item, i) => <UssdLogEntry key={`${item.ts}-${i}`} item={item} />)
          ) : (
            <pre>{t("尚未执行 USSD 命令。输入短码开始会话。")}</pre>
          )}
          {loading ? <div className="terminal-waiting">{t("等待网络响应...")}</div> : null}
        </div>
      </div>

      {!canOperate ? (
        <div className="terminal-disabled-note">
          <WarningRegular />
          <span><strong>{t("USSD 不可用")}</strong> · {t("当前卡片离线或设备不可用，不能发送或继续会话。")}</span>
        </div>
      ) : null}

      <label className="terminal-field-label">{inSession ? t("菜单回复") : t("USSD 短码")}</label>
      <div className="terminal-input-row">
        <Input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder={placeholder}
          disabled={loading || !canOperate}
          className={!canOperate ? "terminal-input-blocked" : ""}
          title={!canOperate ? t("当前设备或卡片不可用，USSD 无法输入") : t("输入 USSD 短码或菜单回复")}
          onKeyDown={(e) => {
            if (e.key === "Enter") void send();
          }}
        />
        <Button
          variant="primary"
          loading={loading}
          disabled={!input.trim() || !canOperate}
          onClick={() => void send()}
          icon={<SendRegular />}
        >
          {inSession ? t("回复") : t("发送")}
        </Button>
      </div>

      <details className="terminal-advanced">
        <summary>{t("高级选项")}</summary>
        <div className="terminal-advanced-grid">
          <label>
            <span>{t("超时(ms)")}</span>
            <Input
              type="number"
              inputMode="numeric"
              value={String(timeoutMs)}
              disabled={!canOperate}
              className={!canOperate ? "terminal-input-blocked" : ""}
              onChange={(e) => setTimeoutMs(Number(e.target.value))}
              placeholder="45000"
            />
          </label>
          <Button
            disabled={!log.length && !inSession}
            onClick={() => {
              setLog([]);
              clearSession();
            }}
          >
            {t("清空记录")}
          </Button>
        </div>
      </details>
    </section>
  );
}
