import { useEffect, useState } from "react";
import { DeleteRegular, EditRegular, GlobeRegular, PauseRegular, PlayRegular, Wifi1Regular } from "@fluentui/react-icons";
import type { UpstreamProxy } from "../../types";
import { Button, Pagination, Tag } from "../ui";
import type { LoadError, UpstreamRow } from "./shared";
import { useI18n } from "../../lib/i18n";
import { useShowSensitive } from "../devices/shared";
import { displaySecret } from "../../lib/sensitive";

const PAGE_SIZE = 10;

export interface UpstreamSectionProps {
  rows: UpstreamRow[];
  loading: boolean;
  error: LoadError | null;
  onRetry: () => void;
  onEdit: (proxy: UpstreamProxy) => void;
  onDelete: (proxy: UpstreamProxy) => void;
  onProbe: (proxy: UpstreamProxy) => void;
  onToggle: (proxy: UpstreamProxy) => void;
  toggleBusyId?: string;
}

export function UpstreamSection({ rows, loading, error, onRetry, onEdit, onDelete, onProbe, onToggle, toggleBusyId }: UpstreamSectionProps) {
  const { t } = useI18n();
  const [showSensitive] = useShowSensitive();
  const [page, setPage] = useState(1);
  const totalPages = Math.max(1, Math.ceil(rows.length / PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const pageRows = rows.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);

  useEffect(() => {
    setPage((current) => Math.min(current, totalPages));
  }, [totalPages]);

  return (
    <section className="ui-card overflow-hidden">
      {error ? (
        <div className="flex items-center justify-between gap-3 border-b border-red-200 bg-red-50 p-3 text-sm text-red-600 dark:border-red-500/20 dark:bg-red-500/10 dark:text-red-300">
          <span className="min-w-0 truncate">{t("加载上游代理失败")}：{error.message}{error.status ? `（${error.status}）` : ""}</span>
          <button type="button" className="shrink-0 font-medium underline underline-offset-2" onClick={onRetry}>{t("重试")}</button>
        </div>
      ) : null}
      <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-white/10">
        <div>
          <h2 className="text-base font-semibold">{t("上游代理")}</h2>
          <p className="mt-1 text-xs text-gray-500">{t("只管理 SOCKS5 代理资源、启停和连通性。")}</p>
        </div>
        <GlobeRegular className="text-2xl text-sky-500" />
      </div>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[760px] text-left text-sm">
          <thead className="border-b border-gray-100 bg-gray-50/70 text-xs uppercase tracking-wide text-gray-500 dark:border-white/10 dark:bg-white/[0.025]">
            <tr>
              <th className="px-4 py-3">{t("名称")}</th>
              <th className="px-4 py-3">{t("地址")}</th>
              <th className="px-4 py-3">{t("状态")}</th>
              <th className="px-4 py-3">{t("最近连通性")}</th>
              <th className="px-4 py-3 text-right">{t("操作")}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-white/10">
            {pageRows.map((row) => (
              <tr key={row.id} className="hover:bg-sky-50/40 dark:hover:bg-sky-500/[0.04]">
                <td className="px-4 py-3 font-semibold">{row.name || row.id}</td>
                <td className="px-4 py-3 font-mono text-xs">{displaySecret(row.addr, showSensitive)}</td>
                <td className="px-4 py-3"><Tag type={row.enabled ? "success" : "info"}>{row.enabled ? t("已启用") : t("已禁用")}</Tag></td>
                <td className="px-4 py-3 text-xs text-gray-500">{t("点击“检测”获取结果")}</td>
                <td className="px-4 py-3">
                  <div className="flex justify-end gap-2">
                    <Button size="small" variant={row.enabled ? "warning" : "success"} plain icon={row.enabled ? <PauseRegular /> : <PlayRegular />} loading={toggleBusyId === row.id} onClick={() => onToggle(row)}>{row.enabled ? t("禁用") : t("启用")}</Button>
<Button size="small" icon={<Wifi1Regular />} loading={toggleBusyId === row.id} onClick={() => onProbe(row)}>{t("检测")}</Button>
                    <Button size="small" icon={<EditRegular />} onClick={() => onEdit(row)}>{t("编辑")}</Button>
                    <Button size="small" variant="danger" plain icon={<DeleteRegular />} onClick={() => onDelete(row)}>{t("删除")}</Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!loading && rows.length > 0 ? (
        <div className="border-t border-gray-100 px-5 py-3 dark:border-white/10">
          <Pagination
            page={currentPage}
            pageSize={PAGE_SIZE}
            total={rows.length}
            onPageChange={setPage}
          />
        </div>
      ) : null}
      {!loading && !rows.length ? <div className="px-6 py-16 text-center text-sm text-gray-400">{t("暂无上游代理")}</div> : null}
      {loading ? <div className="px-6 py-16 text-center text-sm text-gray-400">{t("加载中...")}</div> : null}
    </section>
  );
}
