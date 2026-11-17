import { CardUiRegular, SettingsRegular } from "@fluentui/react-icons";
import { CellLockButton } from "./CellLockButton";
import { cx } from "../../lib/utils";
import { CountryFlag } from "../CountryFlag";
import type { SimCard } from "../../types";
import type { DeviceDetail } from "./types";
import { useI18n } from "../../lib/i18n";

interface Props {
  device: DeviceDetail;
  card?: SimCard;
  onOpenOperatorSelection?: () => void;
  onOpenCellLock?: () => void;
  cellLocked: boolean;
  cellLockStatusKnown: boolean;
}

const SIGNAL_MIN = -120;
const SIGNAL_MAX = -60;
const GAUGE_RADIUS = 88;
const GAUGE_CENTER_X = 120;
const GAUGE_CENTER_Y = 128;

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}

function signalPercent(value?: number | null) {
  if (typeof value !== "number" || !Number.isFinite(value)) return 0;
  return clamp((value - SIGNAL_MIN) / (SIGNAL_MAX - SIGNAL_MIN), 0, 1);
}

function signalQuality(value?: number | null) {
  if (typeof value !== "number" || !Number.isFinite(value)) return tFallback();
  if (value >= -80) return { label: "优秀", color: "text-emerald-500 dark:text-emerald-400", stroke: "#10b981" };
  if (value >= -95) return { label: "良好", color: "text-emerald-600 dark:text-emerald-300", stroke: "#22c55e" };
  if (value >= -105) return { label: "一般", color: "text-amber-500 dark:text-amber-400", stroke: "#f4c95d" };
  return { label: "较差", color: "text-red-500 dark:text-red-400", stroke: "#ef6b73" };
}

function tFallback() {
  return { label: "未知", color: "text-gray-400", stroke: "#9ca3af" };
}

function gaugePoint(progress: number) {
  const angle = Math.PI - clamp(progress, 0, 1) * Math.PI;
  return {
    x: GAUGE_CENTER_X + GAUGE_RADIUS * Math.cos(angle),
    y: GAUGE_CENTER_Y - GAUGE_RADIUS * Math.sin(angle),
  };
}

function qualityLabel(value: number | undefined, kind: "rsrq" | "sinr") {
  if (typeof value !== "number" || !Number.isFinite(value)) return tFallback();
  if (kind === "rsrq") {
    if (value >= -10) return { label: "优秀", color: "text-emerald-500 dark:text-emerald-400" };
    if (value >= -15) return { label: "良好", color: "text-emerald-600 dark:text-emerald-300" };
    if (value >= -20) return { label: "一般", color: "text-amber-500 dark:text-amber-400" };
    return { label: "较差", color: "text-red-500 dark:text-red-400" };
  }
  if (value >= 13) return { label: "优秀", color: "text-emerald-500 dark:text-emerald-400" };
  if (value >= 5) return { label: "良好", color: "text-emerald-600 dark:text-emerald-300" };
  if (value >= 0) return { label: "一般", color: "text-amber-500 dark:text-amber-400" };
  return { label: "较差", color: "text-red-500 dark:text-red-400" };
}

function metricLevel(value: number | undefined, kind: "rsrq" | "sinr") {
  if (typeof value !== "number" || !Number.isFinite(value)) return 0;
  if (kind === "rsrq") return clamp((value + 25) / 20, 0, 1);
  return clamp((value + 5) / 25, 0, 1);
}

function bandFrequency(radioBand?: string, channel?: number) {
  const band = String(radioBand || "").trim().toUpperCase();
  const frequencies: Record<string, string> = {
    B1: "2100 MHz", B2: "1900 MHz", B3: "1800 MHz", B4: "1700 MHz",
    B5: "850 MHz", B7: "2600 MHz", B8: "900 MHz", B12: "700 MHz",
    B13: "700 MHz", B14: "700 MHz", B17: "700 MHz", B20: "800 MHz",
    B25: "1900 MHz", B26: "850 MHz", B28: "700 MHz", B29: "700 MHz",
    B30: "2300 MHz", B38: "2600 MHz", B40: "2300 MHz", B41: "2500 MHz",
    B42: "3500 MHz", B43: "3700 MHz", B46: "5200 MHz", B48: "3500 MHz",
    B66: "1700 MHz", B71: "600 MHz",
  };
  const frequency = frequencies[band] || "";
  if (band && frequency && channel) return `${band} · ${frequency}`;
  if (band && channel) return `${band} · ${tChannel(channel)}`;
  return band || frequency || "--";
}

function tChannel(channel: number) {
  return `信道 ${channel}`;
}

function MetricBar({ value, kind }: { value?: number; kind: "rsrq" | "sinr" }) {
  const level = metricLevel(value, kind);
  const quality = qualityLabel(value, kind);
  return (
    <div>
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs font-semibold text-gray-500 dark:text-gray-400">{kind === "rsrq" ? "RSRQ" : "SINR"}</span>
        <span className={cx("text-sm font-semibold", quality.color)}>{quality.label}</span>
      </div>
      <div className="mt-1 flex items-baseline gap-2">
        <span className="text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">
          {typeof value === "number" ? value : "--"}
        </span>
        <span className="text-xs text-gray-400">dB</span>
      </div>
      <div className="mt-2 flex h-1.5 gap-1">
        {[0, 1, 2, 3].map((index) => (
          <div
            key={index}
            className={cx(
              "flex-1 rounded-full transition-colors duration-500",
              level > index / 4
                ? kind === "rsrq"
                  ? index < 2 ? "bg-emerald-500" : "bg-emerald-300"
                  : index < 2 ? "bg-red-400" : "bg-amber-400"
                : "bg-gray-200 dark:bg-white/10",
            )}
          />
        ))}
      </div>
    </div>
  );
}

