import { WifiOffRegular } from "@fluentui/react-icons";
import type { SimCard } from "../../types";
import type { DeviceDetail } from "./types";
import { FieldRow } from "./FieldRow";
import { useI18n } from "../../lib/i18n";

interface Props {
  device: DeviceDetail;
  card: SimCard;
  reader: boolean;
  operatorDisplay: string;
}

export function OverviewCellularStatusCard({ device, card, reader, operatorDisplay }: Props) {
  const { t } = useI18n();
  const modem = device.modem;
  const plmn = modem?.homeCarrierPlmn || card.homePlmn || [card.mcc, card.mnc].filter(Boolean).join("") || "--";

  return (
    <section className="overview-cellular-card">
      <div className="overview-panel-heading">
        <WifiOffRegular />
        <span>{t("蜂窝")}</span>
      </div>
      {reader ? (
        <div className="overview-not-applicable">
          <WifiOffRegular />
          <strong>{t("不适用")}</strong>
          <span>{t("读卡器通过 Wi-Fi Calling 提供语音和短信，不使用蜂窝网络。")}</span>
        </div>
      ) : (
        <div className="overview-cellular-rows">
          <div className="overview-cellular-title">
            <strong>{t("飞行模式")}</strong>
            <span>{device.flightMode ? t("已开启") : t("已关闭")}</span>
          </div>
          <p>{t("蜂窝网络接入当前设备的射频与数据通道。")}</p>
          <FieldRow label={t("信号")} value={typeof modem?.signalRsrp === "number" ? `${modem.signalRsrp} dBm` : "--"} />
          <FieldRow label={t("蜂窝数据")} value={device.networkConnected ? t("已建立") : t("未建立")} />
          <FieldRow label={t("SIM 归属 PLMN")} value={plmn} />
          <FieldRow label={t("运营商")} value={operatorDisplay} />
        </div>
      )}
    </section>
  );
}
