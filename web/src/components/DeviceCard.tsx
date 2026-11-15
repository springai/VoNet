import type { DashboardDevice } from "../types";
import { deviceTypeImage, deviceTypeLabel } from "../lib/deviceTypes";
import { StatusDot } from "./ui/StatusDot";
import { Tag } from "./ui/Tag";
import { useI18n } from "../lib/i18n";

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-3">
      <span className="shrink-0 text-xs text-gray-400 dark:text-gray-500">{label}</span>
      <span className="min-w-0 truncate text-right text-xs font-medium text-gray-700 dark:text-gray-300" title={value}>
        {value || "--"}
      </span>
    </div>
  );
}

export function DeviceCard({ device, onOpen }: { device: DashboardDevice; onOpen: (id: string) => void }) {
  const { t } = useI18n();
  const deviceLabel = deviceTypeLabel(device.deviceType);

  return (
    <button
      type="button"
      onClick={() => onOpen(device.id)}
      className="ui-card ui-card-hover group relative block w-full overflow-hidden text-left transition-all duration-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-[#0ea5e9] focus-visible:ring-offset-2 focus-visible:ring-offset-white dark:focus-visible:ring-offset-gray-950"
    >
      <div className="p-5">
        <div className="mb-5 flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <img src={deviceTypeImage(device.deviceType)} alt="" className="h-11 w-11 shrink-0 object-contain" />
            <div className="min-w-0">
              <h3 className="truncate text-base font-bold text-gray-800 dark:text-gray-100">{device.name || device.id}</h3>
              <div className="mt-1 truncate text-xs text-gray-400" title={deviceLabel}>{deviceLabel || t("设备")}</div>
            </div>
          </div>
          <Tag type={device.healthy ? "success" : "danger"}>{device.healthy ? t("在线") : t("离线")}</Tag>
        </div>
        <div className="flex items-center gap-2 border-y border-gray-100 py-3 dark:border-white/10">
          <StatusDot tone={device.healthy ? "success" : "danger"} animated={device.healthy} />
          <span className="text-xs font-medium text-gray-600 dark:text-gray-300">
            {device.healthy ? t("设备运行正常") : t("设备当前离线")}
          </span>
        </div>
        <div className="mt-4 space-y-2.5">
          <InfoRow label={t("设备 ID")} value={device.id} />
          <InfoRow label={t("型号")} value={device.model || "--"} />
          <InfoRow label={t("网卡接口")} value={device.interface} />
          <InfoRow label={t("公网 IP")} value={device.publicIp} />
          <InfoRow label={t("代理端口")} value={device.proxyPort ? String(device.proxyPort) : "--"} />
        </div>
      </div>
    </button>
  );
}
