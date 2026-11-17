import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ArrowSyncRegular, AddRegular, ChevronLeftRegular } from "@fluentui/react-icons";
import { api, apiMessage } from "../api";
import type { DeviceConfig, DeviceListItem, DiscoveredDevice, SimCardCandidate } from "../types";
import { usePolling } from "../lib/usePolling";
import { useMediaQuery } from "../lib/useMediaQuery";
import { Button, PageHeader, RefreshButton, ErrorState, ListSkeleton, Tabs, confirmDialog, message } from "../components/ui";
import { DeviceListPanel, type StatusFilter, type SortDir, type SortKey } from "../components/devices/DeviceListPanel";
import { AgentManager } from "../components/agents/AgentManager";
import { DeviceDetailHeader } from "../components/devices/DeviceDetailHeader";
import { DeviceEsimTab } from "../components/devices/DeviceEsimTab";
import { DeviceConfigTab } from "../components/devices/DeviceConfigTab";
import { DeviceAddDialog } from "../components/devices/DeviceAddDialog";
import { copyText, isDeviceOnline, isQmiControl, isRecoveringPhase } from "../components/devices/shared";
import type { AddDeviceForm, LoadError } from "../components/devices/types";
import { listCardCandidates, listCards } from "../components/cards/cardApi";
import { tf, useI18n } from "../lib/i18n";

const VALID_TABS = new Set(["esim", "config"]);
const EMPTY_ADD: AddDeviceForm = {
  id: "",
  agentId: "",
  remoteDeviceId: "",
  remoteHardwareGeneration: "",
  name: "",
  deviceType: "",
  interface: "",
  modemImei: "",
  pcscImeiMode: "random",
  pcscImei: "",
  usbPath: "",
  esimTransport: "at",
  atPort: "",
  controlDevice: "",
  deviceBackend: "at",
	 simPin: "",
};

