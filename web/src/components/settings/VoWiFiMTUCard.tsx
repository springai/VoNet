import { useEffect, useState } from "react";
import { CheckmarkRegular, SettingsRegular } from "@fluentui/react-icons";
import { api, apiMessage } from "../../api";
import { useI18n } from "../../lib/i18n";
import { Button, Input, message } from "../ui";
import { Switch } from "../ui/Switch";
import { CardDecor, CardIcon, CardTitle } from "./Cards";

interface VoWiFiSettings {
  mtuCompatibility: boolean;
  imsApn: string;
}

export function VoWiFiMTUCard() {
  const { t } = useI18n();
  const [enabled, setEnabled] = useState(false);
  const [imsApn, setIMSAPN] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [savingMTU, setSavingMTU] = useState(false);
  const [savingIMSAPN, setSavingIMSAPN] = useState(false);
  useEffect(() => {
    let active = true;
    api<VoWiFiSettings>("/settings/vowifi").then((data) => {
      if (active) {
        setEnabled(data.mtuCompatibility === true);
        setIMSAPN(data.imsApn || "ims");
        setLoaded(true);
      }
    }).catch(() => { if (active) message.error(t("VoWiFi 兼容设置加载失败")); });
    return () => { active = false; };
  }, [t]);
  const onToggle = async (value: boolean) => {
    setSavingMTU(true);
    try {
      const data = await api<VoWiFiSettings>("/settings/vowifi", {
        method: "PUT", body: { mtuCompatibility: value },
      });
      setEnabled(data.mtuCompatibility === true);
      message.success(t("设置已保存，请重连 VoWiFi 后生效"));
    } catch (error) {
      message.error(apiMessage(error) || t("VoWiFi 兼容设置保存失败"));
    } finally { setSavingMTU(false); }
  };

  const onSaveIMSAPN = async () => {
    setSavingIMSAPN(true);
    try {
      const data = await api<VoWiFiSettings>("/settings/vowifi", {
        method: "PUT", body: { imsApn },
      });
      setIMSAPN(data.imsApn || "ims");
      message.success(t("IMS APN 已保存，请重连 VoWiFi 后生效"));
    } catch (error) {
      message.error(apiMessage(error) || t("IMS APN 保存失败"));
    } finally {
      setSavingIMSAPN(false);
    }
  };
  return (
    <div className="ui-card group relative overflow-hidden p-8">
      <CardDecor />
      <div className="relative z-10 mb-6 flex items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <CardIcon><SettingsRegular className="text-[24px]" /></CardIcon>
          <CardTitle title={t("VoWiFi 连接设置")} subtitle={t("配置 MTU 兼容模式与 ePDG IMS APN")} />
        </div>
        <Switch checked={enabled} disabled={!loaded || savingMTU || savingIMSAPN} loading={savingMTU} onChange={onToggle} />
      </div>
      <div className="relative z-10 space-y-4">
        <div>
          <div className="text-sm font-medium text-gray-800 dark:text-gray-100">
            {t("VoWiFi MTU 兼容模式")}
          </div>
          <p className="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
            {t("默认关闭。遇到 MTU 不足导致的 VoWiFi 连接问题时可尝试开启。此设置适用于所有设备，保存后请重连 VoWiFi。")}
          </p>
        </div>
        <div className="rounded-lg bg-gray-50 p-4 dark:bg-white/5">
          <label htmlFor="vowifi-ims-apn" className="text-xs font-bold uppercase tracking-wider text-gray-500">
            {t("ePDG IMS APN")}
          </label>
          <div className="mt-2 flex items-center gap-2">
            <Input
              id="vowifi-ims-apn"
              value={imsApn}
              onChange={(event) => setIMSAPN(event.target.value)}
              placeholder="ims"
              maxLength={100}
              disabled={!loaded || savingIMSAPN}
              className="flex-1"
            />
            <Button
              variant="primary"
              loading={savingIMSAPN}
              disabled={!loaded || savingMTU || savingIMSAPN}
              onClick={onSaveIMSAPN}
              icon={<CheckmarkRegular />}
            >
              {t("保存 IMS APN")}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
