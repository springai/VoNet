import { CardUiRegular, CellularData1Regular, Wifi1Regular } from "@fluentui/react-icons";
import type { DashboardCardView } from "./dashboardModels";
import { Tag } from "../ui/Tag";
import { StatusDot } from "../ui/StatusDot";
import { useI18n } from "../../lib/i18n";
import { cx, signalBars, signalColor } from "../../lib/utils";
import { useShowSensitive } from "../devices/shared";
import { displayICCID } from "../../lib/sensitive";

const BAR_HEIGHTS = ["h-1/4", "h-2/4", "h-3/4", "h-full"];

export function DashboardCardCard({ item, onOpen }: { item: DashboardCardView; onOpen: (iccid: string) => void }) {
  const { t } = useI18n();
  const [showSensitive] = useShowSensitive();
  const { card, device, online, service } = item;
  const ServiceIcon = service === "vowifi" ? Wifi1Regular : service === "cellular_data" ? CellularData1Regular : CardUiRegular;
  const serviceLabel = service === "vowifi"
    ? "WiFi Calling"
    : service === "cellular_data"
      ? t("蜂窝数据")
      : t("网络服务未启用");
  const serviceDetail = service === "vowifi"
    ? device?.vowifiActive
      ? t("已连接")
      : online
        ? t("正在连接")
        : t("当前离线")
    : service === "cellular_data"
      ? [device?.operator || t("检测中"), device?.networkMode].filter(Boolean).join(" · ")
      : t("检查卡片策略或设备绑定");
  const bars = signalBars(device?.signalDbm);

  return (
    <button
      type="button"
      onClick={() => onOpen(card.iccid)}
      className="ui-card ui-card-hover block w-full overflow-hidden text-left transition-all duration-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-[#0ea5e9] focus-visible:ring-offset-2 focus-visible:ring-offset-white dark:focus-visible:ring-offset-gray-950"
    >
      <div className="p-5">
        <div className="mb-5 flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg bg-amber-50 text-amber-600 dark:bg-amber-500/10 dark:text-amber-300">
              <CardUiRegular className="text-[23px]" />
            </div>
            <div className="min-w-0">
              <h3 className="truncate text-base font-bold text-gray-800 dark:text-gray-100">
                {card.displayName || displayICCID(card.iccid, showSensitive)}
              </h3>
              <div className="mt-1 font-mono text-xs text-gray-400">{displayICCID(card.iccid, showSensitive)}</div>
            </div>
          </div>
          <Tag type={online ? "success" : card.conflict ? "danger" : "info"}>
            {online ? t("当前启用") : card.conflict ? t("ICCID 冲突") : t("离线")}
          </Tag>
        </div>

        <div className="rounded-lg border border-gray-100 bg-gray-50/60 p-3 dark:border-white/10 dark:bg-white/5">
          <div className="flex items-center justify-between gap-3">
            <div className="flex min-w-0 items-center gap-2">
              <ServiceIcon
                className={cx(
                  "h-[18px] w-[18px] shrink-0",
                  service === "vowifi"
                    ? "text-emerald-500"
                    : service === "cellular_data"
                      ? "text-sky-500"
                      : "text-gray-400",
                )}
              />
              <div className="min-w-0">
                <div className="truncate text-sm font-semibold text-gray-800 dark:text-gray-200">{serviceLabel}</div>
                <div className="mt-0.5 truncate text-xs text-gray-400">{serviceDetail}</div>
              </div>
            </div>
            {service === "cellular_data" ? (
              <div className="flex h-3 items-end gap-[2px]" title={t("信号强度")}>
                {[1, 2, 3, 4].map((bar) => (
                  <div key={bar} className={cx("w-1 rounded-sm", BAR_HEIGHTS[bar - 1], bars >= bar ? signalColor(device?.signalDbm) : "bg-gray-200 dark:bg-gray-700")} />
                ))}
              </div>
            ) : (
              <StatusDot tone={service === "vowifi" && device?.vowifiActive ? "success" : online ? "warning" : "neutral"} animated={online} />
            )}
          </div>
        </div>

        <div className="mt-4 space-y-2">
          <div className="flex items-center justify-between gap-3 text-xs">
            <span className="text-gray-400">{t("卡片类型")}</span>
            <span className="font-medium text-gray-700 dark:text-gray-300">
              {card.cardType === "esim_profile" ? "eSIM Profile" : t("实体 SIM")}
            </span>
          </div>
          <div className="flex items-center justify-between gap-3 text-xs">
            <span className="text-gray-400">{t("绑定设备")}</span>
            <span className="min-w-0 truncate font-medium text-gray-700 dark:text-gray-300">{device?.name || card.binding?.deviceName || "--"}</span>
          </div>
        </div>
      </div>
    </button>
  );
}
