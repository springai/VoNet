import { useCallback, useEffect, useState } from "react";
import { AddRegular, DeleteRegular, EditRegular, SaveRegular } from "@fluentui/react-icons";
import { api, apiMessage } from "../api";
import { Button, Input, Modal, PageHeader, Pagination, Spinner, Tag } from "../components/ui";
import { message } from "../components/ui/message";
import { useI18n } from "../lib/i18n";

const PAGE_SIZE = 10;

interface PoolItem {
  imei: string;
  status: "available" | "leased_random" | "leased_fixed";
  boundDeviceId: string;
  boundDeviceName: string;
  leaseMode: string;
}

export default function IMEIPoolPage() {
  const { t } = useI18n();
  const [items, setItems] = useState<PoolItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<{ current: string; next: string } | null>(null);
  const [adding, setAdding] = useState(false);
  const [newIMEI, setNewIMEI] = useState("");
  const [saving, setSaving] = useState(false);
  const [page, setPage] = useState(1);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await api<{ items: PoolItem[] }>("/pcsc-imei-pool");
      setItems(data.items || []);
    } catch (error) {
      message.error(apiMessage(error));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const totalPages = Math.max(1, Math.ceil(items.length / PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const pageItems = items.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);

  useEffect(() => {
    setPage((current) => Math.min(current, totalPages));
  }, [totalPages]);

  const save = async (method: "POST" | "PUT" | "DELETE", path: string, imei: string, current = "") => {
    setSaving(true);
    try {
      await api(path, { method, body: method === "DELETE" ? undefined : { imei } });
      message.success(t("IMEI 池已更新"));
      setAdding(false);
      setEditing(null);
      setNewIMEI("");
      await load();
    } catch (error) {
      message.error(apiMessage(error));
    } finally {
      setSaving(false);
    }
    void current;
  };

  return (
    <div>
      <PageHeader
        title={t("IMEI 池")}
        subtitle={t("管理读卡器可用的 IMEI")}
        actions={<Button variant="primary" icon={<AddRegular />} onClick={() => setAdding(true)}>{t("添加 IMEI")}</Button>}
      />
      <div className="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-white/10 dark:bg-white/5">
        {loading ? (
          <div className="flex h-52 items-center justify-center"><Spinner /></div>
        ) : (
          <table className="w-full text-left text-sm">
            <thead className="border-b border-gray-200 bg-gray-50 text-xs uppercase text-gray-500 dark:border-white/10 dark:bg-white/5">
              <tr>
                <th className="px-4 py-3">IMEI</th>
                <th className="px-4 py-3">{t("状态")}</th>
                <th className="px-4 py-3">{t("绑定设备")}</th>
                <th className="px-4 py-3 text-right">{t("操作")}</th>
              </tr>
            </thead>
            <tbody>
              {pageItems.map((item) => {
                const bound = item.status !== "available";
                return (
                  <tr key={item.imei} className="border-b border-gray-100 last:border-0 dark:border-white/5">
                    <td className="px-4 py-3 font-mono">{item.imei}</td>
                    <td className="px-4 py-3">
                      <Tag type={bound ? "warning" : "success"}>
                        {bound ? `${item.leaseMode === "fixed" ? t("固定") : t("随机")} · ${item.boundDeviceName || item.boundDeviceId}` : t("空闲")}
                      </Tag>
                    </td>
                    <td className="px-4 py-3">{item.boundDeviceName || item.boundDeviceId || "--"}</td>
                    <td className="px-4 py-3">
                      <div className="flex justify-end gap-1">
                        <Button size="small" variant="text" disabled={bound} title={t("修改")} icon={<EditRegular />} onClick={() => setEditing({ current: item.imei, next: item.imei })} />
                        <Button size="small" variant="text" disabled={bound} title={t("删除")} icon={<DeleteRegular />} onClick={() => void save("DELETE", `/pcsc-imei-pool/${item.imei}`, item.imei)} />
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        {!loading && items.length > 0 ? (
          <div className="border-t border-gray-200 px-5 py-3 dark:border-white/10">
            <Pagination
              page={currentPage}
              pageSize={PAGE_SIZE}
              total={items.length}
              onPageChange={setPage}
            />
          </div>
        ) : null}
      </div>

      <Modal open={adding} onClose={() => setAdding(false)} title={t("添加 IMEI")} footer={
        <>
          <Button onClick={() => setAdding(false)}>{t("取消")}</Button>
          <Button variant="primary" loading={saving} icon={<SaveRegular />} onClick={() => void save("POST", "/pcsc-imei-pool", newIMEI.trim())}>{t("保存")}</Button>
        </>
      }>
        <Input value={newIMEI} onChange={(e) => setNewIMEI(e.target.value)} maxLength={15} inputMode="numeric" placeholder="IMEI" />
      </Modal>

      <Modal open={!!editing} onClose={() => setEditing(null)} title={t("修改 IMEI")} footer={
        <>
          <Button onClick={() => setEditing(null)}>{t("取消")}</Button>
          <Button variant="primary" loading={saving} icon={<SaveRegular />} onClick={() => editing && void save("PUT", `/pcsc-imei-pool/${editing.current}`, editing.next.trim(), editing.current)}>{t("保存")}</Button>
        </>
      }>
        {editing && <Input value={editing.next} onChange={(e) => setEditing({ ...editing, next: e.target.value })} maxLength={15} inputMode="numeric" placeholder="IMEI" />}
      </Modal>
    </div>
  );
}
