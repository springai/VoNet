import { useEffect, useRef, useState } from "react";
import type { CardKeepAliveConfig, CardKeepAliveCurrency, CardKeepAliveCycleRule } from "../../types";
import { Input, Select, Switch } from "../ui";
import { useI18n } from "../../lib/i18n";
import { centsToMoney, dateInputTimestamp, dateInputValue, moneyToCents, sanitizeMoneyInput } from "./cardKeepAliveMoney";

export { centsToMoney, dateInputValue, moneyToCents, sanitizeMoneyInput } from "./cardKeepAliveMoney";

function MoneyInput({ cents, onChange }: { cents: number; onChange: (cents: number) => void }) {
  const [raw, setRaw] = useState(() => centsToMoney(cents));
  const focusedRef = useRef(false);

  useEffect(() => {
    if (!focusedRef.current) setRaw(centsToMoney(cents));
  }, [cents]);

  return (
    <Input
      type="text"
      inputMode="decimal"
      autoComplete="off"
      value={raw}
      onChange={(event) => {
        const next = sanitizeMoneyInput(event.target.value);
        setRaw(next);
        onChange(moneyToCents(next));
      }}
      onFocus={() => { focusedRef.current = true; }}
      onBlur={() => {
        focusedRef.current = false;
        setRaw(centsToMoney(cents));
      }}
    />
  );
}

const CURRENCIES: CardKeepAliveCurrency[] = ["CNY", "USD", "GBP", "EUR", "NZD"];
const CYCLES: Array<{ value: CardKeepAliveCycleRule; labelKey: string }> = [
  { value: "custom_days", labelKey: "自定义天数" },
  { value: "monthly", labelKey: "月付" },
  { value: "quarterly", labelKey: "季付" },
  { value: "half_yearly", labelKey: "半年付" },
  { value: "yearly", labelKey: "年付" },
];

export interface CardKeepAliveFieldsProps {
  value: CardKeepAliveConfig;
  phoneNumber: string;
  operatorName: string;
  onChange: (value: CardKeepAliveConfig) => void;
  onPhoneNumberChange: (value: string) => void;
  onOperatorNameChange: (value: string) => void;
}

export function CardKeepAliveFields({
  value,
  phoneNumber,
  operatorName,
  onChange,
  onPhoneNumberChange,
  onOperatorNameChange,
}: CardKeepAliveFieldsProps) {
  const { t } = useI18n();
  const patch = (next: Partial<CardKeepAliveConfig>) => onChange({ ...value, ...next });
  const currency = value.currency;

  return (
    <div className="card-keepalive-fields">
      <div className="card-keepalive-basic-grid">
        <label>
          <span>{t("手机号")}</span>
          <Input
            value={phoneNumber}
            onChange={(event) => onPhoneNumberChange(event.target.value)}
            placeholder={t("例如 +8613800138000")}
            inputMode="tel"
          />
        </label>
        <label>
          <span>{t("运营商名称")}</span>
          <Input
            value={operatorName}
            onChange={(event) => onOperatorNameChange(event.target.value)}
            placeholder={t("例如 EE")}
            maxLength={128}
          />
        </label>
        <label>
          <span>{t("套餐信息")}</span>
          <Input
            value={value.planInfo}
            onChange={(event) => patch({ planInfo: event.target.value })}
            placeholder={t("套餐名称、流量或语音说明")}
            maxLength={500}
          />
        </label>
      </div>

      <div className="card-keepalive-section">
        <div className="card-keepalive-section-head">
          <div>
            <strong>{t("保号方式")}</strong>
            <p>{t("开启后维护套餐周期、余额和自动延期")}</p>
          </div>
          <Switch
            checked={value.enabled}
            onChange={(enabled) => patch({ enabled })}
            ariaLabel={t("保号方式")}
          />
        </div>

        {value.enabled ? (
          <div className="card-keepalive-panel">
            <div className="card-keepalive-grid">
              <label>
                <span>{t("当前余额")}</span>
                <div className="money-input">
                  <MoneyInput cents={value.balanceCents} onChange={(balanceCents) => patch({ balanceCents })} />
                  <Select
                    value={currency}
                    options={CURRENCIES.map((item) => ({ value: item, label: item }))}
                    onChange={(item) => patch({ currency: item as CardKeepAliveCurrency })}
                  />
                </div>
              </label>
              <label>
                <span>{t("套餐开始时间")}</span>
                <Input
                  type="date"
                  value={dateInputValue(value.planStartAt)}
                  onChange={(event) => {
                    const planStartAt = dateInputTimestamp(event.target.value);
                    patch({ planStartAt });
                  }}
                />
              </label>
              <label>
                <span>{t("到期日期")}</span>
                <Input
                  type="date"
                  value={dateInputValue(value.expiresAt || 0)}
                  onChange={(event) => patch({ expiresAt: dateInputTimestamp(event.target.value) })}
                />
              </label>
              <label>
                <span>{t("循环规则")}</span>
                <Select
                  value={value.cycleRule}
                  options={CYCLES.map((item) => ({ value: item.value, label: t(item.labelKey) }))}
                  onChange={(item) => patch({ cycleRule: item as CardKeepAliveCycleRule })}
                />
              </label>
              {value.cycleRule === "custom_days" ? (
                <label>
                  <span>{t("自定义天数")}</span>
                  <Input
                    type="number"
                    min="1"
                    max="3650"
                    value={String(value.cycleDays || 30)}
                    onChange={(event) => patch({ cycleDays: Math.max(1, Number(event.target.value) || 1) })}
                  />
                </label>
              ) : null}
              <label>
                <span>{t("每次扣费")} · {currency}</span>
                <MoneyInput cents={value.deductionCents} onChange={(deductionCents) => patch({ deductionCents })} />
              </label>
            </div>
            <div className="card-keepalive-switch-row">
              <div>
                <strong>{t("自动延期")}</strong>
                <p>{t("到期后自动扣费并延长周期；余额不足时不会延期")}</p>
              </div>
              <Switch
                checked={value.autoRenew}
                onChange={(autoRenew) => patch({ autoRenew })}
                ariaLabel={t("自动延期")}
              />
            </div>
          </div>
        ) : null}
      </div>

      {value.enabled ? (
        <div className="card-keepalive-section">
          <div className="card-keepalive-section-head">
            <div>
              <strong>{t("到期通知")}</strong>
              <p>{t("通过系统设置中已启用的通知渠道发送")}</p>
            </div>
            <Switch
              checked={value.notificationEnabled}
              onChange={(notificationEnabled) => patch({ notificationEnabled })}
              ariaLabel={t("到期通知")}
            />
          </div>
          {value.notificationEnabled ? (
            <div className="card-notification-panel">
              <label>
                <span>{t("到期前几天提醒")}</span>
                <Input
                  type="number"
                  min="1"
                  max="365"
                  value={String(value.notifyDaysBefore || 3)}
                  onChange={(event) => patch({ notifyDaysBefore: Math.max(1, Math.min(365, Number(event.target.value) || 1)) })}
                />
              </label>
              <label className="card-fixed-reminder">
                <input type="checkbox" checked disabled readOnly />
                <span>
                  <strong>{t("到期当天提醒")}</strong>
                  <small>{t("保底提醒，不可关闭")}</small>
                </span>
              </label>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
