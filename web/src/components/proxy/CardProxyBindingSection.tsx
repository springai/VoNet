import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, Pencil, Plus, Trash2, X } from "lucide-react";
import { api, apiMessage } from "../../api";
import type { ProxyBindingGroup, ProxyBindingReconnectState } from "../../proxy_types";
import type { SimCard, UpstreamProxy } from "../../types";
import { usePolling } from "../../lib/usePolling";
import { Button, EmptyState, Input, Modal, Select, StatusDot, Tag, confirmDialog, message } from "../ui";
import { useI18n } from "../../lib/i18n";
import { useShowSensitive } from "../devices/shared";
import { displayICCID } from "../../lib/sensitive";

interface Props {
  cards: SimCard[];
  proxies: UpstreamProxy[];
  onChanged: () => void;
}

function cardStatusTone(card: SimCard) {
  if (card.status === "active") return "success";
  if (card.status === "conflict") return "danger";
  if (card.status === "unknown") return "warning";
  return "neutral";
}

export function CardProxyBindingSection({ cards, proxies, onChanged }: Props) {
  const { t } = useI18n();
  const [showSensitive] = useShowSensitive();
  const [groups, setGroups] = useState<ProxyBindingGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [editingCard, setEditingCard] = useState<SimCard | null>(null);
  const [editingGroupId, setEditingGroupId] = useState("");
  const [routes, setRoutes] = useState<string[]>([]);
  const [selectedProxy, setSelectedProxy] = useState("");
  const [saving, setSaving] = useState(false);
  const [query, setQuery] = useState("");

  const proxyById = useMemo(() => new Map(proxies.map((proxy) => [proxy.id, proxy])), [proxies]);
  const groupByICCID = useMemo(
    () => new Map(groups.map((group) => [group.iccid.replace(/\s+/g, ""), group])),
    [groups],
  );

  const loadGroups = useCallback(async (initial = false) => {
    if (initial) setLoading(true);
    try {
      setGroups((await api<ProxyBindingGroup[]>("/proxy-binding-groups")) || []);
      setError("");
    } catch (e) {
      setError(apiMessage(e) || t("读取卡片代理绑定失败"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void loadGroups(true);
  }, [loadGroups]);

  usePolling(() => {
    if (!editingCard) void loadGroups();
  }, 10000, false);

  const eligibleCards = useMemo(
    () => cards.filter((card) => card.registered && card.conflict !== true && card.status !== "conflict"),
    [cards],
  );
  const visibleCards = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return eligibleCards;
    return eligibleCards.filter((card) =>
      `${card.iccid} ${card.displayName || ""} ${card.carrier || ""}`.toLowerCase().includes(q),
    );
  }, [eligibleCards, query]);

  const openEditor = (card: SimCard) => {
    const group = groupByICCID.get(card.iccid.replace(/\s+/g, ""));
    setEditingCard(card);
    setEditingGroupId(group?.id || "");
    setRoutes([...(group?.routes || [])].sort((a, b) => a.priority - b.priority).map((route) => route.upstreamProxyId));
    setSelectedProxy("");
  };

  const closeEditor = () => {
    setEditingCard(null);
    setEditingGroupId("");
    setRoutes([]);
    setSelectedProxy("");
  };

  const moveRoute = (index: number, delta: number) => {
    setRoutes((current) => {
      const next = [...current];
      const target = index + delta;
      if (target < 0 || target >= next.length) return current;
      [next[index], next[target]] = [next[target], next[index]];
      return next;
    });
  };

  const save = async () => {
    if (!editingCard) return;
    const currentCard = cards.find((card) => card.iccid === editingCard.iccid) || editingCard;
    if (currentCard.conflict === true || currentCard.status === "conflict") {
      message.error(t("ICCID 存在冲突，不能绑定代理"));
      return;
    }
    setSaving(true);
    try {
      let result: ProxyBindingReconnectState;
      if (editingGroupId) {
        result = await api<ProxyBindingReconnectState>(
          `/proxy-binding-groups/${encodeURIComponent(editingGroupId)}/routes`,
          { method: "PUT", body: { proxyIds: routes } },
        );
      } else {
        result = await api<ProxyBindingReconnectState>("/proxy-binding-groups", {
          method: "POST",
          body: {
            iccid: currentCard.iccid,
            profileName: currentCard.displayName || currentCard.iccid,
            proxyIds: routes,
          },
        });
      }
      if (result.reconnectError) {
        message.warning(t("代理绑定已保存，但 VoWiFi 立即重连失败；将在下次连接时应用"));
      } else {
        message.success(t("卡片代理绑定已保存"));
      }
      await loadGroups();
      onChanged();
      closeEditor();
    } catch (e) {
      message.error(apiMessage(e) || t("保存卡片代理绑定失败"));
    } finally {
      setSaving(false);
    }
  };

  const removeGroup = async (group: ProxyBindingGroup) => {
    const ok = await confirmDialog(t("确定删除这张卡的手动代理绑定吗？"), t("删除绑定"), {
      type: "warning",
      confirmText: t("删除"),
      cancelText: t("取消"),
    });
    if (!ok) return;
    try {
      await api(`/proxy-binding-groups/${encodeURIComponent(group.id)}`, { method: "DELETE" });
      message.success(t("卡片代理绑定已删除"));
      await loadGroups();
      onChanged();
    } catch (e) {
      message.error(apiMessage(e) || t("删除卡片代理绑定失败"));
    }
  };

  return (
    <section className="ui-card mt-6 overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-white/10">
        <div>
          <h2 className="text-base font-semibold">{t("卡片代理绑定")}</h2>
          <p className="mt-1 text-xs text-gray-500">
            {t("候选卡片全部来自 SIM 管理；离线卡片也可以预先绑定")}
          </p>
        </div>
        <div className="w-full sm:w-64">
          <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("搜索卡片")} />
        </div>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full min-w-[760px] text-left text-sm">
          <thead className="bg-gray-50/80 text-xs uppercase text-gray-500 dark:bg-white/[0.025]">
            <tr>
              <th className="px-5 py-3">{t("卡片")}</th>
              <th className="px-4 py-3">ICCID</th>
              <th className="px-4 py-3">{t("代理顺序")}</th>
              <th className="px-5 py-3 text-right">{t("操作")}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-white/10">
            {visibleCards.map((card) => {
              const group = groupByICCID.get(card.iccid.replace(/\s+/g, ""));
              return (
                <tr key={card.iccid} className="hover:bg-gray-50/70 dark:hover:bg-white/[0.025]">
                  <td className="px-5 py-4">
                    <div className="flex items-center gap-2">
                      <StatusDot tone={cardStatusTone(card)} />
                      <div>
                        <div className="font-semibold text-gray-900 dark:text-white">{card.displayName || displayICCID(card.iccid, showSensitive)}</div>
                        <div className="mt-1 text-xs text-gray-400">{card.carrier || t("运营商未知")}</div>
                      </div>
                    </div>
                  </td>
                  <td className="px-4 py-4 font-mono text-xs text-gray-500">{displayICCID(card.iccid, showSensitive)}</td>
                  <td className="px-4 py-4">
                    {group?.routes?.length ? (
                      <div className="flex flex-wrap gap-1">
                        {group.routes.map((route) => (
                          <Tag key={route.id}>{proxyById.get(route.upstreamProxyId)?.name || route.upstreamProxyId}</Tag>
                        ))}
                      </div>
                    ) : (
                      <Tag>{t("MCC 规则")}</Tag>
                    )}
                  </td>
                  <td className="px-5 py-4">
                    <div className="flex justify-end gap-2">
                      <Button size="small" plain icon={<Pencil className="h-3.5 w-3.5" />} onClick={() => openEditor(card)}>
                        {group ? t("编辑") : t("绑定")}
                      </Button>
                      {group ? (
                        <Button
                          variant="danger"
                          size="small"
                          plain
                          icon={<Trash2 className="h-3.5 w-3.5" />}
                          onClick={() => void removeGroup(group)}
                        >
                          {t("删除")}
                        </Button>
                      ) : null}
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {!loading && !error && visibleCards.length === 0 ? (
        <EmptyState title={t("没有可绑定的卡片")} subtitle={t("只有已在 SIM 管理中登记且 ICCID 唯一的卡片才能绑定代理")} />
      ) : null}
      {error ? <div className="border-t border-red-100 bg-red-50 px-5 py-4 text-sm text-red-600 dark:border-red-500/20 dark:bg-red-500/10">{error}</div> : null}

      <Modal
        open={!!editingCard}
        onClose={closeEditor}
        title={t("卡片代理顺序")}
        width="max-w-2xl"
        footer={
          <>
            <Button onClick={closeEditor}>{t("取消")}</Button>
            <Button variant="primary" loading={saving} onClick={() => void save()}>{t("保存")}</Button>
          </>
        }
      >
        {editingCard ? (
          <div className="space-y-4">
            <div className="ui-panel-muted p-4">
              <div className="font-semibold text-gray-900 dark:text-white">{editingCard.displayName || displayICCID(editingCard.iccid, showSensitive)}</div>
              <div className="mt-1 font-mono text-xs text-gray-500">{displayICCID(editingCard.iccid, showSensitive)}</div>
              <div className="mt-2 text-xs text-gray-500">
                {editingCard.status === "active" ? t("当前启用，保存后立即重连 VoWiFi") : t("卡片当前离线，上线后自动应用此绑定")}
              </div>
            </div>

            <div className="space-y-2">
              {routes.map((proxyId, index) => (
                <div key={`${proxyId}-${index}`} className="flex items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 dark:border-white/10">
                  <span className="w-6 text-center text-xs font-bold text-gray-400">{index + 1}</span>
                  <span className="min-w-0 flex-1 truncate text-sm text-gray-800 dark:text-gray-100">
                    {proxyById.get(proxyId)?.name || proxyId}
                  </span>
                  <Button size="small" plain icon={<ArrowUp className="h-3.5 w-3.5" />} disabled={index === 0} onClick={() => moveRoute(index, -1)} />
                  <Button size="small" plain icon={<ArrowDown className="h-3.5 w-3.5" />} disabled={index === routes.length - 1} onClick={() => moveRoute(index, 1)} />
                  <Button size="small" plain icon={<X className="h-3.5 w-3.5" />} onClick={() => setRoutes((current) => current.filter((_, i) => i !== index))} />
                </div>
              ))}
            </div>

            <div className="flex items-center gap-2">
              <Select
                value={selectedProxy}
                onChange={setSelectedProxy}
                placeholder={t("选择上游代理")}
                options={proxies
                  .filter((proxy) => proxy.enabled && !routes.includes(proxy.id))
                  .map((proxy) => ({ value: proxy.id, label: proxy.name || proxy.id }))}
              />
              <Button
                icon={<Plus className="h-4 w-4" />}
                disabled={!selectedProxy}
                onClick={() => {
                  if (!selectedProxy) return;
                  setRoutes((current) => [...current, selectedProxy]);
                  setSelectedProxy("");
                }}
              >
                {t("添加")}
              </Button>
            </div>
          </div>
        ) : null}
      </Modal>
    </section>
  );
}
