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
    <div className="overview-metric">
      <div className="overview-metric-top">
        <span>{kind === "rsrq" ? "RSRQ" : "SINR"}</span>
        <strong className={quality.color}>{quality.label}</strong>
      </div>
      <div className="overview-metric-reading">
        <strong>{typeof value === "number" ? value : "--"}</strong>
        <span>dB</span>
      </div>
      <div className="overview-metric-bars">
        {[0, 1, 2, 3].map((index) => (
          <div
            key={index}
            className={cx(
              "overview-metric-bar",
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
    <section className="overview-network-card ui-panel-muted min-w-0 overflow-hidden">
      <div className="overview-network-heading">
        <div className="overview-network-title">
          <div className="overview-network-icon">
            <CardUiRegular className="text-[23px]" />
          </div>
          <div className="overview-network-copy">
            <h2>{title}</h2>
            <div className="overview-network-subtitle">
              {operator} · {backendLabel} · {device.interface || "--"}
            </div>
          </div>
        </div>
        <div className="overview-network-actions">
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

      <div className="overview-network-surface">
        <div className="overview-signal-summary">
          <div className="min-w-0">
            <CountryFlag countryCode={modem?.operatorCountryCode} />
            <strong>{operator}</strong>
          </div>
          <div>
            <span className="overview-network-badge">{networkMode}</span>
            <span className={cx("overview-quality-badge", quality.color)}>
              {quality.label}
            </span>
          </div>
        </div>

        <div className="overview-signal-layout">
          <div className="overview-signal-gauge">
            <svg viewBox="0 0 240 155" role="img" aria-label={`RSRP ${rsrp ?? "--"} dBm`}>
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
            <div className="overview-gauge-scale"><span>-120</span><span>-60</span></div>
          </div>
          <div className="overview-signal-metrics">
            <MetricBar value={modem?.signalRsrq} kind="rsrq" />
            <MetricBar value={modem?.signalSinr} kind="sinr" />
          </div>
        </div>

        <div className="overview-signal-band">
          {bandFrequency(modem?.radioBand, modem?.radioChannel)}
        </div>
      </div>
    </section>
  );
}
