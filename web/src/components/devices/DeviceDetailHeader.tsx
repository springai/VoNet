import { useNavigate } from "react-router-dom";
import { CardUiRegular } from "@fluentui/react-icons";
import type { DeviceListItem, SimCardCandidate } from "../../types";
import { Button } from "../ui";
import { useI18n } from "../../lib/i18n";
import { deviceTypeImage } from "../../lib/deviceTypes";

export interface DeviceDetailHeaderProps {
  device: DeviceListItem;
  onCopyText: (text: string) => void;
  candidate?: SimCardCandidate;
}

export function DeviceDetailHeader(props: DeviceDetailHeaderProps) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const { device, candidate } = props;
  const modemICCID = String(device.modem?.iccid || "").replace(/\s+/g, "");
  const runtimeICCID = String(device.vowifiRuntime?.iccid || "").replace(/\s+/g, "");
  const currentICCID = modemICCID || runtimeICCID;
  return (
    <div className="ui-card p-6">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-3">
            <img src={deviceTypeImage(device.deviceType)} alt="" className="h-11 w-11 flex-shrink-0 object-contain" />
            <div className="min-w-0">
              <div className="truncate text-xl font-extrabold text-gray-900 dark:text-white">{device.name || device.id}</div>
              <div className="mt-0.5 truncate text-xs text-gray-500 dark:text-gray-400">
                <span className="cursor-pointer font-mono hover:underline" onClick={() => props.onCopyText(device.id)}>
                  {device.id}
                </span>
              </div>
            </div>
          </div>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-3">
          <div className="text-xs font-semibold text-gray-500">{t("设备硬件与 eSIM 管理")}</div>
          {currentICCID ? (
            <Button
              icon={<CardUiRegular />}
              onClick={() => navigate(`/cards?new=${encodeURIComponent(currentICCID)}&cardType=physical_sim&deviceId=${encodeURIComponent(device.id)}&hostType=${encodeURIComponent(candidate?.hostType || device.deviceType)}`)}
            >
              {t("保存/查看卡片")}
            </Button>
          ) : null}
        </div>
      </div>
    </div>
  );
}
