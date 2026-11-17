import { useCallback, useEffect, useState, type ReactNode } from "react";
import { ArrowSyncRegular, DeleteRegular, MailRegular, PhoneRegular, PowerRegular, SettingsRegular } from "@fluentui/react-icons";
import { api, apiMessage } from "../../api";
import type { SimCard } from "../../types";
import type { DeviceDetail } from "../devices/types";
import { Button, EmptyState, ErrorState, ListSkeleton, Spinner, Switch, Tabs, confirmDialog, message, type TabItem } from "../ui";
import { OverviewNetworkCard } from "../devices/OverviewNetworkCard";
import { OverviewVowifiCard } from "../devices/OverviewVowifiCard";
import { OverviewRealtimeTrafficCard } from "../devices/OverviewRealtimeTrafficCard";
import { OverviewSimPanel } from "../devices/OverviewSimPanel";
import { OverviewNetworkPanel } from "../devices/OverviewNetworkPanel";
import { OverviewTrafficChart } from "../devices/OverviewTrafficChart";
import { OperatorSelectionDialog } from "../devices/OperatorSelectionDialog";
import { CellLockDialog } from "../devices/CellLockDialog";
import { E911SetupDialog } from "../devices/E911SetupDialog";
import { useDeviceCellLock } from "../devices/useDeviceCellLock";
import { isVoWiFiInUse, simOperatorDisplay } from "../devices/shared";
import { usePolling } from "../../lib/usePolling";
import { tf, useI18n } from "../../lib/i18n";

const NETWORK_POLL_MS = 1000;
const NETWORK_DISABLE_TIMEOUT_MS = 35000;
const NETWORK_ENABLE_TIMEOUT_MS = 80000;

interface NetworkActionStatus {
  enabled: boolean;
  connected: boolean;
  phase: "unknown" | "starting" | "connected" | "stopping" | "recovering" | "disabled" | "failed";
  modemPhase?: "rebooting" | "";
  lastError?: string;
}

interface Props {
  card: SimCard;
  tabs: TabItem[];
  activeTab: string;
  onTabChange: (key: string) => void;
  onOpenSms: () => void;
  onOpenCall: () => void;
  onDelete: () => void;
  deleting: boolean;
  children?: ReactNode;
}

