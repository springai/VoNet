import { useCallback, useEffect, useState } from "react";
import { ArrowSyncRegular, PowerRegular } from "@fluentui/react-icons";
import { api, apiMessage } from "../../api";
import type { SimCard } from "../../types";
import type { DeviceDetail } from "../devices/types";
import { Button, EmptyState, ErrorState, ListSkeleton, Spinner, Switch, confirmDialog, message } from "../ui";
import { OverviewNetworkCard } from "../devices/OverviewNetworkCard";
import { OverviewVowifiCard } from "../devices/OverviewVowifiCard";
import { OverviewSimPanel } from "../devices/OverviewSimPanel";
import { OverviewNetworkPanel } from "../devices/OverviewNetworkPanel";
import { OverviewTrafficChart } from "../devices/OverviewTrafficChart";
import { OperatorSelectionDialog } from "../devices/OperatorSelectionDialog";
import { CellLockButton } from "../devices/CellLockButton";
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

export function CardOverviewTab({ card }: { card: SimCard }) {
  const { t } = useI18n();
  const deviceId = card.binding?.deviceId?.trim() || "";
  const [device, setDevice] = useState<DeviceDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [operatorOpen, setOperatorOpen] = useState(false);
  const [e911Open, setE911Open] = useState(false);
  const [networkToggling, setNetworkToggling] = useState(false);
  const [networkTarget, setNetworkTarget] = useState<boolean | null>(null);
  const [reconnecting, setReconnecting] = useState(false);
  const [rebooting, setRebooting] = useState(false);
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
  const showNetworkDetails = !isReader;
  const operatorDisplay = device ? simOperatorDisplay(device) : card.carrier || card.spn || "--";

  return (
    <div className="space-y-4">
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
          <div className="flex flex-wrap items-center justify-end gap-2">
            {canReconnectVoWiFi ? (
              <Button loading={reconnecting} onClick={() => void handleReconnectVoWiFi()} icon={<ArrowSyncRegular />}>
                {t("重连 VoWiFi")}
              </Button>
            ) : !isReader ? (
              <div className="flex h-9 items-center gap-2 rounded-lg border border-gray-200 px-3 text-sm text-gray-700 dark:border-white/10 dark:text-gray-200">
                <span>{t("漫游数据")}</span>
                <Switch
                  checked={!!device.networkEnabled}
                  loading={networkToggling}
                  disabled={networkToggling || !device.interface}
                  onChange={(enabled) => void handleNetworkToggle(enabled)}
                  size="small"
                  ariaLabel={t("漫游数据")}
                />
                {networkToggling && networkTarget !== null ? (
                  <span className="inline-flex items-center gap-1 text-xs text-gray-500">
                    <Spinner className="h-3.5 w-3.5" />
                    {networkTarget ? t("正在开启...") : t("正在关闭...")}
                  </span>
                ) : null}
              </div>
            ) : null}
            {!isReader && !isNative410 ? (
              <Button loading={rebooting} onClick={() => void handleRebootModem()} icon={<PowerRegular />}>
                {t("重启模组")}
              </Button>
            ) : null}
            {device.e911SetupAvailable ? (
              <Button variant="primary" onClick={() => setE911Open(true)}>
                {t("E911地址")}
              </Button>
            ) : null}
          </div>
          <div className={`grid grid-cols-1 gap-4 ${showNetworkDetails ? "lg:grid-cols-3" : "lg:grid-cols-2"}`}>
            <section className="ui-panel-muted p-4">
              <div className="mb-3 text-xs font-bold uppercase tracking-wider text-gray-500">{t("运行状态")}</div>
              {vowifiInUse ? (
                <>
                  <OverviewVowifiCard device={device} />
                  {supportsCellLock ? <CellLockButton onClick={cellLock.openDialog} locked={!!cellLock.status?.target} statusKnown={cellLock.status !== null} /> : null}
                </>
              ) : (
                <OverviewNetworkCard
                  device={device}
                  onOpenOperatorSelection={isReader ? undefined : () => setOperatorOpen(true)}
                  onOpenCellLock={supportsCellLock ? cellLock.openDialog : undefined}
                  cellLocked={!!cellLock.status?.target}
                  cellLockStatusKnown={cellLock.status !== null}
                />
              )}
            </section>
            <OverviewSimPanel
              device={device}
              card={card}
              simOperatorDisplay={operatorDisplay}
              customPhoneNumber={card.policy?.customPhoneNumber}
              e911Starting={false}
              onSetupE911={() => setE911Open(true)}
            />
            {showNetworkDetails ? (
              <OverviewNetworkPanel
                device={device}
                trafficMinuteRx={device.traffic?.rx || ""}
                trafficMinuteTx={device.traffic?.tx || ""}
                trafficSpeedRx={device.traffic?.rate || ""}
                trafficSpeedTx={device.traffic?.rateTx || ""}
              />
            ) : null}
          </div>
          {device.networkEnabled && !isReader ? (
            <OverviewTrafficChart deviceId={device.id} />
          ) : null}
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
