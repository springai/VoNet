import type { ReactNode } from "react";
import type { DeviceListItem, SimCardCandidate } from "../../types";
import { Input, Select, ListSkeleton, EmptyState } from "../ui";
import { isDeviceOnline, isRegistered, isVoWiFiInUse, lifecycleLabel } from "./shared";
import { DeviceListItemCard } from "./DeviceListItemCard";
import { tl, useI18n } from "../../lib/i18n";

export type StatusFilter = "all" | "online" | "offline";
export type SortKey = "name" | "signal";
export type SortDir = "asc" | "desc";

export interface DeviceListPanelProps {
  title?: ReactNode;
  countLabel?: ReactNode;
  headerActions?: ReactNode;
  loading: boolean;
  query: string;
  statusFilter: StatusFilter;
  sortKey: SortKey;
  sortDir: SortDir;
  selectedId: string;
  filteredDevices: DeviceListItem[];
  candidateByICCID?: Record<string, SimCardCandidate>;
  onQueryChange: (v: string) => void;
  onStatusFilterChange: (v: StatusFilter) => void;
  onSortKeyChange: (v: SortKey) => void;
  onSortDirChange: (v: SortDir) => void;
  onSelectDevice: (id: string) => void;
  embedded?: boolean;
  footer?: ReactNode;
}

function primaryLine(d: DeviceListItem): string {
  const phaseLabel = lifecycleLabel(d.lifecyclePhase);
  if (phaseLabel && d.lifecyclePhase !== "online" && d.lifecyclePhase !== "offline") return phaseLabel;
  if (isRegistered(d)) {
    const operator = d.modem?.operator || "--";
    const mode = [d.modem?.networkDuplex, d.modem?.networkMode].filter(Boolean).join(" ") || "--";
    return `${operator} · ${mode}`;
  }
  if (isDeviceOnline(d)) {
    return d.registrationStateLabel === "searching" ? tl("搜索网络中") : d.registrationStateLabel === "denied" ? tl("驻网被拒") : tl("未驻网");
  }
  return tl("控制面恢复中");
}

function statusLine(d: DeviceListItem): string {
  if (isVoWiFiInUse(d)) return "WiFi-Calling";
  return primaryLine(d);
}

export function DeviceListPanel(props: DeviceListPanelProps) {
  const { t } = useI18n();
  const {
    title,
    countLabel,
    headerActions,
    loading,
    query,
    statusFilter,
    sortKey,
    sortDir,
    selectedId,
    filteredDevices,
    embedded,
    footer,
  } = props;
  return (
    <div className={embedded ? "workspace-list-panel min-w-0" : "ui-card p-5"}>
      {title ? (
        <div className="workspace-list-header">
          <div className="min-w-0">
            <h2>{title}</h2>
            {countLabel ? <span>{countLabel}</span> : null}
          </div>
          {headerActions ? <div className="workspace-list-actions">{headerActions}</div> : null}
        </div>
      ) : null}
      <div className="workspace-list-search">
        <Input value={query} onChange={(e) => props.onQueryChange(e.target.value)} placeholder={t("搜索设备 / ICCID / IMEI / 网卡")} />
      </div>
      <div className="workspace-list-filters">
        <Select
          value={statusFilter}
          onChange={(v) => props.onStatusFilterChange(v as StatusFilter)}
          placeholder={t("在线")}
          options={[
            { value: "all", label: t("全部状态") },
            { value: "online", label: t("仅在线") },
            { value: "offline", label: t("仅离线") },
          ]}
        />
        <Select
          value={sortKey}
          onChange={(v) => props.onSortKeyChange(v as SortKey)}
          placeholder={t("排序")}
          options={[
            { value: "name", label: t("排序：名称") },
            { value: "signal", label: t("排序：信号") },
          ]}
        />
        <Select
          value={sortDir}
          onChange={(v) => props.onSortDirChange(v as SortDir)}
          placeholder={t("方向")}
          options={[
            { value: "asc", label: t("升序") },
            { value: "desc", label: t("降序") },
          ]}
        />
      </div>
      {loading && filteredDevices.length === 0 ? (
        <ListSkeleton rows={8} />
      ) : filteredDevices.length === 0 ? (
        <EmptyState title={t("暂无设备")} subtitle={t("该 Agent 当前没有已配置设备")} />
      ) : (
        <div className="device-list-scroll workspace-list-scroll">
          <div className="device-list-grid">
            {filteredDevices.map((d) => (
              <DeviceListItemCard
                key={d.id}
                device={d}
                selected={selectedId === d.id}
                statusText={statusLine(d)}
                candidate={props.candidateByICCID?.[String(d.modem?.iccid || d.vowifiRuntime?.iccid || "").replace(/\s+/g, "")]}
                onSelect={props.onSelectDevice}
              />
            ))}
          </div>
        </div>
      )}
      {footer ? <div className="workspace-list-footer">{footer}</div> : null}
    </div>
  );
}
