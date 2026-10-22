import { useCallback, useEffect, useMemo, useState } from "react";
import { AddRegular, GlobeRegular } from "@fluentui/react-icons";
import { api, ApiError, apiMessage } from "../api";
import type { Country, CountryRule, DeviceListItem, DevicesResponse, UpstreamProxy } from "../types";
import { usePolling } from "../lib/usePolling";
import { Button, PageHeader, confirmDialog, message } from "../components/ui";
import {
  emptyUpstreamForm,
  ipv6AddrError,
  type LoadError,
  type UpstreamForm,
  type UpstreamProbeResult,
  type UpstreamRow,
} from "../components/proxy/shared";
import { UpstreamDialog } from "../components/proxy/UpstreamDialog";
import { CountryRulesDialog } from "../components/proxy/CountryRulesDialog";
import { UpstreamSection } from "../components/proxy/UpstreamSection";
import { ProxyBindingSection } from "../components/proxy/ProxyBindingSection";
import { tf, useI18n } from "../lib/i18n";
import { listPlugins, pluginAssetURL, type InstalledPlugin } from "../extensions";

export default function ProxyPage() {
  const { t, lang } = useI18n();
  const [proxies, setProxies] = useState<UpstreamProxy[]>([]);
  const [devices, setDevices] = useState<DeviceListItem[]>([]);
  const [countries, setCountries] = useState<Country[]>([]);
  const [countryRules, setCountryRules] = useState<CountryRule[]>([]);
  const [upstreamLoading, setUpstreamLoading] = useState(true);
  const [upstreamError, setUpstreamError] = useState<LoadError | null>(null);
  const [upstreamDialogOpen, setUpstreamDialogOpen] = useState(false);
  const [editingUpstream, setEditingUpstream] = useState<UpstreamProxy | null>(null);
  const [upstreamForm, setUpstreamForm] = useState<UpstreamForm>(emptyUpstreamForm());
  const [testingUpstream, setTestingUpstream] = useState(false);
  const [upstreamProbe, setUpstreamProbe] = useState<UpstreamProbeResult | null>(null);
  const [countryDialogOpen, setCountryDialogOpen] = useState(false);
  const [countryBusy, setCountryBusy] = useState(false);
  const [toggleBusyId, setToggleBusyId] = useState("");
  const [plugins, setPlugins] = useState<InstalledPlugin[]>([]);

  const regionNames = useMemo(() => {
    try {
      return new Intl.DisplayNames([lang === "zh" ? "zh-CN" : "en"], { type: "region" });
    } catch {
      return null;
    }
  }, [lang]);

  const proxyRows = useMemo<UpstreamRow[]>(() => proxies.map((proxy) => {
    const countryNames = countryRules
      .filter((rule) => rule.enabled && rule.upstreamProxyId === proxy.id)
      .map((rule) => regionNames?.of(rule.countryCode) || rule.countryName || rule.countryCode);
    return { ...proxy, bindingCount: 0, countryNames };
  }), [proxies, countryRules, regionNames]);

  const loadUpstream = useCallback(async (initial = false) => {
    if (initial) setUpstreamLoading(true);
    setUpstreamError(null);
    try {
      const [proxyList, deviceList, countryList, ruleList] = await Promise.all([
        api<UpstreamProxy[]>("/upstream-proxies"),
        api<DevicesResponse>("/devices"),
        api<Country[]>("/upstream-proxy-countries"),
        api<CountryRule[]>("/upstream-proxy-country-rules"),
      ]);
      setProxies(proxyList || []);
      setDevices(deviceList?.devices || []);
      setCountries(countryList || []);
      setCountryRules(ruleList || []);
    } catch (error) {
      setUpstreamError({ message: apiMessage(error), status: error instanceof ApiError ? error.status : undefined });
    } finally {
      if (initial) setUpstreamLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadUpstream(true);
  }, [loadUpstream]);

  useEffect(() => {
    let active = true;
    const load = () => listPlugins().then((items) => {
      if (active) setPlugins(items || []);
    }).catch(() => undefined);
    void load();
    window.addEventListener("vonet:plugins-changed", load);
    return () => { active = false; window.removeEventListener("vonet:plugins-changed", load); };
  }, []);

  usePolling(() => {
    if (!upstreamLoading) void loadUpstream(false);
  }, 10000, false);

  const openUpstreamDialog = useCallback((proxy?: UpstreamProxy) => {
    setUpstreamProbe(null);
    if (proxy) {
      setEditingUpstream(proxy);
      setUpstreamForm({
        id: proxy.id,
        name: proxy.name,
        addr: proxy.addr,
        username: proxy.username,
        password: proxy.password === "****" ? "" : proxy.password ?? "",
        enabled: proxy.enabled,
      });
    } else {
      setEditingUpstream(null);
      setUpstreamForm(emptyUpstreamForm());
    }
    setUpstreamDialogOpen(true);
  }, []);

  const submitUpstream = useCallback(async () => {
    const form = { ...upstreamForm };
    form.id = (form.id || "").trim();
    form.name = (form.name || "").trim();
    form.addr = (form.addr || "").trim();
    if (!form.id) return message.warning(t("ID 不能为空"));
    if (!form.addr) return message.warning(t("SOCKS5 地址不能为空"));
    const ipv6Error = ipv6AddrError(form.addr);
    if (ipv6Error) return message.warning(ipv6Error);
    try {
      if (editingUpstream) {
        await api(`/upstream-proxies/${form.id}`, { method: "PUT", body: form });
        message.success(t("上游代理已更新，并已执行连通性探测"));
      } else {
        await api("/upstream-proxies", { method: "POST", body: form });
        message.success(t("上游代理已创建，并已执行连通性探测"));
      }
      setUpstreamDialogOpen(false);
      await loadUpstream(false);
    } catch (error) {
      message.error(apiMessage(error) || t("保存失败"));
    }
  }, [upstreamForm, editingUpstream, loadUpstream, t]);

  const testUpstream = useCallback(async () => {
    const form = { ...upstreamForm, addr: (upstreamForm.addr || "").trim() };
    if (!form.addr) return message.warning(t("请先填写 SOCKS5 地址"));
    const ipv6Error = ipv6AddrError(form.addr);
    if (ipv6Error) return message.warning(ipv6Error);
    setTestingUpstream(true);
    setUpstreamProbe(null);
    try {
      const data = await api<{ status?: string; probe?: UpstreamProbeResult; message?: string }>("/upstream-proxy-probe", {
        method: "POST",
        body: {
          id: editingUpstream?.id || "",
          addr: form.addr,
          username: (form.username || "").trim(),
          password: form.password || "",
        },
      });
      setUpstreamProbe(data.probe || null);
      if (data.probe?.udpExchangeOk) {
        message.success(data.message || t("SOCKS5 认证与真实 UDP 往返探测通过"));
      } else {
        message.warning(data.message || t("代理不能承载 VoWiFi 所需的 UDP"));
      }
    } catch (error) {
      message.error(apiMessage(error) || t("连通性检测失败"));
    } finally {
      setTestingUpstream(false);
    }
  }, [upstreamForm, editingUpstream, t]);

  const probeProxy = useCallback(async (proxy: UpstreamProxy) => {
    setToggleBusyId(proxy.id);
    try {
      const data = await api<{ message?: string; probe?: UpstreamProbeResult }>(`/upstream-proxies/${encodeURIComponent(proxy.id)}/actions/probe`, { method: "POST" });
      if (data.probe?.udpExchangeOk) message.success(data.message || t("代理连通性检测通过"));
      else message.warning(data.message || t("代理不能承载 VoWiFi 所需的 UDP"));
    } catch (error) {
      message.error(apiMessage(error) || t("连通性检测失败"));
    } finally {
      setToggleBusyId("");
    }
  }, [t]);

  const removeUpstream = useCallback(async (proxy: UpstreamProxy) => {
    const ok = await confirmDialog(
      <>
        {tf("确定删除上游代理“{name}”？", { name: proxy.name || proxy.id })}
        <br />
        {t("该代理的绑定路由会被删除，MCC 规则也会自动清理。")}
      </>,
      t("确认删除"),
      { confirmText: t("删除"), cancelText: t("取消"), type: "warning" },
    );
    if (!ok) return;
    try {
      await api(`/upstream-proxies/${proxy.id}`, { method: "DELETE" });
      message.success(t("上游代理已删除"));
      await loadUpstream(false);
    } catch (error) {
      message.error(apiMessage(error) || t("删除失败"));
    }
  }, [loadUpstream, t]);

  const toggleUpstream = useCallback(async (proxy: UpstreamProxy) => {
    const enabled = !proxy.enabled;
    setToggleBusyId(proxy.id);
    try {
      await api(`/upstream-proxies/${encodeURIComponent(proxy.id)}`, { method: "PATCH", body: { enabled } });
      message.success(enabled ? t("代理已启用") : t("代理已禁用；手动路由会跳过该代理"));
      await loadUpstream(false);
    } catch (error) {
      message.error(apiMessage(error) || t("切换代理状态失败"));
    } finally {
      setToggleBusyId("");
    }
  }, [loadUpstream, t]);

  const saveCountryRules = useCallback(async (assignments: Record<string, string>) => {
    setCountryBusy(true);
    const current = new Map(countryRules.map((rule) => [rule.countryCode, rule.upstreamProxyId]));
    const changed = Object.entries(assignments).filter(([code, proxyID]) => proxyID && current.get(code) !== proxyID);
    const removed = countryRules.filter((rule) => !assignments[rule.countryCode]);
    try {
      await Promise.all(changed.map(([code, proxyID]) => api(`/upstream-proxy-country-rules/${encodeURIComponent(code)}`, {
        method: "PUT",
        body: { upstreamProxyId: proxyID, enabled: true },
      })));
      await Promise.all(removed.map((rule) => api(`/upstream-proxy-country-rules/${encodeURIComponent(rule.countryCode)}`, {
        method: "DELETE",
      })));
      message.success(t("国家规则已保存"));
      await loadUpstream(false);
      setCountryDialogOpen(false);
    } catch (error) {
      await loadUpstream(false);
      message.error(apiMessage(error) || t("保存规则失败"));
    } finally {
      setCountryBusy(false);
    }
  }, [countryRules, loadUpstream, t]);

  return (
    <div className="proxy-management-page mx-auto w-full max-w-[1600px]">
      <div className="mx-auto max-w-7xl">
      <PageHeader
        title={t("代理管理")}
        subtitle={t("管理上游代理、MCC 国家规则和 SIM / Profile 代理顺序")}
        actions={<Button variant="primary" icon={<AddRegular />} onClick={() => openUpstreamDialog()}>{t("新增代理")}</Button>}
      />
      <UpstreamSection
        rows={proxyRows}
        loading={upstreamLoading}
        error={upstreamError}
        onRetry={() => loadUpstream(false)}
        onEdit={openUpstreamDialog}
        onDelete={removeUpstream}
        onProbe={probeProxy}
        onToggle={(proxy) => void toggleUpstream(proxy)}
        toggleBusyId={toggleBusyId}
      />
      <section className="ui-card mt-6 flex items-center justify-between gap-4 p-5">
        <div>
          <h2 className="text-base font-semibold">{t("MCC 国家规则")}</h2>
          <p className="mt-1 text-xs text-gray-500">{t("没有手动代理绑定的卡片才会使用 MCC 规则。")}</p>
        </div>
        <Button icon={<GlobeRegular />} onClick={() => setCountryDialogOpen(true)}>{t("管理国家规则")}</Button>
      </section>
      </div>
      <ProxyBindingSection proxies={proxies} devices={devices} onChanged={() => void loadUpstream(false)} />
      <div className="mx-auto max-w-7xl">
      {plugins.filter((plugin) => plugin.enabled).flatMap((plugin) =>
        plugin.contributions.filter((contribution) => contribution.location === "proxy").map((contribution) => (
          <section key={`${plugin.id}:${contribution.id}`} className="ui-card mt-6 overflow-hidden p-0">
            <iframe
              title={contribution.label}
              src={pluginAssetURL(plugin, contribution)}
              className="h-[640px] w-full border-0 bg-white dark:bg-[#15151a]"
              sandbox="allow-scripts allow-forms allow-same-origin"
            />
          </section>
        )),
      )}
      </div>
      <UpstreamDialog
        open={upstreamDialogOpen}
        editing={!!editingUpstream}
        form={upstreamForm}
        testing={testingUpstream}
        probe={upstreamProbe}
        onPatch={(patch) => {
          if ("addr" in patch || "username" in patch || "password" in patch) setUpstreamProbe(null);
          setUpstreamForm((form) => ({ ...form, ...patch }));
        }}
        onTest={() => void testUpstream()}
        onClose={() => {
          setUpstreamDialogOpen(false);
          setUpstreamProbe(null);
        }}
        onSubmit={submitUpstream}
      />
      <CountryRulesDialog
        open={countryDialogOpen}
        proxies={proxies}
        countries={countries}
        rules={countryRules}
        busy={countryBusy}
        onSave={(assignments) => void saveCountryRules(assignments)}
        onClose={() => setCountryDialogOpen(false)}
      />
    </div>
  );
}
