import { useEffect, useState } from "react";
import { api, apiMessage } from "../../api";
import { Select } from "../ui";
import { useI18n } from "../../lib/i18n";

export interface PCSCIMEISelection {
  mode: "random" | "fixed";
  imei: string;
}

interface PoolItem {
  imei: string;
  boundDeviceId?: string;
}

interface PCSCIMEISelectProps {
  value: PCSCIMEISelection;
  deviceId?: string;
  disabled?: boolean;
  onChange: (value: PCSCIMEISelection) => void;
}

export function PCSCIMEISelect({ value, deviceId, disabled, onChange }: PCSCIMEISelectProps) {
  const { t } = useI18n();
  const [items, setItems] = useState<PoolItem[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const data = await api<{ items?: PoolItem[] }>("/pcsc-imei-pool");
        if (active) setItems(data.items || []);
      } catch (error) {
        if (active) console.warn(apiMessage(error));
      } finally {
        if (active) setLoading(false);
      }
    })();
    return () => {
      active = false;
    };
  }, []);

  const selectedValue = value.mode === "fixed" && value.imei ? value.imei : "random";
  const options = [
    { value: "random", label: t("随机 IMEI") },
    ...items.map((item) => ({
      value: item.imei,
      label: item.imei,
      disabled: Boolean(item.boundDeviceId && item.boundDeviceId !== deviceId),
    })),
  ];
  if (value.mode === "fixed" && value.imei && !items.some((item) => item.imei === value.imei)) {
    options.push({ value: value.imei, label: value.imei, disabled: false });
  }

  return (
    <Select
      value={selectedValue}
      disabled={disabled || loading}
      placeholder={t("随机 IMEI")}
      options={options}
      onChange={(next) => {
        if (next === "random") onChange({ mode: "random", imei: "" });
        else onChange({ mode: "fixed", imei: next });
      }}
    />
  );
}
