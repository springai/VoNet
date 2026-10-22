import { useCallback, useRef, useState } from "react";
import {
  ArrowSyncRegular,
  CheckmarkCircleRegular,
  DismissCircleRegular,
  WarningRegular,
  PulseRegular,
} from "@fluentui/react-icons";
import { api } from "../api";
import { Button, PageHeader } from "../components/ui";
import { useI18n } from "../lib/i18n";

interface DiagCheck {
  name: string;
  status: "passed" | "warning" | "failed";
  message: string;
  detail?: string;
}

const STATUS_ICON: Record<string, React.ReactNode> = {
  passed: <CheckmarkCircleRegular className="text-lg text-emerald-500" />,
  warning: <WarningRegular className="text-lg text-amber-500" />,
  failed: <DismissCircleRegular className="text-lg text-red-500" />,
};

const STATUS_BG: Record<string, string> = {
  passed: "border-emerald-200 bg-emerald-50 dark:border-emerald-500/20 dark:bg-emerald-500/10",
  warning: "border-amber-200 bg-amber-50 dark:border-amber-500/20 dark:bg-amber-500/10",
  failed: "border-red-200 bg-red-50 dark:border-red-500/20 dark:bg-red-500/10",
};

function CheckRow({ check, visible }: { check: DiagCheck; visible: boolean }) {
  return (
    <div
      className={`flex items-start gap-3 rounded-lg border p-3 text-sm transition-all duration-500 ${
        STATUS_BG[check.status] || "border-gray-200 bg-gray-50"
      } ${visible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-3"}`}
    >
      <div className="mt-0.5 shrink-0">{STATUS_ICON[check.status] || null}</div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="font-mono text-xs font-bold text-gray-600 dark:text-gray-300">{check.name}</span>
        </div>
        <div className="mt-0.5 text-gray-700 dark:text-gray-200">{check.message}</div>
        {check.detail ? (
          <pre className="mt-1 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded bg-black/5 p-2 text-xs text-gray-500 dark:bg-white/5 dark:text-gray-400">
            {check.detail}
          </pre>
        ) : null}
      </div>
    </div>
  );
}

function Spinner() {
  return (
    <div className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-indigo-200 border-t-indigo-600" />
  );
}

export default function DiagnosticsPage() {
  const { t } = useI18n();
  const [checks, setChecks] = useState<DiagCheck[]>([]);
  const [visibleCount, setVisibleCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [ran, setRan] = useState(false);
  const timerRef = useRef<number[]>([]);

  const stopTimers = () => {
    for (const id of timerRef.current) window.clearTimeout(id);
    timerRef.current = [];
  };

  const runDiagnostics = useCallback(async () => {
    stopTimers();
    setLoading(true);
    setChecks([]);
    setVisibleCount(0);
    setRan(false);
    try {
      const data = await api<DiagCheck[]>("/system/diagnostics");
      const items = Array.isArray(data) ? data : [];
      setChecks(items);
      setRan(true);
      // Sequential reveal animation
      items.forEach((_, i) => {
        const id = window.setTimeout(() => {
          setVisibleCount((prev) => Math.max(prev, i + 1));
        }, i * 120);
        timerRef.current.push(id);
      });
    } catch (e) {
      console.error("Diagnostics failed", e);
    } finally {
      setLoading(false);
    }
  }, []);

  const passed = checks.filter((c) => c.status === "passed").length;
  const warnings = checks.filter((c) => c.status === "warning").length;
  const failed = checks.filter((c) => c.status === "failed").length;

  return (
    <div className="mx-auto max-w-5xl">
      <PageHeader title={t("系统诊断")} subtitle={t("检查 VoWiFi/XFRM 运行环境")} />

      <div className="ui-card group relative overflow-hidden p-8">
        <div className="absolute right-0 top-0 -mr-10 -mt-10 h-40 w-40 rounded-bl-full bg-indigo-500/5 transition-transform group-hover:scale-110" />
        <div className="relative z-10 mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3">
            <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-indigo-50 text-indigo-600 dark:bg-indigo-500/10 dark:text-indigo-400">
              <PulseRegular className="text-[24px]" />
            </div>
            <div>
              <h3 className="text-lg font-bold text-gray-800 dark:text-gray-100">{t("VoWiFi/XFRM 环境")}</h3>
              <p className="text-xs text-gray-500">{t("检查内核 XFRM/IPsec 支持、加密算法、内核模块与相关服务")}</p>
            </div>
          </div>
          <Button variant="primary" loading={loading} onClick={runDiagnostics} icon={<ArrowSyncRegular />}>
            {t("开始诊断")}
          </Button>
        </div>

        {ran && (
          <div className="relative z-10 mb-4 flex items-center gap-4 rounded-lg bg-gray-50 p-3 text-sm dark:bg-white/5">
            <span className="text-emerald-600 dark:text-emerald-400">{t("通过")} {passed}</span>
            <span className="text-amber-600 dark:text-amber-400">{t("警告")} {warnings}</span>
            <span className="text-red-600 dark:text-red-400">{t("失败")} {failed}</span>
            <span className="text-gray-400">|</span>
            <span className="text-gray-500">{t("共")} {checks.length} {t("项")}</span>
          </div>
        )}

        <div className="relative z-10 space-y-2">
          {!ran && !loading && (
            <div className="p-10 text-center text-sm text-gray-400">
              {t("点击「开始诊断」检查系统 VoWiFi/XFRM 运行环境")}
            </div>
          )}
          {loading && (
            <div className="flex items-center justify-center gap-3 p-10 text-sm text-gray-400">
              <Spinner />
              <span>{t("正在诊断...")}</span>
            </div>
          )}
          {checks.map((check, idx) => (
            <CheckRow key={idx} check={check} visible={idx < visibleCount} />
          ))}
        </div>
      </div>
    </div>
  );
}