export function OverviewNetworkCard({
  device, card, onOpenOperatorSelection, onOpenCellLock, cellLocked, cellLockStatusKnown,
}: Props) {
  const { t } = useI18n();
  const modem = device.modem;
  const backendLabel =
    device.backendMode === "qmi" ? "QMI"
      : device.backendMode === "mbim" ? "MBIM"
        : device.backendMode === "at" ? "AT"
          : device.backendMode === "pcsc" ? "PC/SC" : "Auto";
  const title = card?.cardType === "esim_profile" ? "eSIM" : "pSIM";
  const operator = modem?.operator || card?.carrier || card?.spn || "--";
  const networkMode = modem?.networkMode || "--";
  const quality = signalQuality(modem?.signalRsrp ?? modem?.signalDbm);
  const gaugeProgress = signalPercent(modem?.signalRsrp ?? modem?.signalDbm);
  const gaugePointValue = gaugePoint(gaugeProgress);
  const rsrp = modem?.signalRsrp ?? modem?.signalDbm;

  return (
    <section className="ui-panel-muted min-w-0 overflow-hidden p-5">
      <div className="mb-5 flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-sky-50 text-sky-600 dark:bg-sky-500/10 dark:text-sky-300">
            <CardUiRegular className="text-[23px]" />
          </div>
          <div className="min-w-0">
            <h2 className="truncate text-2xl font-bold text-gray-900 dark:text-white">{title}</h2>
            <div className="truncate text-sm text-gray-500 dark:text-gray-400">
              {operator} · {backendLabel} · {device.interface || "--"}
            </div>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {onOpenCellLock ? <CellLockButton compact onClick={onOpenCellLock} locked={cellLocked} statusKnown={cellLockStatusKnown} /> : null}
          {onOpenOperatorSelection ? (
            <button
              type="button"
              onClick={onOpenOperatorSelection}
              className="rounded p-1 transition-colors hover:bg-black/5 dark:hover:bg-white/10"
              title={t("网络选择设置")}
            >
              <SettingsRegular className="h-5 w-5 text-gray-500 dark:text-gray-400" />
            </button>
          ) : null}
        </div>
      </div>

      <div className="rounded-xl border border-gray-200 bg-white p-4 dark:border-white/10 dark:bg-black/10">
        <div className="flex items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2">
            <CountryFlag countryCode={modem?.operatorCountryCode} />
            <span className="truncate text-base font-semibold text-gray-900 dark:text-white">{operator}</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="inline-flex h-7 items-center rounded-full bg-sky-100 px-3 text-xs font-semibold text-sky-700 dark:bg-sky-500/15 dark:text-sky-300">{networkMode}</span>
            <span className={cx("inline-flex h-7 items-center rounded-full bg-gray-100 px-3 text-xs font-semibold dark:bg-white/10", quality.color)}>
              {quality.label}
            </span>
          </div>
        </div>

        <div className="mt-3 grid grid-cols-1 items-center gap-5 sm:grid-cols-[minmax(210px,1fr)_minmax(180px,0.9fr)]">
          <div className="relative mx-auto w-full max-w-[250px]">
            <svg viewBox="0 0 240 155" className="h-auto w-full" role="img" aria-label={`RSRP ${rsrp ?? "--"} dBm`}>
              <path d="M 32 128 A 88 88 0 0 1 208 128" fill="none" stroke="currentColor" strokeWidth="16" strokeLinecap="round" className="text-gray-200 dark:text-white/10" />
              <path
                d="M 32 128 A 88 88 0 0 1 208 128"
                fill="none"
                stroke={quality.stroke}
                strokeWidth="16"
                strokeLinecap="round"
                pathLength={100}
                strokeDasharray={`${gaugeProgress * 100} 100`}
                className="transition-[stroke-dasharray] duration-700 ease-out"
              />
              <circle cx={gaugePointValue.x} cy={gaugePointValue.y} r="6" fill="#ffffff" stroke={quality.stroke} strokeWidth="4" className="transition-all duration-700 ease-out" />
              <text x="120" y="112" textAnchor="middle" className="fill-gray-900 text-[31px] font-semibold dark:fill-white">{typeof rsrp === "number" ? rsrp : "--"}</text>
              <text x="120" y="136" textAnchor="middle" className="fill-gray-400 text-[12px]">dBm · RSRP</text>
            </svg>
            <div className="absolute bottom-1 left-0 right-0 flex justify-between text-[11px] text-gray-400"><span>-120</span><span>-60</span></div>
          </div>
          <div className="space-y-5">
            <MetricBar value={modem?.signalRsrq} kind="rsrq" />
            <MetricBar value={modem?.signalSinr} kind="sinr" />
          </div>
        </div>

        <div className="mt-4 border-t border-gray-100 pt-3 text-sm font-medium text-gray-500 dark:border-white/10 dark:text-gray-400">
          {bandFrequency(modem?.radioBand, modem?.radioChannel)}
        </div>
      </div>
    </section>
  );
}