export default function DevicesPage() {
  const { t } = useI18n();
  const [searchParams, setSearchParams] = useSearchParams();
  const [list, setList] = useState<DeviceListItem[]>([]);
  const [listLoading, setListLoading] = useState(true);
  const [listError, setListError] = useState<LoadError | null>(null);
  const [lastOkAt, setLastOkAt] = useState<number | null>(null);
  const [selectedId, setSelectedId] = useState("");
  const [selectedAgentId, setSelectedAgentId] = useState("");
  const [activeTab, setActiveTab] = useState("esim");
  const [editConfig, setEditConfig] = useState<DeviceConfig | null>(null);
  const [configSnapshot, setConfigSnapshot] = useState("");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [rebooting, setRebooting] = useState(false);
  const [rescanning, setRescanning] = useState(false);
  const [discovered, setDiscovered] = useState<DiscoveredDevice[]>([]);
  const [discovering, setDiscovering] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [addSelected, setAddSelected] = useState<DiscoveredDevice | null>(null);
  const [addConfig, setAddConfig] = useState<AddDeviceForm>(EMPTY_ADD);
  const [addSaving, setAddSaving] = useState(false);
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [sortKey, setSortKey] = useState<SortKey>("name");
  const [sortDir, setSortDir] = useState<SortDir>("asc");
  const [candidateByICCID, setCandidateByICCID] = useState<Record<string, SimCardCandidate>>({});

  const selectedIdRef = useRef(selectedId);
  selectedIdRef.current = selectedId;
  const activeTabRef = useRef(activeTab);
  activeTabRef.current = activeTab;
  const searchParamsRef = useRef(searchParams);
  searchParamsRef.current = searchParams;
  const discoveryInFlightRef = useRef(false);

  const isMobile = useMediaQuery("(max-width: 767px)");

  const handleBackToList = useCallback(() => {
    setSelectedId("");
    const p = new URLSearchParams(searchParamsRef.current);
    p.delete("device");
    p.delete("tab");
    setSearchParams(p, { replace: true });
  }, [setSearchParams]);

  const loadConfig = useCallback(async (id: string) => {
    if (!id) {
      setEditConfig(null);
      setConfigSnapshot("");
      return;
    }
    try {
      const agentQuery = selectedAgentId ? `?agent_id=${encodeURIComponent(selectedAgentId)}` : "";
      const res = await api<{ config?: DeviceConfig }>(`/devices/${id}/config${agentQuery}`);
      const cfg = res?.config ? (JSON.parse(JSON.stringify(res.config)) as DeviceConfig) : null;
      if (cfg && isQmiControl(cfg.controlDevice)) cfg.deviceBackend = "qmi";
      if (cfg?.id && cfg.id !== id) setSelectedId(cfg.id);
      setEditConfig(cfg);
      setConfigSnapshot(JSON.stringify(cfg));
    } catch {
      setEditConfig(null);
      setConfigSnapshot("");
    }
  }, [selectedAgentId]);

  const loadDiscovered = useCallback(async (resetSelection = true, showLoading = true) => {
    if (discoveryInFlightRef.current) return;
    discoveryInFlightRef.current = true;
    if (showLoading) setDiscovering(true);
    if (resetSelection) {
      setDiscovered([]);
      setAddSelected(null);
      setAddConfig(EMPTY_ADD);
    }
    try {
      if (!selectedAgentId) {
        if (showLoading) setDiscovering(false);
        return;
      }
      await api(`/devices/actions/rescan?agent_id=${encodeURIComponent(selectedAgentId)}`, {
        method: "POST",
        body: {},
      });
      const res = await api<{ devices?: DiscoveredDevice[] }>(`/devices/discovered?agent_id=${encodeURIComponent(selectedAgentId)}`);
      const devices = Array.isArray(res?.devices) ? res!.devices! : [];
      setDiscovered(devices);
    } catch {
      if (resetSelection) setDiscovered([]);
    } finally {
      discoveryInFlightRef.current = false;
      if (showLoading) setDiscovering(false);
    }
  }, [selectedAgentId]);
  const selectDevice = useCallback(
    (id: string, updateUrl = true) => {
      const clean = (id || "").trim();
      if (!clean) return;
      setSelectedId(clean);
      if (updateUrl) {
        const p = new URLSearchParams(searchParamsRef.current);
        p.set("device", clean);
        p.set("tab", activeTabRef.current);
        setSearchParams(p, { replace: true });
      }
      void loadConfig(clean);
    },
    [loadConfig, setSearchParams],
  );

  const loadCandidateStatus = useCallback(async () => {
    try {
      const registered = await listCards();
      const candidates = await listCardCandidates(registered);
      const next: Record<string, SimCardCandidate> = {};
      for (const candidate of candidates) {
        const iccid = candidate.iccid.replace(/\s+/g, "");
        const existing = next[iccid];
        next[iccid] = existing
          ? {
              ...existing,
              conflict: existing.conflict === true || candidate.conflict === true,
              state:
                existing.state === "stable" || candidate.state === "stable"
                  ? "stable"
                  : existing.state === "pending" || candidate.state === "pending"
                    ? "pending"
                    : "offline",
              hostType: existing.hostType || candidate.hostType,
            }
          : candidate;
      }
      setCandidateByICCID(next);
    } catch {
      setCandidateByICCID({});
    }
  }, []);

  const loadDevices = useCallback(
    async (manual: boolean) => {
      if (!selectedAgentId) {
        setList([]);
        setSelectedId("");
        setEditConfig(null);
        setConfigSnapshot("");
        return;
      }
      if (manual) {
        setListLoading(true);
        setListError(null);
      }
      try {
        const endpoint = selectedAgentId
          ? `/agents/${encodeURIComponent(selectedAgentId)}/devices`
          : "/devices";
        const res = await api<{ devices?: DeviceListItem[] }>(endpoint);
        const devices = res?.devices || [];
        setList(devices);
        void loadCandidateStatus();
        setLastOkAt(Date.now());
        if (manual) setListError(null);
        const urlDevice = (searchParamsRef.current.get("device") || "").trim();
        let next = selectedIdRef.current;
        if (urlDevice) next = urlDevice;
        else if (!next && devices.length) next = devices[0].id;
        else if (next && devices.length === 0) next = "";
        if (next !== selectedIdRef.current) {
          setSelectedId(next);
          if (next) await loadConfig(next);
          else {
            setEditConfig(null);
            setConfigSnapshot("");
          }
        }
      } catch (e) {
        if (manual) setListError({ message: apiMessage(e) || t("加载设备信息失败"), status: (e as { status?: number })?.status });
      } finally {
        if (manual) setListLoading(false);
      }
    },
    [loadCandidateStatus, loadConfig, selectedAgentId],
  );

  const refreshAll = useCallback(async () => {
    await loadDevices(false);
  }, [loadDevices]);

  const refreshSoon = useCallback(
    (ms: number) => {
      window.setTimeout(() => {
        refreshAll().catch(() => {});
      }, ms);
    },
    [refreshAll],
  );

  const handleProfileChanged = useCallback(() => {
    refreshAll().catch(() => {});
    refreshSoon(2000);
    refreshSoon(5000);
    refreshSoon(10000);
  }, [refreshAll, refreshSoon]);

  const handleTabChange = useCallback(
    (key: string) => {
      setActiveTab(key);
      const p = new URLSearchParams(searchParamsRef.current);
      if (selectedIdRef.current) p.set("device", selectedIdRef.current);
      p.set("tab", key);
      setSearchParams(p, { replace: true });
    },
    [setSearchParams],
  );

  const handleRebootModem = useCallback(async () => {
    const id = selectedIdRef.current.trim();
    if (!id) return false;
    const ok = await confirmDialog(tf("确定对设备 {id} 发送重启模组指令？设备将在此期间脱网和失联数秒。", { id }), t("确认重启"), {
      confirmText: t("立即重启"),
      cancelText: t("取消"),
      type: "warning",
    });
    if (!ok) return false;
    setRebooting(true);
    try {
      await api(`/devices/${id}/actions/reboot`, { method: "POST" });
      message.success(t("重启指令已送达，设备正在重新启动"));
      refreshAll().catch(() => {});
      refreshSoon(5000);
      return true;
    } catch (e) {
      message.error(apiMessage(e) || t("指令下发失败"));
      return false;
    } finally {
      setRebooting(false);
    }
  }, [refreshAll, refreshSoon]);

  const handleRescan = useCallback(async () => {
    if (!selectedAgentId) return;
    setRescanning(true);
    try {
      await Promise.all([loadDevices(true), loadDiscovered()]);
      message.success(t("设备重新扫描完成"));
    } catch (e) {
      message.error(apiMessage(e) || t("重新扫描失败"));
    } finally {
      setRescanning(false);
    }
  }, [loadDevices, loadDiscovered, selectedAgentId]);

  const handleSaveConfig = useCallback(async () => {
    const id = selectedIdRef.current.trim();
    if (!id || !editConfig) return;
    setSaving(true);
    try {
      const res = await api<{ requiresRestart?: boolean; warning?: string }>(`/devices/${id}`, {
        method: "PUT",
        body: { config: editConfig },
      });
      if (res?.warning) message.warning(res.warning);
      else if (res?.requiresRestart) message.warning(t("配置已保存，但部分变更需要重启服务后生效"));
      else message.success(t("配置已保存"));
      setConfigSnapshot(JSON.stringify(editConfig));
      await loadDevices(false);
      await loadConfig(id);
    } catch (e) {
      message.error(apiMessage(e) || t("保存失败"));
    } finally {
      setSaving(false);
    }
  }, [editConfig, loadDevices, loadConfig]);

  const handleDeleteDevice = useCallback(async () => {
    const id = selectedIdRef.current.trim();
    if (!id) return;
    const ok = await confirmDialog(tf("确定删除设备 {id} 的配置？删除后该设备将停止接管（代理/网络/AT）。", { id }), t("确认删除"), {
      confirmText: t("删除"),
      cancelText: t("取消"),
      type: "warning",
    });
    if (!ok) return;
    setDeleting(true);
    try {
      await api(`/devices/${id}`, { method: "DELETE" });
      message.success(t("设备已删除"));
      setEditConfig(null);
      setConfigSnapshot("");
      setSelectedId("");
      const p = new URLSearchParams(searchParamsRef.current);
      p.delete("device");
      setSearchParams(p, { replace: true });
      await loadDevices(true);
    } catch (e) {
      message.error(apiMessage(e) || t("删除失败"));
    } finally {
      setDeleting(false);
    }
  }, [loadDevices, setSearchParams]);

  const openAddDialog = useCallback(() => {
    setAddOpen(true);
    setAddSelected(null);
    setAddConfig(EMPTY_ADD);
  }, []);

  const selectDiscovered = useCallback((d: DiscoveredDevice) => {
    if (d.discoveryIssue === "pcsc_service_unavailable") {
      message.warning(t("系统已发现 USB 读卡器，但 PC/SC 服务未运行；请安装并启动 pcscd 后重新扫描。"));
      return;
    }
    if (d.discoveryIssue === "pcsc_driver_missing") {
      message.warning(t("系统已发现 USB 读卡器，但 PC/SC 驱动未加载；请安装 libccid 或厂商驱动后重新扫描。"));
      return;
    }
    if (d.discoveryIssue === "at_probe_pending") {
      message.warning(t("设备识别尚未完成，请稍后重新扫描。"));
      return;
    }
    if (d.discoveryIssue === "at_port_missing") {
      message.warning(
        t(
          d.vendorId === 0x19d2
            ? "已发现该模组，但未找到 AT 串口：通常是 usbserial 通用驱动未绑定该 PID。可执行 `echo 19d2 <pid> | sudo tee /sys/bus/usb-serial/drivers/generic/new_id` 后重新扫描。"
            : "已发现该模组，但未找到 AT 串口：通常是 option 驱动未认该 PID 或模组处于 MBIM/RNDIS 组态。可 `echo 2c7c <pid> | sudo tee /sys/bus/usb-serial/drivers/option1/new_id` 后重扫，或用 AT+QCFG 切到 QMI+AT 组态。",
        ),
      );
      return;
    }
    if (d.degraded) {
      message.warning(t("无法读取该设备 IMEI（可能控制口挂死），请执行 AT!RESET 或切换组态后重试"));
      return;
    }
    setAddSelected(d);
    setAddConfig((prev) => {
      const mode = String(d.mode || "").toLowerCase();
      const isReader = d.hardwareKind === "pcsc" || mode === "pcsc";
      const backend = isReader ? "pcsc" : mode === "mbim" ? "mbim" : isQmiControl(d.controlPath) || (mode === "qmi" && d.controlPath) ? "qmi" : "at";
      let id = prev.id;
      if (isReader && !id.startsWith("Reader-")) {
        const suffix = id.replace(/^Reader-/i, "");
        id = "Reader-" + suffix;
      }
      return {
        ...prev,
        id,
        agentId: d.agentId || selectedAgentId,
        remoteDeviceId: d.remoteDeviceId || d.discoveryKey,
        remoteHardwareGeneration: d.hardwareGeneration || "",
        interface: d.netInterface || "",
        atPort: d.atPort || "",
        controlDevice: d.controlPath || "",
        modemImei: isReader ? "" : d.imei || "",
        pcscImeiMode: "random",
        pcscImei: "",
        usbPath: d.usbPath || "",
        deviceBackend: backend,
		deviceType: isReader ? "usb_sim_reader" : (d.deviceType && d.deviceType !== "usb_sim_reader" ? d.deviceType : (prev.deviceType !== "usb_sim_reader" ? prev.deviceType : "")),
		esimTransport: isReader ? "pcsc" : backend,
      };
    });
  }, [selectedAgentId]);

  const saveAdd = useCallback(async () => {
    setAddSaving(true);
    try {
      if (!addSelected) {
        message.warning(t("请选择一个未配置设备"));
        return;
      }
	  if (!addConfig.deviceType) {
		message.warning(t("请选择设备类型"));
		return;
	  }
	  if (addConfig.deviceType === "usb_sim_reader" && !addConfig.id.startsWith("Reader-")) {
		message.warning(t("读卡器设备 ID 必须以 Reader- 开头"));
		return;
	  }
      const res = await api<{ warning?: string; started?: boolean }>("/devices", { method: "POST", body: { config: addConfig } });
      if (res?.warning) message.warning(res.warning);
      else if (res?.started === true) message.success(t("设备已添加并开始接管"));
      else message.success(t("设备配置已添加"));
      setAddOpen(false);
      await loadDevices(true);
    } catch (e) {
      message.error(apiMessage(e) || t("添加失败"));
    } finally {
      setAddSaving(false);
    }
  }, [addSelected, addConfig, loadDevices]);

  const handleCopyText = useCallback((text: string) => {
    const clean = (text || "").trim();
    if (!clean || clean === "--" || clean === "---") return;
    void copyText(clean, t("已复制"));
  }, []);

  // URL -> state sync.
  useEffect(() => {
    const tab = (searchParams.get("tab") || "").trim();
    if (VALID_TABS.has(tab) && tab !== activeTabRef.current) setActiveTab(tab);
    const dev = (searchParams.get("device") || "").trim();
    if (dev && dev !== selectedIdRef.current) {
      setSelectedId(dev);
      void loadConfig(dev);
    }
  }, [searchParams, loadConfig]);

  // Initial load + background polling.
  useEffect(() => {
    void loadDevices(true);
  }, [loadDevices]);
  usePolling(() => {
    void loadDevices(false);
  }, 15000, false);

  const filteredDevices = useMemo(() => {
    const q = query.trim().toLowerCase();
    let d = list.slice();
    if (statusFilter === "online") d = d.filter((c) => isDeviceOnline(c));
    else if (statusFilter === "offline") d = d.filter((c) => !c?.running && !isRecoveringPhase(c.lifecyclePhase));
    if (q) {
      d = d.filter((c) =>
        `${c?.id || ""} ${c?.name || ""} ${c?.modem?.iccid || ""} ${c?.modem?.imei || ""} ${c?.interface || ""}`
          .toLowerCase()
          .includes(q),
      );
    }
    d.sort((a, b) => {
      if (sortKey === "name") {
        const an = (a.name || a.id || "").toLowerCase();
        const bn = (b.name || b.id || "").toLowerCase();
        return an < bn ? (sortDir === "asc" ? -1 : 1) : an > bn ? (sortDir === "asc" ? 1 : -1) : 0;
      }
      if (sortKey === "signal") {
        const av = Number(a?.modem?.signalDbm ?? -999);
        const bv = Number(b?.modem?.signalDbm ?? -999);
        return sortDir === "asc" ? av - bv : bv - av;
      }
      return 0;
    });
    return d;
  }, [list, query, statusFilter, sortKey, sortDir]);

  const selectedListItem = useMemo(() => list.find((d) => d.id === selectedId) || null, [list, selectedId]);
  const configDirty = editConfig && configSnapshot ? JSON.stringify(editConfig) !== configSnapshot : false;
  const unconfiguredDiscovered = useMemo(() => discovered.filter((d) => !d.configured), [discovered]);

  useEffect(() => {
    if (activeTab !== "esim" && activeTab !== "config") setActiveTab("config");
  }, [activeTab]);

  useEffect(() => {
    if (!addOpen) return;
    void loadDiscovered(true, true);
    const timer = window.setInterval(() => {
      void loadDiscovered(false, false);
    }, 5000);
    return () => window.clearInterval(timer);
  }, [addOpen, loadDiscovered]);
  const tabItems = [
    { key: "esim", label: t("eSIM") },
    { key: "config", label: t("配置") },
  ];

  return (
    <div className="devices-page">
      <PageHeader
        title={t("设备管理")}
        subtitle={t("管理设备硬件、eSIM Profile 和设备配置")}
      />
      <AgentManager selectedAgentId={selectedAgentId} onSelect={setSelectedAgentId}>
        <div className="space-y-4">
          {selectedAgentId ? (
            <div className="flex flex-wrap items-center justify-end gap-2">
              <RefreshButton loading={listLoading} onClick={refreshAll} />
              <Button loading={rescanning} onClick={handleRescan} icon={<ArrowSyncRegular />}>
                {t("重新扫描")}
              </Button>
              <Button variant="primary" onClick={openAddDialog} className="!border-0" icon={<AddRegular />}>
                {t("添加设备")}
              </Button>
            </div>
          ) : null}
          {selectedAgentId && listError ? (
            <ErrorState
              title={t("设备列表加载失败")}
              message={listError.message}
              statusCode={listError.status}
              lastSuccessAt={lastOkAt}
              retryText={t("重试")}
              onRetry={() => void loadDevices(true)}
            />
          ) : null}
          {selectedAgentId ? (
            <div className="devices-layout">
              {(!isMobile || !selectedId) && (
                <DeviceListPanel
                  embedded
                  loading={listLoading}
                  query={query}
                  statusFilter={statusFilter}
                  sortKey={sortKey}
                  sortDir={sortDir}
                  selectedId={selectedId}
                  filteredDevices={filteredDevices}
                  candidateByICCID={candidateByICCID}
                  onQueryChange={setQuery}
                  onStatusFilterChange={setStatusFilter}
                  onSortKeyChange={setSortKey}
                  onSortDirChange={setSortDir}
                  onSelectDevice={(id) => selectDevice(id)}
                />
              )}
              <div className={`min-w-0 space-y-4 ${isMobile && selectedId ? "" : isMobile ? "hidden" : ""}`}>
                {isMobile && selectedId && selectedListItem ? (
                  <Button variant="text" onClick={handleBackToList} icon={<ChevronLeftRegular />}>
                    {t("返回")}
                  </Button>
                ) : null}
                {selectedListItem ? (
                  <>
                    <DeviceDetailHeader device={selectedListItem} onCopyText={handleCopyText} />
                    <div className="device-detail-tabs ui-card p-6">
                      <Tabs tabs={tabItems} value={activeTab} onChange={handleTabChange} />
                      <div className="mt-5">
                        {activeTab === "esim" ? (
                          <DeviceEsimTab
                            deviceId={selectedListItem.id}
                            hostType={selectedListItem.deviceType}
                            deviceImei={selectedListItem.modem?.imei || ""}
                            isActive={activeTab === "esim"}
                            deviceOnline={isDeviceOnline(selectedListItem)}
                            rebooting={rebooting}
                            onRebootModem={handleRebootModem}
                            onProfileChanged={handleProfileChanged}
                          />
                        ) : null}
                        {activeTab === "config" ? (
                          <DeviceConfigTab editConfig={editConfig} deviceStatus={selectedListItem} saving={saving} deleting={deleting} onSave={handleSaveConfig} onDelete={handleDeleteDevice} onEditConfig={setEditConfig} />
                        ) : null}
                      </div>
                    </div>
                  </>
                ) : listLoading ? (
                  <ListSkeleton rows={8} />
                ) : (
                  <div className="ui-card p-10 text-center text-sm text-gray-400">
                    {list.length === 0 ? t("该 Agent 当前没有已配置设备") : t("请选择设备查看详情")}
                  </div>
                )}
              </div>
            </div>
          ) : (
            <div className="py-8 text-center text-sm text-gray-500">
              {t("请先选择 VoNet-Agent")}
            </div>
          )}
        </div>
      </AgentManager>
      <DeviceAddDialog
        open={addOpen}
        discovering={discovering}
        unconfiguredDiscovered={unconfiguredDiscovered}
        addSelected={addSelected}
        addConfig={addConfig}
        addSaving={addSaving}
        onClose={() => setAddOpen(false)}
        onRefresh={() => void loadDiscovered()}
        onSelectDevice={selectDiscovered}
        onConfigChange={setAddConfig}
        onSave={saveAdd}
      />
    </div>
  );
}
