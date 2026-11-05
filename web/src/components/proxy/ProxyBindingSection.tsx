import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent } from "react";
import {
  Plus,
  ArrowRight,
  ArrowDown,
  ArrowUp,
  ChevronRight,
  GripVertical,
  Link2,
  Monitor,
  Pencil,
  RefreshCw,
  Search,
  Trash2,
  X,
} from "lucide-react";
import { api, apiMessage } from "../../api";
import type { DeviceListItem, EsimOverview, UpstreamProxy } from "../../types";
import type { ProxyBindingGroup, ProxyBindingReconnectState } from "../../proxy_types";
import { usePolling } from "../../lib/usePolling";
import {
  Button,
  Drawer,
  EmptyState,
  Input,
  Modal,
  Select,
  StatusDot,
  Tag,
  Tooltip,
  confirmDialog,
  message,
} from "../ui";
import { tf, useI18n } from "../../lib/i18n";
import {
  bindingCardKey,
  buildInventoryBindingCards,
  buildLiveBindingCards,
  buildStoredBindingCards,
  createRequestGuard,
  failedInventorySnapshot,
  inventoryReadPlan,
  mergeBindingCards,
  proxyBindingReconnectOutcome,
  reconcileInventoryBindingCards,
  selectionAfterRefresh,
  type BindingCard,
  type InventorySnapshot,
} from "./bindingModel";
import "./binding.css";

interface Props {
  proxies: UpstreamProxy[];
  devices: DeviceListItem[];
  onChanged: () => void;
}

interface DraftRoute {
  id: string;
  upstreamProxyId: string;
}

type RouteFilter = "all" | "bound" | "unbound";

