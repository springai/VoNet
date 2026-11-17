import { useState } from "react";
import { CopyRegular, SendRegular, WarningRegular, WindowConsoleRegular } from "@fluentui/react-icons";
import { api } from "../../api";
import { Button, Input, Select, Switch, message } from "../ui";
import { AT_COMMAND_GROUPS } from "./atCommands";
import { type AtLogItem } from "./AtLogEntry";
import { useI18n } from "../../lib/i18n";

export interface DeviceAtTabProps {
  deviceId: string;
  iccid?: string;
  backendMode?: string;
  atPort?: string;
  running: boolean;
  reader?: boolean;
}

export function DeviceAtTab({ deviceId, iccid, backendMode, atPort, running, reader = false }: DeviceAtTabProps) {
  const { t } = useI18n();
  const [cmd, setCmd] = useState("");
  const [template, setTemplate] = useState("");
  const [timeoutMs, setTimeoutMs] = useState<number>(10000);
  const [sending, setSending] = useState(false);
  const [force, setForce] = useState(false);
  const [log, setLog] = useState<AtLogItem[]>([]);

  const hasAtPort = String(atPort || "").trim().length > 0;
  const usable = !!deviceId && !!running && hasAtPort && !reader;
  const unavailableTitle = reader
    ? t("读卡器不支持 AT")
    : running
      ? (hasAtPort ? t("AT 终端暂不可用") : t("当前设备没有可用 AT 口"))
      : t("当前设备未运行");
  const unavailableDesc = reader
    ? t("USB SIM 读卡器仅通过 Wi-Fi Calling 提供服务，不暴露 AT 串口。")
    : running
    ? !hasAtPort && backendMode === "qmi"
      ? t("设备当前处于纯 QMI 模式，但没有解析到可用的 AT 口，因此无法提供 AT 串口终端。")
      : hasAtPort
        ? t("当前设备暂时无法提供 AT 串口终端，请稍后重试。")
        : t("设备当前没有可用的 AT 口，因此无法提供 AT 串口终端。")
    : t("设备当前未启动，AT 终端暂时不可用。待设备运行后，如果存在可用的 AT 口，即可在这里直接发送 AT 指令。");

  async function send(commandOverride?: string) {
    const command = String(commandOverride ?? cmd ?? "").trim();
    if (!command || !usable || sending) return;
    setSending(true);
    if (!commandOverride) setCmd("");
    try {
      const query = iccid ? `?iccid=${encodeURIComponent(iccid)}` : "";
      const res = await api<{ ok?: boolean; response?: string; result?: string }>(
        `/devices/${encodeURIComponent(deviceId)}/actions/at${query}`,
        {
        method: "POST",
        body: { cmd: command, timeoutMs: timeoutMs || 10000, force },
        },
      );
      setLog((prev) => [
        ...prev,
        { ts: Date.now(), cmd: command, ok: res?.ok ?? true, response: res?.response ?? res?.result ?? JSON.stringify(res ?? {}) },
      ]);
    } catch (e) {
      setLog((prev) => [...prev, { ts: Date.now(), cmd: command, ok: false, response: e instanceof Error ? e.message : t("请求异常") }]);
    } finally {
      setSending(false);
    }
  }

  const responseText = log.length
    ? log.map((item) => `> ${item.cmd}\n${item.ok ? "" : "[ERROR] "}${item.response}`).join("\n\n")
    : t("尚未执行命令。点击上方快捷查询，或输入 AT 命令。");

  async function copyResponse() {
    try {
      await navigator.clipboard.writeText(responseText);
      message.success(t("AT 响应已复制"));
    } catch {
      message.error(t("复制 AT 响应失败"));
    }
  }

  const quickCommands = [
    { label: t("信号查询 (CSQ)"), value: "AT+CSQ" },
    { label: t("SIM 状态 (CPIN)"), value: "AT+CPIN?" },
    { label: t("设备信息 (ATI)"), value: "ATI" },
  ];

  return (
    <section className="terminal-module terminal-module-at">
      <div className="terminal-panel-header">
        <div className="terminal-panel-title">
          <WindowConsoleRegular />
          <strong>{t("AT 终端交互")}</strong>
        </div>
        <div className="terminal-panel-meta">
          <span>{t("串行通信")}</span>
          <Switch checked={force} onChange={setForce} ariaLabel={t("强制发送 AT 指令")} size="small" />
        </div>
      </div>

      <div className="terminal-command-presets">
        {quickCommands.map((item) => (
          <button
            key={item.value}
            type="button"
            disabled={!usable || sending}
            onClick={() => void send(item.value)}
            title={usable ? item.value : unavailableTitle}
          >
            {item.label}
          </button>
        ))}
      </div>

      <div className="terminal-response">
        <div className="terminal-response-head">
          <span>{t("ttyUSB 响应预览")}</span>
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
        <pre>{sending ? t("等待模组响应...") : responseText}</pre>
      </div>

      <div className="terminal-input-row">
        <Input
          value={cmd}
          onChange={(e) => setCmd(e.target.value)}
          placeholder={t("输入自定义 AT 指令，例如 AT+COPS?...")}
          disabled={!usable || sending}
          className={!usable ? "terminal-input-blocked" : ""}
          title={!usable ? `${unavailableTitle}：${unavailableDesc}` : t("输入 AT 命令")}
          onKeyDown={(e) => {
            if (e.key === "Enter") void send();
          }}
        />
        <Button
          variant="primary"
          loading={sending}
          disabled={!usable || !cmd.trim()}
          onClick={() => void send()}
          icon={<SendRegular />}
        >
          {t("发送")}
        </Button>
      </div>

      {!usable ? (
        <div className="terminal-disabled-note">
          <WarningRegular />
          <span><strong>{unavailableTitle}</strong> · {unavailableDesc}</span>
        </div>
      ) : null}

      <details className="terminal-advanced">
        <summary>{t("高级选项")}</summary>
        <div className="terminal-advanced-grid">
          <label>
            <span>{t("快捷指令模板")}</span>
            <Select
              value={template}
              disabled={!usable}
              onChange={(v) => {
                setTemplate(v);
                if (v) setCmd(v);
              }}
              placeholder={t("选择常用命令（可选）")}
              options={AT_COMMAND_GROUPS.flatMap((g) => g.items.map((it) => ({ value: it.value, label: `${t(g.label)} · ${t(it.label)}` })))}
            />
          </label>
          <label>
            <span>{t("超时(ms)")}</span>
            <Input
              type="number"
              inputMode="numeric"
              value={String(timeoutMs)}
              disabled={!usable}
              className={!usable ? "terminal-input-blocked" : ""}
              onChange={(e) => setTimeoutMs(Number(e.target.value))}
              placeholder="10000"
            />
          </label>
          <div className="terminal-force-copy">
            <WarningRegular />
            <span>{t("强制模式允许发送默认被拦截的 AT 指令，误操作可能导致断网或费用扣除。")}</span>
          </div>
          <Button disabled={!log.length} onClick={() => setLog([])}>{t("清空记录")}</Button>
        </div>
      </details>
    </section>
  );
}
