import { useCallback, useEffect, useState } from "react";
import { api, apiMessage } from "../../api";
import type { DeviceConfig, SimCard } from "../../types";
import { DeviceConfigTab } from "../devices/DeviceConfigTab";
import { ErrorState, ListSkeleton, message } from "../ui";
import { FieldRow } from "../devices/FieldRow";
import { useI18n } from "../../lib/i18n";

function ClearedDeviceConfig({ showATPort = true }: { showATPort?: boolean }) {
  const { t } = useI18n();
  const rows = [
    t("ID"),
    t("名称"),
    t("设备类型"),
    t("IMEI 绑定"),
    t("设备路径"),
    t("网卡接口"),
    ...(showATPort ? [t("AT 端口")] : []),
    t("控制设备"),
    t("SIM PIN"),
  ];
  return (
    <div className="ui-panel-muted p-4">
      <div className="mb-3 text-xs font-bold uppercase tracking-wider text-gray-500">{t("设备配置")}</div>
      <div className="space-y-1.5 text-sm text-gray-700 dark:text-gray-200">
        {rows.map((label) => <FieldRow key={label} label={label} value="" />)}
      </div>
    </div>
  );
}

export function CardDeviceConfig({ card, onSaved }: { card: SimCard; onSaved?: () => void }) {
  const { t } = useI18n();
  const deviceId = card.binding?.deviceId?.trim() || "";
  const hostReady = card.status === "active" && !!card.binding?.deviceId;
  const isReader =
    card.hostType === "usb_sim_reader" ||
    card.binding?.deviceType === "usb_sim_reader" ||
    card.binding?.backendMode === "pcsc" ||
    card.policy?.runtimeMode === "pcsc";
  const [config, setConfig] = useState<DeviceConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    if (!deviceId || !hostReady) {
      setConfig(null);
      setError("");
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const result = await api<{ config?: DeviceConfig }>(`/devices/${encodeURIComponent(deviceId)}/config`);
      setConfig(result.config || null);
      setError("");
    } catch (cause) {
      setConfig(null);
      setError(apiMessage(cause) || t("设备配置加载失败"));
    } finally {
      setLoading(false);
    }
  }, [deviceId, hostReady, t]);

  useEffect(() => {
    setConfig(null);
    void load();
  }, [load]);

  const save = useCallback(async () => {
    if (!hostReady || !deviceId || !config || saving) return;
    setSaving(true);
    try {
      await api(`/devices/${encodeURIComponent(deviceId)}`, {
        method: "PUT",
        body: { config },
      });
      message.success(t("设备配置已保存"));
      await load();
      onSaved?.();
    } catch (cause) {
      message.error(apiMessage(cause) || t("设备配置保存失败"));
    } finally {
      setSaving(false);
    }
  }, [config, deviceId, hostReady, load, onSaved, saving, t]);

  if (!hostReady) return <ClearedDeviceConfig showATPort={!isReader} />;
  if (loading && !config) return <ListSkeleton rows={8} />;
  if (error && !config) {
    return <ErrorState title={t("设备配置加载失败")} message={error} retryText={t("重试")} onRetry={() => void load()} />;
  }
  return (
    <DeviceConfigTab
      editConfig={config}
      deviceStatus={null}
      saving={saving}
      deleting={false}
      showDelete={false}
      onSave={() => void save()}
      onEditConfig={setConfig}
    />
  );
}