export function ProxyBindingSection({ proxies, devices, onChanged }: Props) {
  const { t } = useI18n();
  const [groups, setGroups] = useState<ProxyBindingGroup[]>([]);
  const [groupsLoading, setGroupsLoading] = useState(true);
  const [groupsError, setGroupsError] = useState("");
  const [inventoryVersion, setInventoryVersion] = useState(0);
  const [inventoryLoading, setInventoryLoading] = useState(false);
  const [inventoryProgress, setInventoryProgress] = useState({ done: 0, total: 0 });
  const [inventoryError, setInventoryError] = useState("");
  const [pickerOpen, setPickerOpen] = useState(false);
  const [pickerQuery, setPickerQuery] = useState("");
  const [pickerSelectedKey, setPickerSelectedKey] = useState("");
  const [editorTarget, setEditorTarget] = useState<BindingCard | null>(null);
  const [editorGroupId, setEditorGroupId] = useState("");
  const [editorRoutes, setEditorRoutes] = useState<DraftRoute[]>([]);
  const [routeBaseline, setRouteBaseline] = useState<string[]>([]);
  const [selectedProxyId, setSelectedProxyId] = useState("");
  const [saving, setSaving] = useState(false);
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [dragOverIndex, setDragOverIndex] = useState<number | null>(null);
  const [query, setQuery] = useState("");
  const [routeFilter, setRouteFilter] = useState<RouteFilter>("all");
  const [deviceFilter, setDeviceFilter] = useState("");
  const [proxyFilter, setProxyFilter] = useState("");

  const inventoryCache = useRef(new Map<string, InventorySnapshot>());
  const inventoryGuard = useRef(createRequestGuard());
  const inventoryLoadingRef = useRef(false);
  const groupsGuard = useRef(createRequestGuard());

  const labels = useMemo(() => ({
    currentCard: t("当前 SIM 卡"),
    currentInUse: t("当前使用中"),
  }), [t]);

  const proxyById = useMemo(() => new Map(proxies.map((proxy) => [proxy.id, proxy])), [proxies]);
  const groupByKey = useMemo(
    () => new Map(groups.map((group) => [bindingCardKey(group.deviceId, group.iccid), group])),
    [groups],
  );

  const allCards = useMemo(() => {
    const cachedCards = reconcileInventoryBindingCards(
      Array.from(inventoryCache.current.values()).flatMap((snapshot) => snapshot.cards),
      devices,
    );
    return mergeBindingCards(
      buildLiveBindingCards(devices, labels),
      buildStoredBindingCards(groups, devices),
      cachedCards,
    );
  }, [devices, groups, inventoryVersion, labels]);

  const boundCount = allCards.filter((card) => groupByKey.has(card.key)).length;
  const routeProxyIds = editorRoutes.map((route) => route.upstreamProxyId);
  const routeDirty = routeProxyIds.join("|") !== routeBaseline.join("|");
  const availableProxies = proxies.filter(
    (proxy) => proxy.enabled && !routeProxyIds.includes(proxy.id),
  );

  const loadGroups = useCallback(async (initial = false) => {
    const token = groupsGuard.current.begin();
    if (initial) setGroupsLoading(true);
    try {
      const data = await api<ProxyBindingGroup[]>("/proxy-binding-groups");
      if (!groupsGuard.current.isCurrent(token)) return;
      setGroups(data || []);
      setGroupsError("");
    } catch (error) {
      if (!groupsGuard.current.isCurrent(token)) return;
      setGroupsError(apiMessage(error) || t("读取绑定关系失败"));
    } finally {
      if (groupsGuard.current.isCurrent(token) && initial) setGroupsLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void loadGroups(true);
  }, [loadGroups]);

  usePolling(() => {
    if (!pickerOpen && !editorTarget) void loadGroups(false);
  }, 10000, false);

  useEffect(() => {
    setPickerSelectedKey((current) => current ? selectionAfterRefresh(allCards, current) : "");
  }, [allCards]);

  const refreshInventory = useCallback(async (force = false) => {
    if (inventoryLoadingRef.current) return;
    const token = inventoryGuard.current.begin();
    const staleDeviceIds = Array.from(inventoryCache.current.entries())
      .filter(([, snapshot]) => snapshot.stale)
      .map(([deviceID]) => deviceID);
    const targets = inventoryReadPlan(inventoryCache.current.keys(), devices, force, staleDeviceIds);
    if (targets.length === 0) {
      setInventoryError("");
      return;
    }

    setInventoryLoading(true);
    inventoryLoadingRef.current = true;
    setInventoryProgress({ done: 0, total: targets.length });
    setInventoryError("");
    let done = 0;
    const failures: string[] = [];

    await Promise.allSettled(targets.map(async (device) => {
      try {
        const overview = await api<EsimOverview>(
          `/devices/${encodeURIComponent(device.id)}/esim?cached=1`,
        );
        if (!inventoryGuard.current.isCurrent(token)) return;
        inventoryCache.current.set(device.id, {
          cards: buildInventoryBindingCards(overview, device, labels),
          fetchedAt: overview.cachedAt ? overview.cachedAt * 1000 : Date.now(),
          stale: !!overview.stale || !device.controlOnline,
        });
      } catch (error) {
        if (!inventoryGuard.current.isCurrent(token)) return;
        const previous = inventoryCache.current.get(device.id);
        inventoryCache.current.set(
          device.id,
          failedInventorySnapshot(previous, device, apiMessage(error)),
        );
        failures.push(device.name || device.id);
      } finally {
        if (inventoryGuard.current.isCurrent(token)) {
          done += 1;
          setInventoryProgress({ done, total: targets.length });
        }
      }
    }));

    if (!inventoryGuard.current.isCurrent(token)) return;
    inventoryLoadingRef.current = false;
    setInventoryVersion((version) => version + 1);
    setInventoryLoading(false);
    setInventoryProgress({ done: targets.length, total: targets.length });
    if (failures.length) {
      setInventoryError(tf("部分设备 Profile 读取失败：{devices}", { devices: failures.join("、") }));
    }
  }, [devices, labels, t]);

  const openPicker = useCallback(() => {
    setPickerQuery("");
    setPickerSelectedKey("");
    setInventoryError("");
    setPickerOpen(true);
    void refreshInventory(false);
  }, [refreshInventory]);

  const openEditor = useCallback((card: BindingCard) => {
    const group = groupByKey.get(card.key);
    const nextRoutes = [...(group?.routes || [])]
      .sort((a, b) => a.priority - b.priority)
      .map((route) => ({ id: route.id, upstreamProxyId: route.upstreamProxyId }));
    setEditorTarget(card);
    setEditorGroupId(group?.id || "");
    setEditorRoutes(nextRoutes);
    setRouteBaseline(nextRoutes.map((route) => route.upstreamProxyId));
    setSelectedProxyId("");
    setDragIndex(null);
    setDragOverIndex(null);
    setPickerOpen(false);
  }, [groupByKey]);

  const requestCloseEditor = useCallback(async () => {
    if (routeDirty) {
      const ok = await confirmDialog(
        t("代理顺序尚未保存，确定放弃更改吗？"),
        t("放弃未保存更改"),
        { type: "warning", confirmText: t("放弃更改"), cancelText: t("继续编辑") },
      );
      if (!ok) return;
    }
    setEditorTarget(null);
    setEditorGroupId("");
    setEditorRoutes([]);
    setRouteBaseline([]);
    setSelectedProxyId("");
  }, [routeDirty, t]);

  const moveRoute = (index: number, delta: number) => {
    setEditorRoutes((current) => {
      const target = index + delta;
      if (target < 0 || target >= current.length) return current;
      const next = [...current];
      [next[index], next[target]] = [next[target], next[index]];
      return next;
    });
  };

  const dropRoute = (targetIndex: number) => {
    if (dragIndex === null || dragIndex === targetIndex) return;
    setEditorRoutes((current) => {
      const next = [...current];
      const [moved] = next.splice(dragIndex, 1);
      next.splice(targetIndex, 0, moved);
      return next;
    });
    setDragIndex(null);
    setDragOverIndex(null);
  };

  const onRouteDragOver = (event: DragEvent<HTMLDivElement>, index: number) => {
    event.preventDefault();
    setDragOverIndex(index);
  };

  const save = async () => {
    if (!editorTarget) return;
    setSaving(true);
    try {
      const proxyIds = editorRoutes.map((route) => route.upstreamProxyId);
      let result: ProxyBindingReconnectState;
      if (editorGroupId) {
        result = await api<ProxyBindingReconnectState>(`/proxy-binding-groups/${encodeURIComponent(editorGroupId)}/routes`, {
          method: "PUT",
          body: { proxyIds },
        });
      } else {
        result = await api<ProxyBindingReconnectState>("/proxy-binding-groups", {
          method: "POST",
          body: {
            deviceId: editorTarget.deviceId,
            iccid: editorTarget.iccid,
            profileName: editorTarget.profileName || editorTarget.iccid,
            proxyIds,
          },
        });
      }
      const reconnectOutcome = proxyBindingReconnectOutcome(result);
      if (reconnectOutcome === "failed") {
        message.warning(
          editorRoutes.length
            ? tf("代理顺序已保存，但立即 VoWiFi 重连失败；将在下次启动或重连时应用：{error}", { error: result.reconnectError || "" })
            : tf("已清除手动绑定，但立即 VoWiFi 重连失败；将在下次启动或重连时使用 MCC 国家规则：{error}", { error: result.reconnectError || "" }),
        );
      } else if (reconnectOutcome === "requested") {
        message.success(
          editorRoutes.length
            ? t("代理顺序已保存，正在重连 VoWiFi")
            : t("已清除手动绑定，正在重连 VoWiFi"),
        );
      } else {
        message.success(
          editorRoutes.length
            ? t("代理顺序已保存，将在下次 VoWiFi 启动或重连时应用")
            : t("已清除手动绑定，将在下次 VoWiFi 启动或重连时使用 MCC 国家规则"),
        );
      }
      await loadGroups(false);
      onChanged();
      setEditorTarget(null);
      setEditorGroupId("");
      setEditorRoutes([]);
      setRouteBaseline([]);
    } catch (error) {
      message.error(apiMessage(error) || t("保存代理顺序失败"));
    } finally {
      setSaving(false);
    }
  };

  const removeGroup = async () => {
    if (!editorGroupId) return;
    const ok = await confirmDialog(
      t("确定删除这张卡的手动代理绑定吗？"),
      t("删除绑定"),
      { type: "warning", confirmText: t("删除"), cancelText: t("取消") },
    );
    if (!ok) return;
    setSaving(true);
    try {
      const result = await api<ProxyBindingReconnectState>(
        `/proxy-binding-groups/${encodeURIComponent(editorGroupId)}`,
        { method: "DELETE" },
      );
      const reconnectOutcome = proxyBindingReconnectOutcome(result);
      if (reconnectOutcome === "failed") {
        message.warning(
          tf("代理绑定已删除，但立即 VoWiFi 重连失败；将在下次启动或重连时生效：{error}", {
            error: result.reconnectError || "",
          }),
        );
      } else if (reconnectOutcome === "requested") {
        message.success(t("代理绑定已删除，正在重连 VoWiFi"));
      } else {
        message.success(t("代理绑定已删除，将在下次 VoWiFi 启动或重连时生效"));
      }
      await loadGroups(false);
      onChanged();
      setEditorTarget(null);
      setEditorGroupId("");
      setEditorRoutes([]);
      setRouteBaseline([]);
    } catch (error) {
      message.error(apiMessage(error) || t("删除代理绑定失败"));
    } finally {
      setSaving(false);
    }
  };

  const normalizedQuery = query.trim().toLowerCase();
  const visibleCards = allCards.filter((card) => {
    const group = groupByKey.get(card.key);
    if (routeFilter === "bound" && !group) return false;
    if (routeFilter === "unbound" && group) return false;
    if (deviceFilter && card.deviceId !== deviceFilter) return false;
    if (proxyFilter && !group?.routes.some((route) => route.upstreamProxyId === proxyFilter)) return false;
    if (!normalizedQuery) return true;
    return `${card.deviceName} ${card.deviceId} ${card.profileName} ${card.iccid}`
      .toLowerCase()
      .includes(normalizedQuery);
  });

  const pickerQueryNormalized = pickerQuery.trim().toLowerCase();
  const pickerCards = allCards.filter((card) => {
    if (!pickerQueryNormalized) return true;
    return `${card.deviceName} ${card.deviceId} ${card.profileName} ${card.iccid}`
      .toLowerCase()
      .includes(pickerQueryNormalized);
  });

  const selectedPickerCard = allCards.find((card) => card.key === pickerSelectedKey);
  const selectedPickerGroup = selectedPickerCard ? groupByKey.get(selectedPickerCard.key) : undefined;
  const lastInventoryRead = Math.max(
    0,
    ...Array.from(inventoryCache.current.values()).map((snapshot) => snapshot.fetchedAt),
  );
  const hasStaleInventory =
    Array.from(inventoryCache.current.values()).some((snapshot) => snapshot.stale && snapshot.cards.length > 0) ||
    allCards.some((card) => card.source === "inventory" && !card.online);

  return (
    <section className="ui-card proxy-binding-section mt-6 overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-white/10">
        <div className="min-w-0">
          <h2 className="text-base font-semibold">{t("SIM / Profile 绑定")}</h2>
          <p className="mt-1 text-xs text-gray-500">
            {tf("{total} 张卡 · {bound} 条手动绑定", { total: allCards.length, bound: boundCount })}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Tooltip content={t("刷新绑定关系")}>
            <Button
              icon={<RefreshCw className="h-4 w-4" />}
              loading={groupsLoading}
              onClick={() => void loadGroups(false)}
              aria-label={t("刷新绑定关系")}
            />
          </Tooltip>
          <Button variant="primary" icon={<Plus className="h-4 w-4" />} onClick={openPicker}>
            {t("新增绑定")}
          </Button>
        </div>
      </div>

      <div className="border-b border-gray-100 px-5 py-4 dark:border-white/10">
        <div className="proxy-binding-toolbar">
          <label>
            <span className="mb-1 block text-xs text-gray-500">{t("搜索")}</span>
            <Input
              prefix={<Search className="h-4 w-4" />}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t("搜索设备 / Profile / ICCID")}
            />
          </label>
          <div>
            <span className="mb-1 block text-xs text-gray-500">{t("路由状态")}</span>
            <div className="proxy-binding-segments" role="group" aria-label={t("路由状态")}>
              {([
                ["all", t("全部")],
                ["bound", t("已绑定")],
                ["unbound", t("未绑定")],
              ] as Array<[RouteFilter, string]>).map(([value, label]) => (
                <button
                  key={value}
                  type="button"
                  className={`proxy-binding-segment ${routeFilter === value ? "is-active" : ""}`}
                  aria-pressed={routeFilter === value}
                  onClick={() => setRouteFilter(value)}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>
          <label>
            <span className="mb-1 block text-xs text-gray-500">{t("设备")}</span>
            <Select
              value={deviceFilter}
              onChange={setDeviceFilter}
              placeholder={t("全部设备")}
              options={[
                { value: "", label: t("全部设备") },
                ...devices.map((device) => ({
                  value: device.id,
                  label: device.name || device.id,
                })),
              ]}
            />
          </label>
          <label>
            <span className="mb-1 block text-xs text-gray-500">{t("代理")}</span>
            <Select
              value={proxyFilter}
              onChange={setProxyFilter}
              placeholder={t("全部代理")}
              options={[
                { value: "", label: t("全部代理") },
                ...proxies.map((proxy) => ({
                  value: proxy.id,
                  label: proxy.name || proxy.id,
                })),
              ]}
            />
          </label>
        </div>
      </div>

      {groupsError ? (
        <div className="border-b border-amber-200 bg-amber-50 px-5 py-2 text-xs text-amber-700 dark:border-amber-500/20 dark:bg-amber-500/10 dark:text-amber-300">
          {tf("绑定关系可能不是最新状态：{error}", { error: groupsError })}
        </div>
      ) : null}

      <div className="overflow-x-auto">
        <table className="proxy-binding-table w-full min-w-[980px] text-left text-sm">
          <thead className="border-b border-gray-100 bg-gray-50/70 text-xs uppercase text-gray-500 dark:border-white/10 dark:bg-white/[0.025]">
            <tr>
              <th className="px-4 py-3">{t("设备 / 读卡器")}</th>
              <th className="px-4 py-3">{t("SIM / Profile")}</th>
              <th className="px-4 py-3">ICCID</th>
              <th className="px-4 py-3">{t("代理顺序")}</th>
              <th className="px-4 py-3">{t("路由状态")}</th>
              <th className="px-4 py-3 text-right">{t("操作")}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-white/10">
            {visibleCards.map((card) => {
              const group = groupByKey.get(card.key);
              const routes = [...(group?.routes || [])].sort((a, b) => a.priority - b.priority);
              const hasDisabledProxy = routes.some((route) => !proxyById.get(route.upstreamProxyId)?.enabled);
              return (
                <tr key={card.key} className="hover:bg-sky-50/40 dark:hover:bg-sky-500/[0.04]">
                  <td className="px-4 py-3" data-label={t("设备 / 读卡器")}>
                    <div className="flex items-center gap-2 font-semibold">
                      <Monitor className="h-4 w-4 shrink-0 text-gray-400" />
                      <span className="truncate">{card.deviceName}</span>
                    </div>
                    <div className="mt-1 flex items-center gap-1.5 text-xs text-gray-500">
                      <StatusDot tone={card.online ? "success" : "neutral"} animated={card.online} />
                      {card.online ? t("在线") : t("离线")}
                      <span className="truncate font-mono">{card.deviceId}</span>
                    </div>
                  </td>
                  <td className="px-4 py-3" data-label={t("SIM / Profile")}>
                    <div className="font-medium">{card.profileName || card.iccid}</div>
                    <div className="mt-1 flex flex-wrap gap-1">
                      {card.current ? <Tag type="success">{t("当前使用中")}</Tag> : null}
                      {card.stateText && !card.current ? <Tag type="info">{card.stateText}</Tag> : null}
                    </div>
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-gray-600 dark:text-gray-300" data-label="ICCID">
                    {card.iccid}
                  </td>
                  <td className="px-4 py-3" data-label={t("代理顺序")}>
                    {group ? (
                      <div className="flex flex-wrap items-center gap-1">
                        {routes.map((route, index) => {
                          const proxy = proxyById.get(route.upstreamProxyId);
                          return (
                            <span key={route.id} className="flex items-center gap-1">
                              {index > 0 ? <ChevronRight className="h-3.5 w-3.5 text-gray-400" /> : null}
                              <Tag type={proxy?.enabled ? "primary" : "warning"}>
                                <span className="font-mono">{index + 1}</span>
                                <span className="max-w-[140px] truncate">{proxy?.name || route.upstreamProxyId}</span>
                              </Tag>
                            </span>
                          );
                        })}
                      </div>
                    ) : (
                      <Tag type="info">{t("MCC 回退 / 直连")}</Tag>
                    )}
                  </td>
                  <td className="px-4 py-3" data-label={t("路由状态")}>
                    <div className="flex flex-wrap gap-1">
                      <Tag type={group ? "success" : "info"}>{group ? t("手动绑定") : t("自动路由")}</Tag>
                      {hasDisabledProxy ? <Tag type="warning">{t("包含已禁用代理")}</Tag> : null}
                    </div>
                  </td>
                  <td className="px-4 py-3" data-label={t("操作")}>
                    <div className="flex justify-end gap-1">
                      <Tooltip content={group ? t("编辑代理顺序") : t("绑定代理")}>
                        <Button
                          size="small"
                          plain
                          icon={group ? <Pencil className="h-4 w-4" /> : <Link2 className="h-4 w-4" />}
                          disabled={!card.bindingEligible}
                          onClick={() => openEditor(card)}
                          aria-label={group ? t("编辑代理顺序") : t("绑定代理")}
                        />
                      </Tooltip>
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {groupsLoading && !allCards.length ? (
        <div className="px-6 py-16 text-center text-sm text-gray-400">{t("加载中...")}</div>
      ) : null}
      {!groupsLoading && !visibleCards.length ? (
        <div className="p-5">
          <EmptyState
            title={allCards.length ? t("没有匹配的 SIM / Profile") : t("没有可显示的 SIM / Profile")}
            subtitle={allCards.length ? t("调整搜索或筛选条件。") : t("打开新增绑定读取设备 Profile。")}
            icon={<Link2 className="h-5 w-5" />}
          />
        </div>
      ) : null}

      <Modal
        open={pickerOpen}
        onClose={() => setPickerOpen(false)}
        title={t("选择 SIM / Profile")}
        width="max-w-[1600px]"
        className="proxy-binding-picker-dialog"
        bodyClassName="min-h-0"
      >
        <div className="space-y-4">
          <div className="flex flex-wrap items-end gap-3">
            <div className="min-w-[240px] flex-1">
              <span className="mb-1 block text-xs text-gray-500">{t("搜索")}</span>
              <Input
                prefix={<Search className="h-4 w-4" />}
                value={pickerQuery}
                onChange={(event) => setPickerQuery(event.target.value)}
                placeholder={t("搜索设备 / Profile / ICCID")}
              />
            </div>
            <Button
              icon={<RefreshCw className="h-4 w-4" />}
              loading={inventoryLoading}
              onClick={() => void refreshInventory(true)}
            >
              {t("重新读取 Profile")}
            </Button>
          </div>

          <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500">
            <span>
              {inventoryLoading
                ? tf("正在读取 Profile {done}/{total}", inventoryProgress)
                : lastInventoryRead
                  ? tf("Profile 缓存于 {time}", {
                      time: new Date(lastInventoryRead).toLocaleTimeString(),
                    })
                  : t("尚未读取 Profile")}
            </span>
            {hasStaleInventory ? (
              <Tag type="warning">{t("离线 Profile 为缓存数据，可能不是最新")}</Tag>
            ) : null}
            {inventoryError ? <span className="text-amber-600 dark:text-amber-400">{inventoryError}</span> : null}
          </div>

          <div className="proxy-binding-picker-list overflow-auto rounded-lg border border-gray-100 dark:border-white/10">
            <table className="proxy-binding-picker-table w-full min-w-[860px] text-left text-sm">
              <thead className="sticky top-0 z-10 bg-gray-50 text-xs text-gray-500 dark:bg-[#1c1c22]">
                <tr>
                  <th className="w-12 px-4 py-3"></th>
                  <th className="px-4 py-3">{t("设备 / 读卡器")}</th>
                  <th className="px-4 py-3">{t("SIM / Profile")}</th>
                  <th className="px-4 py-3">ICCID</th>
                  <th className="px-4 py-3">{t("绑定状态")}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-white/10">
                {pickerCards.map((card) => {
                  const existing = groupByKey.get(card.key);
                  const disabled = !!existing || !card.bindingEligible;
                  return (
                    <tr
                      key={card.key}
                      className={`${disabled ? "opacity-60" : "cursor-pointer hover:bg-sky-50/50 dark:hover:bg-sky-500/[0.05]"} ${
                        pickerSelectedKey === card.key ? "bg-sky-50 dark:bg-sky-500/10" : ""
                      }`}
                      onClick={() => {
                        if (!disabled) setPickerSelectedKey(card.key);
                      }}
                    >
                      <td className="px-4 py-3">
                        <input
                          type="radio"
                          name="proxy-binding-card"
                          checked={pickerSelectedKey === card.key}
                          disabled={disabled}
                          onChange={() => setPickerSelectedKey(card.key)}
                          aria-label={`${card.profileName} ${card.iccid}`}
                        />
                      </td>
                      <td className="px-4 py-3">
                        <div className="font-medium">{card.deviceName}</div>
                        <div className="mt-1 font-mono text-xs text-gray-500">{card.deviceId}</div>
                      </td>
                      <td className="px-4 py-3">
                        <div className="font-medium">{card.profileName || card.iccid}</div>
                        {card.current ? <div className="mt-1"><Tag type="success">{t("当前使用中")}</Tag></div> : null}
                      </td>
                      <td className="px-4 py-3 font-mono text-xs">{card.iccid}</td>
                      <td className="px-4 py-3">
                        {existing ? (
                          <Tag type="info">{t("已有手动绑定")}</Tag>
                        ) : !card.bindingEligible ? (
                          <Tag type="warning">{t("缺少可用 ICCID")}</Tag>
                        ) : card.online ? (
                          <Tag type="primary">{t("可绑定")}</Tag>
                        ) : (
                          <Tag type="primary">{t("离线可绑定")}</Tag>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {!pickerCards.length ? (
              <div className="px-5 py-12 text-center text-sm text-gray-400">{t("没有可显示的 SIM / Profile")}</div>
            ) : null}
          </div>

          <div className="flex justify-end gap-2">
            <Button onClick={() => setPickerOpen(false)}>{t("取消")}</Button>
            <Button
              variant="primary"
              icon={<ArrowRight className="h-4 w-4" />}
              disabled={
                !selectedPickerCard ||
                !selectedPickerCard.bindingEligible ||
                !!selectedPickerGroup ||
                inventoryLoading
              }
              onClick={() => selectedPickerCard && openEditor(selectedPickerCard)}
            >
              {t("配置代理")}
            </Button>
          </div>
        </div>
      </Modal>

      <Drawer
        open={!!editorTarget}
        onClose={() => void requestCloseEditor()}
        closeOnOverlay={!routeDirty}
        side="right"
        widthClass="w-[min(720px,100vw)]"
        className="proxy-binding-drawer"
      >
        {editorTarget ? (
          <>
            <div className="flex shrink-0 items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-white/10">
              <div className="min-w-0">
                <h3 className="truncate text-base font-semibold">
                  {editorGroupId ? t("编辑代理顺序") : t("绑定代理")}
                </h3>
                <p className="mt-1 truncate text-xs text-gray-500">
                  {editorTarget.deviceName} · {editorTarget.profileName || editorTarget.iccid}
                </p>
              </div>
              <Tooltip content={t("关闭")}>
                <Button
                  plain
                  icon={<X className="h-4 w-4" />}
                  onClick={() => void requestCloseEditor()}
                  aria-label={t("关闭")}
                />
              </Tooltip>
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto px-5 py-5">
              <div className="border-b border-gray-100 pb-4 dark:border-white/10">
                <div className="text-sm font-semibold">{editorTarget.profileName || editorTarget.iccid}</div>
                <div className="mt-1 flex flex-wrap items-center gap-2 font-mono text-xs text-gray-500">
                  <span>{editorTarget.iccid}</span>
                  <StatusDot tone={editorTarget.online ? "success" : "neutral"} animated={editorTarget.online} />
                  <span>
                    {editorTarget.online
                      ? t("在线")
                      : editorTarget.bindingEligible
                        ? t("设备离线，可编辑")
                        : t("离线，只读")}
                  </span>
                  {!editorTarget.online && editorTarget.bindingEligible ? (
                    <Tag type="warning">{t("保存后待设备恢复生效")}</Tag>
                  ) : null}
                </div>
              </div>

              <div className="mt-5 flex items-center justify-between">
                <div>
                  <h4 className="text-sm font-semibold">{t("代理顺序")}</h4>
                  <p className="mt-1 text-xs text-gray-500">{t("优先使用序号较小的代理。")}</p>
                </div>
                {routeDirty ? <Tag type="warning">{t("未保存")}</Tag> : null}
              </div>

              <div className="proxy-binding-route-list mt-3">
                {editorRoutes.map((route, index) => {
                  const proxy = proxyById.get(route.upstreamProxyId);
                  return (
                    <div
                      key={route.id}
                      draggable={!saving && editorTarget.bindingEligible}
                      onDragStart={() => setDragIndex(index)}
                      onDragEnd={() => {
                        setDragIndex(null);
                        setDragOverIndex(null);
                      }}
                      onDragOver={(event) => onRouteDragOver(event, index)}
                      onDrop={(event) => {
                        event.preventDefault();
                        dropRoute(index);
                      }}
                      className={`proxy-binding-route-row ${
                        dragIndex === index ? "is-dragging" : ""
                      } ${dragOverIndex === index && dragIndex !== index ? "is-drag-over" : ""}`}
                    >
                      <GripVertical className="h-4 w-4 cursor-grab text-gray-400" />
                      <span className="flex h-6 w-6 items-center justify-center rounded-full bg-sky-100 font-mono text-xs font-semibold text-sky-700 dark:bg-sky-500/15 dark:text-sky-300">
                        {index + 1}
                      </span>
                      <div className="min-w-0">
                        <div className="truncate text-sm font-medium">{proxy?.name || route.upstreamProxyId}</div>
                        <div className="mt-0.5 flex items-center gap-2">
                          <span className="truncate font-mono text-xs text-gray-500">{proxy?.addr || route.upstreamProxyId}</span>
                          {!proxy?.enabled ? <Tag type="warning">{t("已禁用")}</Tag> : null}
                        </div>
                      </div>
                      <div className="proxy-binding-route-actions flex gap-1">
                        <Tooltip content={t("上移")}>
                          <Button
                            size="small"
                            plain
                            icon={<ArrowUp className="h-4 w-4" />}
                            disabled={saving || !editorTarget.bindingEligible || index === 0}
                            onClick={() => moveRoute(index, -1)}
                            aria-label={t("上移")}
                          />
                        </Tooltip>
                        <Tooltip content={t("下移")}>
                          <Button
                            size="small"
                            plain
                            icon={<ArrowDown className="h-4 w-4" />}
                            disabled={saving || !editorTarget.bindingEligible || index === editorRoutes.length - 1}
                            onClick={() => moveRoute(index, 1)}
                            aria-label={t("下移")}
                          />
                        </Tooltip>
                        <Tooltip content={t("移除")}>
                          <Button
                            size="small"
                            variant="danger"
                            plain
                            icon={<Trash2 className="h-4 w-4" />}
                            disabled={saving || !editorTarget.bindingEligible}
                            onClick={() => setEditorRoutes((current) => current.filter((_, routeIndex) => routeIndex !== index))}
                            aria-label={t("移除")}
                          />
                        </Tooltip>
                      </div>
                    </div>
                  );
                })}
                {!editorRoutes.length ? (
                  <div className="rounded-lg border border-dashed border-gray-300 px-4 py-10 text-center text-sm text-gray-400 dark:border-gray-700">
                    {t("没有手动代理顺序，保存后将使用 MCC 国家规则")}
                  </div>
                ) : null}
              </div>

              <div className="mt-4 grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
                <Select
                  value={selectedProxyId}
                  onChange={setSelectedProxyId}
                  placeholder={t("选择要添加的代理")}
                  disabled={saving || !editorTarget.bindingEligible}
                  options={availableProxies.map((proxy) => ({
                    value: proxy.id,
                    label: `${proxy.name || proxy.id}（${proxy.addr}）`,
                  }))}
                />
                <Button
                  icon={<Plus className="h-4 w-4" />}
                  disabled={saving || !editorTarget.bindingEligible || !selectedProxyId}
                  onClick={() => {
                    const proxy = proxies.find((item) => item.id === selectedProxyId);
                    if (!proxy) return;
                    setEditorRoutes((current) => [
                      ...current,
                      { id: `draft:${proxy.id}:${Date.now()}`, upstreamProxyId: proxy.id },
                    ]);
                    setSelectedProxyId("");
                  }}
                >
                  {t("添加代理")}
                </Button>
              </div>
            </div>

            <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t border-gray-100 px-5 py-4 dark:border-white/10">
              <div>
                {editorGroupId ? (
                  <Button
                    variant="danger"
                    plain
                    icon={<Trash2 className="h-4 w-4" />}
                    loading={saving}
                    disabled={!editorTarget.bindingEligible}
                    onClick={() => void removeGroup()}
                  >
                    {t("删除绑定")}
                  </Button>
                ) : null}
              </div>
              <div className="flex gap-2">
                <Button disabled={saving} onClick={() => void requestCloseEditor()}>{t("取消")}</Button>
                <Button
                  variant="primary"
                  loading={saving}
                  disabled={!editorTarget.bindingEligible || !routeDirty}
                  onClick={() => void save()}
                >
                  {t("保存顺序")}
                </Button>
              </div>
            </div>
          </>
        ) : null}
      </Drawer>
    </section>
  );
}
