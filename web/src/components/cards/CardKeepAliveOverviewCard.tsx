import { useCallback, useEffect, useMemo, useState } from "react";
import type { CardKeepAliveCharge, CardKeepAliveConfig, CardKeepAliveCycleRule, SimCard } from "../../types";
import { apiMessage } from "../../api";
import { Button, confirmDialog, message } from "../ui";
import { useI18n } from "../../lib/i18n";
import { useShowSensitive } from "../devices/shared";
import { displaySecret } from "../../lib/sensitive";
import { SensitiveInfoButton } from "../ui/SensitiveInfoButton";
import { centsToMoney, dateInputValue } from "./CardKeepAliveFields";
import { moneyDisplay } from "./cardKeepAliveMoney";
import { listCardKeepAliveCharges, renewCardKeepAlive } from "./cardApi";

const CYCLE_LABELS: Record<CardKeepAliveCycleRule, string> = {
  custom_days: "自定义天数",
  monthly: "月付",
  quarterly: "季付",
  half_yearly: "半年付",
  yearly: "年付",
};

function keepAliveDays(keepAlive?: CardKeepAliveConfig): number | null {
  if (!keepAlive?.enabled) return null;
  if (typeof keepAlive.daysRemaining === "number") return Math.max(0, keepAlive.daysRemaining);
  if (!keepAlive.expiresAt) return null;
  return Math.max(0, Math.ceil((keepAlive.expiresAt * 1000 - Date.now()) / 86400000));
}

function chargeLine(charge: CardKeepAliveCharge, t: (value: string) => string): string {
  const action = charge.source === "manual" ? t("已手动续期") : t("已自动续期");
  return `${dateInputValue(charge.createdAt)} ${action} -${moneyDisplay(charge.amountCents)}${charge.currency}，${t("余额")} ${moneyDisplay(charge.balanceAfterCents)}${charge.currency}`;
}

export function CardKeepAliveOverviewCard({ card }: { card: SimCard }) {
  const { t } = useI18n();
  const [showSensitive] = useShowSensitive();
  const [charges, setCharges] = useState<CardKeepAliveCharge[]>([]);
  const [renewing, setRenewing] = useState(false);
  const [override, setOverride] = useState<CardKeepAliveConfig | null>(null);
  const baseKeepAlive = override || card.keepalive;
  const days = keepAliveDays(baseKeepAlive);
  const expired = !!baseKeepAlive?.enabled && (
    baseKeepAlive.status === "expired" ||
    (!!baseKeepAlive.expiresAt && baseKeepAlive.expiresAt * 1000 < Date.now())
  );
  const cycle = baseKeepAlive?.cycleRule === "custom_days"
    ? `${t("自定义天数")} · ${baseKeepAlive.cycleDays || 0}`
    : t(CYCLE_LABELS[baseKeepAlive?.cycleRule || "monthly"]);

  const refreshCharges = useCallback(async () => {
    try {
      setCharges(await listCardKeepAliveCharges(card.iccid));
    } catch {
      setCharges([]);
    }
  }, [card.iccid]);

  useEffect(() => {
    setOverride(null);
    void refreshCharges();
  }, [card.iccid, card.keepalive?.balanceCents, card.keepalive?.expiresAt, refreshCharges]);

  const summary = useMemo(() => ({
    operator: card.carrier || "--",
    phone: displaySecret(card.msisdn, showSensitive),
    start: dateInputValue(baseKeepAlive?.planStartAt || 0) || "--",
    balance: `${centsToMoney(baseKeepAlive?.balanceCents || 0)} ${baseKeepAlive?.currency || "CNY"}`,
    cycle: baseKeepAlive?.enabled ? cycle : t("未启用"),
    deduction: `${centsToMoney(baseKeepAlive?.deductionCents || 0)} ${baseKeepAlive?.currency || "CNY"}`,
  }), [baseKeepAlive, card.carrier, card.msisdn, cycle, showSensitive, t]);

  async function renew() {
    if (!baseKeepAlive?.enabled || renewing) return;
    const amount = `${centsToMoney(baseKeepAlive.deductionCents)}${baseKeepAlive.currency}`;
    const confirmed = await confirmDialog(
      t("将从今天开始按当前保号规则重新计算周期，并从余额中扣费 {amount}。").replace("{amount}", amount),
      t("确认续期"),
      { type: "warning", confirmText: t("确认续期"), cancelText: t("取消") },
    );
    if (!confirmed) return;
    setRenewing(true);
    try {
      const renewal = await renewCardKeepAlive(card.iccid);
      setOverride({
        ...baseKeepAlive,
        balanceCents: renewal.balanceAfterCents,
        currentCycleStartAt: renewal.cycleStartAt,
        expiresAt: renewal.newExpiresAt,
        lastChargeAt: renewal.createdAt,
        lastRenewedAt: renewal.createdAt,
        status: "active",
        daysRemaining: undefined,
      });
      await refreshCharges();
      message.success(t("保号已续期"));
    } catch (error) {
      message.error(apiMessage(error) || t("保号续期失败"));
    } finally {
      setRenewing(false);
    }
  }

  return (
    <section className="card-keepalive-overview-card">
      <header className="card-keepalive-overview-head">
        <div>
          <strong>{t("保号信息")}</strong>
          <p>{t("套餐、余额与到期周期")}</p>
        </div>
        <div className="card-keepalive-overview-actions">
          <SensitiveInfoButton />
          <Button
            variant="primary"
            plain
            loading={renewing}
            disabled={!baseKeepAlive?.enabled}
            onClick={() => void renew()}
          >
            {t("续期")}
          </Button>
        </div>
      </header>

      <div className="card-keepalive-overview-body">
        <div className="card-keepalive-overview-days">
          <strong>{days == null ? "--" : days}</strong>
          <span>{days == null ? t("未启用保号") : expired ? t("已到期") : days === 0 ? t("今天到期") : t("天后到期")}</span>
        </div>
        <div className="card-keepalive-overview-fields">
          <div><small>{t("运营商")}</small><strong>{summary.operator}</strong></div>
          <div><small>{t("手机号")}</small><strong>{summary.phone}</strong></div>
          <div><small>{t("套餐开始时间")}</small><strong>{summary.start}</strong></div>
          <div><small>{t("当前余额")}</small><strong>{summary.balance}</strong></div>
          <div><small>{t("循环保号规则")}</small><strong>{summary.cycle}</strong></div>
          <div><small>{t("每次扣费")}</small><strong>{summary.deduction}</strong></div>
        </div>
      </div>

      {charges.length > 0 ? (
        <div className="card-keepalive-records">
          <strong>{t("保号记录")}</strong>
          <div>
            {charges.slice(0, 8).map((charge) => (
              <p key={charge.id}>{chargeLine(charge, t)}</p>
            ))}
          </div>
        </div>
      ) : null}
    </section>
  );
}
