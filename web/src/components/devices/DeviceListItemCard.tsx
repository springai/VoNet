import type { DeviceListItem, SimCardCandidate } from "../../types";
import { cx } from "../../lib/utils";
import { Button, Tag, StatusDot } from "../ui";
import { deviceStatusMeta } from "./shared";
import { deviceTypeImage } from "../../lib/deviceTypes";
import { useNavigate } from "react-router-dom";
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
  const navigate = useNavigate();
  const { t } = useI18n();
  const modemICCID = String(device.modem?.iccid || "").replace(/\s+/g, "");
  const runtimeICCID = String(device.vowifiRuntime?.iccid || "").replace(/\s+/g, "");
  const currentICCID = modemICCID || runtimeICCID;
  const readConflict =
    (!!modemICCID && !!runtimeICCID && modemICCID !== runtimeICCID) ||
    candidate?.conflict === true;
  return (
    <div className={cx(
      "device-list-item rounded-xl border",
      selected
        ? "border-indigo-200 bg-indigo-50/70 dark:border-indigo-500/30 dark:bg-indigo-500/10"
        : "border-gray-100 dark:border-white/10",
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
            {currentICCID ? (
              <div className="mt-1 flex min-w-0 items-center gap-2">
                <span className="truncate font-mono text-[11px] text-amber-600 dark:text-amber-300">
                  ICCID {currentICCID}
                </span>
                <Tag type={readConflict ? "danger" : candidate?.state === "stable" ? "success" : candidate?.state === "offline" ? "info" : "warning"}>
                  {readConflict
                    ? t("ICCID 冲突")
                    : candidate?.state === "stable"
                      ? t("稳定读取")
                      : candidate?.state === "offline"
                        ? t("离线")
                        : t("待稳定确认")}
                </Tag>
              </div>
            ) : null}
          </div>
          <div className="flex items-center gap-2">
            <StatusDot tone={meta.tone} size="sm" animated={meta.animated} />
            <Tag type={meta.tag}>{meta.label}</Tag>
          </div>
        </div>
      </button>
      {currentICCID ? (
        <div className="flex justify-end border-t border-gray-100 px-2 pb-2 pt-1.5 dark:border-white/10">
          <Button
            size="small"
            plain
            onClick={() => navigate(`/cards?new=${encodeURIComponent(currentICCID)}&cardType=physical_sim&deviceId=${encodeURIComponent(device.id)}&hostType=${encodeURIComponent(candidate?.hostType || device.deviceType)}`)}
          >
            {t("保存/查看卡片")}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