export function CardOverviewTab({ card, tabs, activeTab, onTabChange, onOpenSms, onOpenCall, onDelete, deleting, children }: Props) {
  const { t } = useI18n();
  const deviceId = card.binding?.deviceId?.trim() || "";
  const [device, setDevice] = useState<DeviceDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [operatorOpen, setOperatorOpen] = useState(false);
  const [networkToggling, setNetworkToggling] = useState(false);
  const [networkTarget, setNetworkTarget] = useState<boolean | null>(null);
  const [reconnecting, setReconnecting] = useState(false);
  const [rebooting, setRebooting] = useState(false);
  const [e911Open, setE911Open] = useState(false);
  const hostReady = card.status === "active" && !!card.binding?.deviceId;

  const loadDevice = useCallback(async (showLoading = false) => {
    if (!deviceId || !hostReady) {
      setDevice(null);
      setError("");
      setLoading(false);
      return;
    }
    if (showLoading) setLoading(true);
    try {
      const result = await api<{ devices?: DeviceDetail[] }>(`/devices/${encodeURIComponent(deviceId)}/overview`);
      const overview = result.devices?.[0] || null;
      setDevice(overview);
      setError("");
    } catch (cause) {
      setError(apiMessage(cause) || t("设备状态加载失败"));
    } finally {
      setLoading(false);
    }
  }, [deviceId, hostReady, t]);

  useEffect(() => {
    setDevice(null);
    void loadDevice(true);
  }, [loadDevice]);

  usePolling(() => {
    if (deviceId && hostReady && !networkToggling) void loadDevice(false);
  }, 5000, false);

  const refreshSoon = useCallback((delay: number) => {
    window.setTimeout(() => void loadDevice(false), delay);
  }, [loadDevice]);

  const handleReconnectVoWiFi = useCallback(async () => {
    if (!deviceId || reconnecting) return;
    const confirmed = await confirmDialog(
      tf("确定对设备 {id} 发起 VoWiFi 环境的重新连接拨号？这将在后台重新注册 IMS 链路。", { id: deviceId }),
      t("重连 VoWiFi"),
      { confirmText: t("确定重连"), cancelText: t("取消"), type: "info" },
    );
    if (!confirmed) return;
    setReconnecting(true);
    try {
      await api(
        `/devices/${encodeURIComponent(deviceId)}/vowifi/actions/reconnect?iccid=${encodeURIComponent(card.iccid)}`,
        { method: "POST" },
      );
      message.success(t("已触发重连指令，VoWiFi 服务正在重启..."));
      refreshSoon(2000);
      refreshSoon(5000);
    } catch (cause) {
      message.error(apiMessage(cause) || t("重连命令下发失败"));
    } finally {
      setReconnecting(false);
    }
  }, [card.iccid, deviceId, reconnecting, refreshSoon, t]);

  const handleNetworkToggle = useCallback(async (enabled: boolean) => {
    if (!deviceId || networkToggling) return;
    setNetworkToggling(true);
    setNetworkTarget(enabled);
    message.info(enabled ? t("正在开启漫游数据，请稍候") : t("正在关闭漫游数据，请稍候"));
    try {
      await api<NetworkActionStatus>(`/devices/${encodeURIComponent(deviceId)}/network`, {
        method: "PATCH",
        body: { enabled },
      });
      const deadline = Date.now() + (enabled ? NETWORK_ENABLE_TIMEOUT_MS : NETWORK_DISABLE_TIMEOUT_MS);
      let terminal: NetworkActionStatus | null = null;
      let lastError: unknown = null;
      while (Date.now() < deadline) {
        await new Promise((resolve) => window.setTimeout(resolve, NETWORK_POLL_MS));
        try {
          const status = await api<NetworkActionStatus>(`/devices/${encodeURIComponent(deviceId)}/network`);
          lastError = null;
          setDevice((current) => current ? {
            ...current,
            networkEnabled: status.enabled,
            networkConnected: status.connected,
            networkPhase: status.phase,
            modemPhase: status.modemPhase || "",
            networkError: status.lastError || "",
          } : current);
          if (status.phase === "failed" || (enabled && status.phase === "connected") || (!enabled && status.phase === "disabled")) {
            terminal = status;
            break;
          }
        } catch (pollError) {
          lastError = pollError;
        }
      }
      if (!terminal) throw new Error(apiMessage(lastError) || t("操作超时，请检查当前数据会话状态"));
      if (terminal.phase === "failed") throw new Error(terminal.lastError || (enabled ? t("开启漫游数据失败") : t("关闭漫游数据失败")));
      message.success(enabled ? t("漫游数据已开启，仅供 Export Proxy 使用") : t("漫游数据已关闭"));
    } catch (cause) {
      message.error(apiMessage(cause) || (enabled ? t("开启漫游数据失败") : t("关闭漫游数据失败")));
    } finally {
      setNetworkToggling(false);
      setNetworkTarget(null);
      await loadDevice(false);
    }
  }, [deviceId, loadDevice, networkToggling, t]);

  const handleRebootModem = useCallback(async () => {
    if (!deviceId || rebooting) return;
    const confirmed = await confirmDialog(
      tf("确定对设备 {id} 发送重启模组指令？设备将在此期间脱网和失联数秒。", { id: deviceId }),
      t("确认重启"),
      { confirmText: t("立即重启"), cancelText: t("取消"), type: "warning" },
    );
    if (!confirmed) return;
    setRebooting(true);
    try {
      await api(`/devices/${encodeURIComponent(deviceId)}/actions/reboot`, { method: "POST" });
      message.success(t("重启指令已送达，设备正在重新启动"));
      refreshSoon(3000);
      refreshSoon(8000);
    } catch (cause) {
      message.error(apiMessage(cause) || t("指令下发失败"));
    } finally {
      setRebooting(false);
    }
  }, [deviceId, rebooting, refreshSoon, t]);

  const supportsCellLock = device?.supportsCellLock === true;
  const cellLock = useDeviceCellLock(deviceId, supportsCellLock);
  const isReader =
    card.hostType === "usb_sim_reader" ||
    card.binding?.deviceType === "usb_sim_reader" ||
    card.binding?.backendMode === "pcsc" ||
    card.policy?.runtimeMode === "pcsc" ||
    device?.deviceType === "usb_sim_reader";
  const isNative410 = device?.deviceType === "wifi_410";
  const vowifiInUse = isVoWiFiInUse(device, isReader);
  const canReconnectVoWiFi = !!device && (isReader || !!device.vowifiEnabled || vowifiInUse);
  const operatorDisplay = device ? simOperatorDisplay(device) : card.carrier || card.spn || "--";

  return (
    <div className="overview-tab-shell">
      {!hostReady ? (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <EmptyState
            title={deviceId ? t("卡片当前离线") : t("卡片当前未绑定设备")}
            subtitle={t("设备稳定读取到此 ICCID 后，将重新绑定并恢复实时 VoWiFi 和蜂窝网络状态")}
          />
          <OverviewSimPanel
            card={card}
            simOperatorDisplay={operatorDisplay}
            customPhoneNumber={card.policy?.customPhoneNumber}
            variant="summary"
            e911Starting={false}
            onSetupE911={() => setE911Open(true)}
          />
        </div>
      ) : loading && !device ? (
        <ListSkeleton rows={6} />
      ) : error && !device ? (
        <ErrorState title={t("卡片设备状态加载失败")} message={error} retryText={t("重试")} onRetry={() => void loadDevice(true)} />
      ) : device ? (
        <>
          <section className="overview-runtime-hero">
            <div className="overview-runtime-header">
              <div className="min-w-0">
                <h2>{card.displayName || device.name || t("设备概览")}</h2>
                <p>{vowifiInUse ? "VoWiFi" : "VoLTE"} · {operatorDisplay} · {device.backendMode || "--"} · {device.interface || "--"}</p>
              </div>
              <div className="overview-runtime-actions">
                <Button icon={<MailRegular />} onClick={onOpenSms}>{t("短信")}</Button>
                <Button icon={<PhoneRegular />} onClick={onOpenCall}>{t("通话")}</Button>
              </div>
            </div>
            <div className="overview-runtime-grid">
              {vowifiInUse ? (
                <>
                  <OverviewVowifiCard device={device} card={card} operatorDisplay={operatorDisplay} />
                  <OverviewSimPanel
                    device={device}
                    card={card}
                    simOperatorDisplay={operatorDisplay}
                    customPhoneNumber={card.policy?.customPhoneNumber}
                    variant="summary"
                    e911Starting={false}
                    onSetupE911={() => setE911Open(true)}
                  />
                </>
              ) : (
                <>
                  <OverviewNetworkCard
                    device={device}
                    card={card}
                    onOpenOperatorSelection={isReader ? undefined : () => setOperatorOpen(true)}
                    onOpenCellLock={supportsCellLock ? cellLock.openDialog : undefined}
                    cellLocked={!!cellLock.status?.target}
                    cellLockStatusKnown={cellLock.status !== null}
                  />
                  <OverviewSimPanel
                    device={device}
                    card={card}
                    simOperatorDisplay={operatorDisplay}
                    customPhoneNumber={card.policy?.customPhoneNumber}
                    variant="summary"
                    e911Starting={false}
                    onSetupE911={() => setE911Open(true)}
                  />
                </>
              )}
            </div>
          </section>
          <Tabs tabs={tabs} value={activeTab} onChange={onTabChange} />
          {activeTab === "overview" ? (
            <>
              <div className="overview-lower-grid">
                <OverviewNetworkPanel
                  device={device}
                  reader={isReader}
                  trafficMinuteRx={device.traffic?.rx || ""}
                  trafficMinuteTx={device.traffic?.tx || ""}
                  trafficSpeedRx={device.traffic?.rate || ""}
                  trafficSpeedTx={device.traffic?.rateTx || ""}
                />
                <OverviewSimPanel
                  device={device}
                  card={card}
                  simOperatorDisplay={operatorDisplay}
                  customPhoneNumber={card.policy?.customPhoneNumber}
                  variant="details"
                />
              </div>
              <div className="overview-support-grid">
                {!vowifiInUse ? <OverviewRealtimeTrafficCard device={device} /> : null}
              </div>
              {device.networkEnabled && !isReader ? (
                <OverviewTrafficChart deviceId={device.id} />
              ) : null}
              <section className="overview-quick-actions">
                <div className="overview-quick-header">
                  <div>
                    <strong><SettingsRegular /> {t("快捷操作")}</strong>
                    <span>{t("连接与通话")}</span>
                  </div>
                  <small>{t("常用设备操作")}</small>
                </div>
                <div className="overview-quick-grid">
                  {!isReader ? (
                    <div className="overview-action-card">
                      <div>
                        <strong>{t("数据漫游")}</strong>
                        <span>{device.networkEnabled ? t("已开启") : t("已关闭")}</span>
                      </div>
                      <Switch
                        checked={!!device.networkEnabled}
                        loading={networkToggling}
                        disabled={networkToggling || !device.interface}
                        onChange={(enabled) => void handleNetworkToggle(enabled)}
                        size="small"
                        ariaLabel={t("数据漫游")}
                      />
                    </div>
                  ) : null}
                  {!isReader && !isNative410 ? (
                    <div className="overview-action-card">
                      <div>
                        <strong>{t("重启模组")}</strong>
                        <span>{t("断线后自动恢复连接")}</span>
                      </div>
                      <Button
                        loading={rebooting}
                        variant="danger"
                        className="overview-danger-button"
                        onClick={() => void handleRebootModem()}
                        icon={<PowerRegular />}
                      >
                        {t("重启模组")}
                      </Button>
                    </div>
                  ) : null}
                  <div className="overview-action-card">
                    <div>
                      <strong>{t("重连 VoWiFi")}</strong>
                      <span>{canReconnectVoWiFi ? t("重新建立 IMS 链路") : t("不适用")}</span>
                    </div>
                    <Button
                      loading={reconnecting}
                      disabled={!canReconnectVoWiFi}
                      onClick={() => void handleReconnectVoWiFi()}
                      icon={<ArrowSyncRegular />}
                    >
                      {t("重连 VoWiFi")}
                    </Button>
                  </div>
                  <div className="overview-action-card overview-action-danger">
                    <div>
                      <strong>{t("删除卡片")}</strong>
                      <span>{t("永久删除此 SIM 卡配置")}</span>
                    </div>
                    <Button
                      loading={deleting}
                      variant="danger"
                      className="overview-danger-button"
                      onClick={onDelete}
                      icon={<DeleteRegular />}
                    >
                      {t("删除卡片")}
                    </Button>
                  </div>
                </div>
                <p>{t("快捷操作会按当前设备能力显示；执行危险操作前会弹出确认。")}</p>
              </section>
            </>
          ) : children}
          {!isReader ? (
            <OperatorSelectionDialog
              open={operatorOpen}
              deviceId={device.id}
              scanBlockedReason={
                device.flightMode || device.modem?.operatingMode === 0 || device.modem?.operatingMode === 4
                  ? t("运营商扫描需要开启蜂窝射频；请先关闭飞行模式，再手动开始扫描。")
                  : ""
              }
              onClose={() => setOperatorOpen(false)}
              onUpdated={() => void loadDevice(false)}
            />
          ) : null}
          {supportsCellLock && cellLock.dialogOpen ? <CellLockDialog cellLock={cellLock} /> : null}
          <E911SetupDialog
            key={device.id}
            open={e911Open}
            deviceId={device.id}
            onClose={() => setE911Open(false)}
            onDone={() => void loadDevice(false)}
          />
        </>
      ) : null}
    </div>
  );
}
