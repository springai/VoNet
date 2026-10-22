import { LockClosedFilled, LockClosedRegular } from "@fluentui/react-icons";
import { useI18n } from "../../lib/i18n";
import { Button } from "../ui";

export function CellLockButton({ locked, statusKnown, onClick, compact = false }: { locked: boolean; statusKnown: boolean; onClick: () => void; compact?: boolean }) {
  const { t } = useI18n();
  const label = statusKnown ? t(locked ? "已配置锁定" : "未配置锁定") : t("锁定状态未知");
  const title = `${t("小区锁定")} · ${label}`;
  const props = { onClick, title, "aria-label": title, ...(statusKnown ? { "aria-pressed": locked } : {}) };
  const Icon = locked && statusKnown ? LockClosedFilled : LockClosedRegular;
  const icon = <Icon aria-hidden="true" className={`h-5 w-5 ${locked && statusKnown ? "text-sky-600 dark:text-sky-300" : "text-gray-500 dark:text-gray-400"}`} />;
  if (compact) return <button type="button" {...props} className="shrink-0 rounded p-1 transition-colors hover:bg-black/5 dark:hover:bg-white/10">{icon}</button>;
  return (
    <Button block className="mb-3" {...props} icon={icon}>
      <span>{t("小区锁定")}</span>
      <span className="text-xs opacity-70">{label}</span>
    </Button>
  );
}
