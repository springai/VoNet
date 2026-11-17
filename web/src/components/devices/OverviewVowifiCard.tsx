import { useEffect, useState } from "react";
import { ArrowClockwiseRegular, CheckmarkRegular, MailRegular, PhoneRegular } from "@fluentui/react-icons";
import { cx } from "../../lib/utils";
import type { SimCard } from "../../types";
import type { DeviceDetail } from "./types";
import { tf, useI18n } from "../../lib/i18n";

interface Props {
  device: DeviceDetail;
  card?: SimCard;
  operatorDisplay?: string;
}

function registeredDuration(startedAt?: string, now = Date.now()) {
  const started = startedAt ? Date.parse(startedAt) : NaN;
  if (!Number.isFinite(started) || started <= 0 || now < started) return "--:--:--";
  const total = Math.floor((now - started) / 1000);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  return [hours, minutes, seconds].map((value) => String(value).padStart(2, "0")).join(":");
}

function displayServiceName(card: SimCard | undefined, operatorDisplay?: string) {
  const base = card?.displayName?.trim() || operatorDisplay?.trim() || card?.carrier?.trim() || card?.spn?.trim();
  if (!base) return "VoWiFi";
  return /vowifi/i.test(base) ? base : `${base} VoWiFi`;
}

export function OverviewVowifiCard({ device, card, operatorDisplay }: Props) {
  const { t } = useI18n();
  const rt = device.vowifiRuntime;
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!rt?.startedAt) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [rt?.startedAt]);

  const stages = [
    { label: t("身份"), ready: !!rt?.simReady && !!rt?.iccid && !!rt?.imsi },
    { label: t("安全隧道"), ready: !!rt?.tunnelReady },
    { label: t("鉴权"), ready: !!rt?.imsReady || !!rt?.smsReady },
    { label: t("注册"), ready: !!rt?.imsReady },
  ];
  const completed = stages.reduce((count, stage) => (
    count === stages.indexOf(stage) && stage.ready ? count + 1 : count
  ), 0);
  const active = !!rt?.active;
  const currentStage = completed < stages.length ? completed : -1;
  const registered = !!rt?.imsReady;
  const statusLabel = registered
    ? t("已注册")
    : active && currentStage === 1
      ? t("建立隧道")
      : active && currentStage === 2
        ? t("鉴权中")
        : active && currentStage === 3
          ? t("注册中")
          : active
            ? t("准备中")
            : t("未注册");
  const reconnects = Math.max(0, Number(rt?.attempt || 1) - 1);
  const capabilities = [
    {
      label: t("语音"),
      state: rt?.imsReady ? t("可用") : active && completed >= 1 ? t("准备中") : t("不可用"),
      available: !!rt?.imsReady,
      preparing: active && completed >= 1 && !rt?.imsReady,
      icon: <PhoneRegular />,
    },
    {
      label: t("短信"),
      state: rt?.smsReady ? t("可用") : active && completed >= 1 ? t("准备中") : t("不可用"),
      available: !!rt?.smsReady,
      preparing: active && completed >= 1 && !rt?.smsReady,
      icon: <MailRegular />,
    },
    {
      label: "USSD",
      state: rt?.imsReady && rt?.tunnelReady
        ? t("可用")
        : active && currentStage >= 2
          ? t("准备中")
          : t("不可用"),
      available: !!rt?.imsReady && !!rt?.tunnelReady,
      preparing: active && currentStage >= 2 && !(rt?.imsReady && rt?.tunnelReady),
      icon: <span className="text-base font-bold">#</span>,
    },
  ];

  return (
    <section className="ui-panel-muted min-w-0 overflow-hidden p-5">
      <div className="mb-5 flex items-start justify-between gap-3">
        <div className="flex items-center gap-2 text-gray-600 dark:text-gray-300">
          <PhoneRegular className="text-[22px]" />
          <span className="text-sm font-semibold">IMS</span>
        </div>
        <div className={cx(
          "inline-flex h-8 items-center gap-1.5 rounded-full px-3 text-sm font-semibold",
          registered
            ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300"
            : active
              ? "bg-sky-100 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300"
              : "bg-gray-100 text-gray-600 dark:bg-white/10 dark:text-gray-300",
        )}>
          {registered
            ? <CheckmarkRegular className="text-[16px]" />
            : active
              ? <ArrowClockwiseRegular className="animate-spin text-[16px]" />
              : null}
          {statusLabel}
        </div>
      </div>

      <div className="mb-6">
        <h2 className="text-2xl font-bold text-gray-900 dark:text-white">{displayServiceName(card, operatorDisplay)}</h2>
        <div className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {rt?.networkMode || t("Wi-Fi 接入")} · {t("已连续注册")} {registeredDuration(rt?.startedAt, now)} · {tf("重连 {count} 次", { count: reconnects })}
        </div>
      </div>

      <div className="relative mb-2 px-5">
        <div className="absolute left-[12.5%] right-[12.5%] top-[17px] h-1 rounded-full bg-gray-200 dark:bg-white/10">
          <div
            className="h-1 rounded-full bg-emerald-500 transition-[width] duration-700 ease-out dark:bg-emerald-400"
            style={{ width: `${completed <= 1 ? 0 : ((completed - 1) / (stages.length - 1)) * 100}%` }}
          />
        </div>
        <div className="relative grid grid-cols-4">
          {stages.map((stage, index) => (
            <div key={stage.label} className="flex min-w-0 flex-col items-center gap-2">
              <div className={cx(
                "flex h-9 w-9 shrink-0 items-center justify-center rounded-full border-2 transition-colors duration-500",
                index < completed
                  ? "border-emerald-500 bg-emerald-500 text-white shadow-sm dark:border-emerald-400 dark:bg-emerald-400"
                  : index === currentStage && active
                    ? "border-sky-200 bg-sky-50 text-sky-600 shadow-sm dark:border-sky-400/30 dark:bg-sky-500/10 dark:text-sky-300"
                    : "border-gray-300 bg-white text-gray-400 dark:border-white/15 dark:bg-gray-900",
              )}>
                {index < completed
                  ? <CheckmarkRegular className="text-[19px]" />
                  : index === currentStage && active
                    ? <ArrowClockwiseRegular className="animate-spin text-[21px]" />
                    : index + 1}
              </div>
              <div className={cx(
                "text-center text-xs font-medium",
                index < completed
                  ? "text-gray-600 dark:text-gray-300"
                  : index === currentStage && active
                    ? "font-semibold text-sky-700 dark:text-sky-300"
                    : "text-gray-500 dark:text-gray-400",
              )}>
                {stage.label}
              </div>
            </div>
          ))}
        </div>
      </div>

      <div className="mt-5 grid grid-cols-3 overflow-hidden rounded-lg border border-gray-200 bg-gray-50 dark:border-white/10 dark:bg-white/[0.03]">
        {capabilities.map((capability, index) => (
          <div
            key={capability.label}
            className={cx(
              "flex min-h-12 items-center justify-center gap-2 px-2 text-sm font-semibold",
              index > 0 && "border-l border-gray-200 dark:border-white/10",
              capability.available
                ? "text-emerald-700 dark:text-emerald-300"
                : capability.preparing
                  ? "text-sky-700 dark:text-sky-300"
                  : "text-gray-400 dark:text-gray-500",
            )}
          >
            {capability.icon}
            <span>{capability.label}</span>
            <span>{capability.state}</span>
          </div>
        ))}
      </div>
    </section>
  );
}
