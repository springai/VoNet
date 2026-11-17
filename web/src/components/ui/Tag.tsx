import type { ReactNode } from "react";
import { cx } from "../../lib/utils";

export type TagType = "success" | "danger" | "warning" | "info" | "primary";

const TONE: Record<TagType, string> = {
  success: "bg-emerald-50 text-emerald-700 border-emerald-100 dark:bg-emerald-500/10 dark:text-emerald-300 dark:border-emerald-500/20",
  danger: "bg-red-50 text-red-700 border-red-100 dark:bg-red-500/10 dark:text-red-300 dark:border-red-500/20",
  warning: "bg-amber-50 text-amber-700 border-amber-100 dark:bg-amber-500/10 dark:text-amber-300 dark:border-amber-500/20",
  info: "bg-slate-100 text-slate-600 border-slate-200/70 dark:bg-white/5 dark:text-slate-300 dark:border-white/10",
  primary: "bg-blue-50 text-blue-700 border-blue-100 dark:bg-blue-500/10 dark:text-blue-300 dark:border-blue-500/20",
};

// el-tag equivalent.
export function Tag({ type = "info", children, className }: { type?: TagType; children: ReactNode; className?: string }) {
  return (
    <span
      className={cx(
        "ui-tag inline-flex items-center gap-1 whitespace-nowrap rounded-full border px-2.5 py-1 text-xs font-semibold",
        TONE[type],
        className,
      )}
    >
      {children}
    </span>
  );
}
