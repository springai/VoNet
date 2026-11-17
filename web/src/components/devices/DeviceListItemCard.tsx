import type { DeviceListItem, SimCardCandidate } from "../../types";
import { cx, signalBars } from "../../lib/utils";
import { SignalHigh, SignalLow, SignalMedium, Wifi, WifiLow, WifiOff } from "lucide-react";
import { deviceStatusMeta, isDeviceOnline, isVoWiFiInUse } from "./shared";
import { deviceTypeImage } from "../../lib/deviceTypes";
import { useI18n } from "../../lib/i18n";

export interface DeviceListItemCardProps {
  device: DeviceListItem;
  selected: boolean;
  statusText: string;
  candidate?: SimCardCandidate;
  onSelect: (id: string) => void;
}

export function DeviceListItemCard({ device, selected, statusText, candidate, onSelect }: DeviceListItemCardProps) {
  const meta = deviceStatusMeta(device);
  const { t } = useI18n();
  const modemICCID = String(device.modem?.iccid || "").replace(/\s+/g, "");
  const runtimeICCID = String(device.vowifiRuntime?.iccid || "").replace(/\s+/g, "");
  const currentICCID = modemICCID || runtimeICCID;
  const readConflict =
    (!!modemICCID && !!runtimeICCID && modemICCID !== runtimeICCID) ||
    candidate?.conflict === true;
  const readStable = candidate?.state === "stable" || (!candidate && meta.tone === "success");
  const readStateLabel = readConflict
    ? t("ICCID 冲突")
    : readStable
      ? t("稳定读取")
      : candidate?.state === "offline"
        ? t("离线")
        : candidate?.state === "pending"
          ? t("待稳定确认")
          : t("ICCID 已识别");
  const online = isDeviceOnline(device);
  const pending = meta.animated || meta.tone === "warning";
  const wifiCallingOnly = device.deviceType === "usb_sim_reader";
  const wifiMode = isVoWiFiInUse(device, wifiCallingOnly);
  const bars = signalBars(device.modem?.signalDbm);
  const NetworkIcon = wifiMode
    ? online
      ? Wifi
      : pending
        ? WifiLow
        : WifiOff
    : bars >= 3
      ? SignalHigh
      : bars >= 2
        ? SignalMedium
        : SignalLow;
  return (
    <button
      type="button"
      onClick={() => onSelect(device.id)}
      className={cx("device-list-item reference-list-item", selected && "is-active")}
      title={`${device.name || device.id} · ${readStateLabel}`}
    >
      <span className="reference-list-icon">
        <img src={deviceTypeImage(device.deviceType)} alt="" />
      </span>
      <span className="reference-list-copy">
        <span className="reference-list-title">{device.name || device.id}</span>
        <span className="reference-list-subtitle">{device.id} · {device.interface || "--"}</span>
        <span className={cx(
          "reference-list-connection",
          online && !pending && "is-online",
          pending && "is-pending",
          !online && !pending && "is-danger",
        )}>
          <i />
          {statusText}
        </span>
      </span>
      <span
        className={cx("card-connectivity-icon", online && "is-online", pending && "is-pending")}
        aria-label={online ? t("在线") : t("离线")}
      >
        <NetworkIcon />
      </span>
    </button>
  );
}
