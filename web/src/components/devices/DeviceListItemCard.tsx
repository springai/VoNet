import type { DeviceListItem, SimCardCandidate } from "../../types";
import { cx } from "../../lib/utils";
import { Tag, StatusDot } from "../ui";
import { deviceStatusMeta } from "./shared";
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
  const readStateTone = readConflict
    ? "danger"
    : readStable
      ? "success"
      : candidate?.state === "offline"
        ? "info"
        : candidate?.state === "pending"
          ? "warning"
          : "info";
  return (
    <div className={cx(
      "device-list-item reference-list-item",
      selected
        ? "is-active"
        : "",
    )}>
      <button
        type="button"
        onClick={() => onSelect(device.id)}
        className="w-full rounded-[11px] p-3 text-left transition-colors hover:bg-gray-50/60 dark:hover:bg-white/5"
      >
        <div className="flex items-start justify-between gap-2">
          <img src={deviceTypeImage(device.deviceType)} alt="" className="h-10 w-10 shrink-0 object-contain" />
          <div className="min-w-0 flex-1">
            <div className="truncate font-bold text-gray-800 dark:text-gray-100">{device.name || device.id}</div>
            <div className="mt-0.5 truncate text-xs text-gray-500">
              {device.id} · {device.interface || "--"}
            </div>
            <div className="mt-1 truncate text-xs text-gray-400">{statusText}</div>
          </div>
          <div className="flex items-center gap-2">
            <StatusDot tone={meta.tone} size="sm" animated={meta.animated} />
            <Tag type={meta.tag}>{meta.label}</Tag>
          </div>
        </div>
        {currentICCID ? (
          <div className="mt-2 flex justify-end">
            <Tag type={readStateTone}>{readStateLabel}</Tag>
          </div>
        ) : null}
      </button>
    </div>
  );
}
