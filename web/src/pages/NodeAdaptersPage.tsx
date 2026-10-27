import { useCallback, useEffect, useRef, useState } from "react";
import {
  AddRegular, DeleteRegular, EditRegular, PlayRegular, StopRegular,
  CheckmarkCircleRegular,
  DismissCircleRegular, ErrorCircleRegular, WarningRegular,
  ArrowSyncRegular, ArrowSwapRegular, PlugConnectedRegular, DatabaseRegular,
  LinkRegular, EyeRegular, CopyRegular, PauseRegular, ArrowDownloadRegular,
  ChevronDownRegular, ChevronUpRegular,
} from "@fluentui/react-icons";
import { api, eventStreamURL } from "../api";
import { Button, PageHeader, confirmDialog, message, Modal, Input, Textarea, Switch } from "../components/ui";
import { tf, useI18n } from "../lib/i18n";

/* ---------- types ---------- */

interface EnvStatus {
  dbPath: string;
  dbExists: boolean;
  dbReady: boolean;
  dbError: string;
  singboxOk: boolean;
  singboxMsg: string;
}

interface SingboxStatus {
  installed: boolean;
  binaryPath: string;
  version: string;
  installDir: string;
  error: string;
}

interface NodeAdapter {
  id: string;
  name: string;
  protocol: string;
  nodeConfigJson: unknown;
  listenHost: string;
  listenPort: number;
  udpIp: string;
  authEnabled: boolean;
  username: string;
  password: string;
  enabled: boolean;
  httpEnabled: boolean;
  socks5Configured: boolean;
  httpConfigured: boolean;
  httpListenHost: string;
  httpListenPort: number;
  httpStatus: string;
  httpLastError: string;
  status: string;
  lastError: string;
  lastCheckAt: number;
  checkResultJson: unknown;
  createdAt: string;
  updatedAt: string;
}

interface GlobalConfig {
  listenIp: string;
  udpIp: string;
}

interface PingResult {
  host: string;
  port: number;
  reachable: boolean;
  latencyMs: number;
  error?: string;
}

interface HTTPCheckResult {
  reachable: boolean;
  roundTripMs: number;
  diagnosis?: string;
}

interface Socks5CheckResult {
  reachable: boolean;
  handshakeOk: boolean;
  udpAssociateOk: boolean;
  udpExchangeOk: boolean;
  authMethod: string;
  relayAddr: string;
  dnsServer: string;
  dnsName: string;
  dnsRcode: number;
  roundTripMs: number;
  diagnosis: string;
  hint: string;
}

interface ParsedLink {
  name: string;
  protocol: string;
  nodeConfigJson: unknown;
  serverAddr: string;
  serverPort: number;
}

interface SingboxLogEntry {
  time: string;
  source: string;
  msg: string;
}

/* ---------- helpers ---------- */

function decamelizeKey(key: string): string {
  return key.replace(/([a-z0-9])([A-Z])/g, "$1_$2").toLowerCase();
}

/** Recursively convert camelCase keys back to snake_case. */
function decamelize(obj: unknown): unknown {
  if (Array.isArray(obj)) return obj.map(decamelize);
  if (obj !== null && typeof obj === "object") {
    return Object.fromEntries(
      Object.entries(obj as Record<string, unknown>).map(([k, v]) => [decamelizeKey(k), decamelize(v)])
    );
  }
  return obj;
}

/**
 * Safely extract a plain object from nodeConfigJson with snake_case keys.
 * The api() camelize() converts server_port → serverPort, so we decamelize back.
 */
function configObj(raw: unknown): Record<string, unknown> {
  let obj: unknown = raw;
  if (typeof raw === "string") {
    try { obj = JSON.parse(raw); } catch { return {}; }
  }
  if (obj && typeof obj === "object") {
    return decamelize(obj) as Record<string, unknown>;
  }
  return {};
}

function configToString(raw: unknown): string {
  return JSON.stringify(configObj(raw), null, 2);
}

function jsonToYaml(obj: unknown, indent = 0): string {
  if (obj === null || obj === undefined) return "null";
  if (typeof obj === "string") return obj;
  if (typeof obj === "number" || typeof obj === "boolean") return String(obj);
  const pad = "  ".repeat(indent);
  if (Array.isArray(obj)) {
    if (obj.length === 0) return "[]";
    return obj.map((item) => `${pad}- ${jsonToYaml(item, indent + 1).trimStart()}`).join("\n");
  }
  if (typeof obj === "object") {
    const entries = Object.entries(obj as Record<string, unknown>);
    if (entries.length === 0) return "{}";
    return entries
      .map(([k, v]) => {
        const val = jsonToYaml(v, indent + 1);
        if (typeof v === "object" && v !== null) {
          return `${pad}${k}:\n${val}`;
        }
        return `${pad}${k}: ${val}`;
      })
      .join("\n");
  }
  return String(obj);
}

/* ---------- page ---------- */

export default function NodeAdaptersPage() {
  const { t } = useI18n();
  const [env, setEnv] = useState<EnvStatus | null>(null);
  const [envLoading, setEnvLoading] = useState(true);
  const [creatingDb, setCreatingDb] = useState(false);
  const [adapters, setAdapters] = useState<NodeAdapter[]>([]);
  const [globalCfg, setGlobalCfg] = useState<GlobalConfig>({ listenIp: "0.0.0.0", udpIp: "0.0.0.0" });
  const [loading, setLoading] = useState(false);

  // Dialogs
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<NodeAdapter | null>(null);
  const [form, setForm] = useState<NodeAdapter>(emptyForm());
  const [singboxStatus, setSingboxStatus] = useState<SingboxStatus | null>(null);
  const [singboxInstalling, setSingboxInstalling] = useState(false);
  const [singboxScanning, setSingboxScanning] = useState(false);
  const [installLogs, setInstallLogs] = useState<string[]>([]);
  const installEsRef = useRef<EventSource | null>(null);

  // Singbox log viewer
  const [logViewerOpen, setLogViewerOpen] = useState(false);
  const [singboxLogs, setSingboxLogs] = useState<SingboxLogEntry[]>([]);
  const logEndRef = useRef<HTMLDivElement>(null);
  const logEsRef = useRef<EventSource | null>(null);
  const [singboxPaused, setSingboxPaused] = useState(false);
  const [globalDialogOpen, setGlobalDialogOpen] = useState(false);
  const [globalForm, setGlobalForm] = useState<GlobalConfig>({ listenIp: "0.0.0.0", udpIp: "0.0.0.0" });
  const [detectedIps, setDetectedIps] = useState<string[]>([]);

  // Link import
  const [linkInput, setLinkInput] = useState("");
  const [linkParsing, setLinkParsing] = useState(false);
  const [importDialogOpen, setImportDialogOpen] = useState(false);
  const [parsedNodes, setParsedNodes] = useState<ParsedLink[]>([]);
  const [selectedParsed, setSelectedParsed] = useState<Set<number>>(new Set());

  // Ping state
  const [pingResults, setPingResults] = useState<Record<string, PingResult>>({});
  const [pingingId, setPingingId] = useState("");
  // SOCKS5 check state
  const [socks5CheckingId, setSocks5CheckingId] = useState("");
  const [socks5Results, setSocks5Results] = useState<Record<string, Socks5CheckResult>>({});

  // HTTP check state
  const [httpCheckingId, setHttpCheckingId] = useState("");
  const [httpResults, setHttpResults] = useState<Record<string, HTTPCheckResult>>({});

  // View config modal
  const [viewConfigOpen, setViewConfigOpen] = useState(false);
  const [viewConfigData, setViewConfigData] = useState<unknown>(null);
  const [viewConfigTab, setViewConfigTab] = useState<"json" | "yaml">("json");

  /* --- data loading --- */

  const checkEnv = useCallback(async () => {
    setEnvLoading(true);
    try {
      const data = await api<EnvStatus>("/node-adapters/environment");
      setEnv(data);
    } catch { setEnv(null); }
    finally { setEnvLoading(false); }
  }, []);

  const loadAdapters = useCallback(async () => {
    setLoading(true);
    try {
      const data = await api<NodeAdapter[]>("/node-adapters");
      setAdapters(data || []);
    } catch { /* ignore */ }
    try {
      const cfg = await api<GlobalConfig>("/node-adapters/global-config");
      setGlobalCfg(cfg);
    } catch { /* ignore */ }
    setLoading(false);
  }, []);

  useEffect(() => { void checkEnv(); void checkSingbox(); }, [checkEnv]);
  useEffect(() => { if (env?.dbReady) { void loadAdapters(); void detectIps(); } }, [env?.dbReady, loadAdapters]);

  async function checkSingbox() {
    try {
      const status = await api<SingboxStatus>("/node-adapters/singbox/status");
      setSingboxStatus(status);
    } catch { setSingboxStatus(null); }
  }

  async function scanSingbox() {
    setSingboxScanning(true);
    try {
      const status = await api<SingboxStatus>("/node-adapters/singbox/scan", { method: "POST" });
      setSingboxStatus(status);
      if (status.installed) {
        message.success(t("本地扫描成功") + (status.version ? `: ${status.version}` : ""));
      } else {
        message.warning(t("未找到可用的 sing-box 二进制") + (status.error ? `: ${status.error}` : ""));
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("扫描失败") + ": " + msg);
    } finally { setSingboxScanning(false); }
  }

  async function installSingbox() {
    setSingboxInstalling(true);
    setInstallLogs([]);
    try {
      // Use SSE for real-time progress
      const response = await fetch("/api/node-adapters/singbox/install-stream", {
        method: "POST",
        headers: { "Content-Type": "application/json", "Accept": "text/event-stream" },
        credentials: "include",
        body: JSON.stringify({}),
      });
      if (!response.ok || !response.body) {
        throw new Error(`HTTP ${response.status}`);
      }
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      let done = false;
      while (!done) {
        const { value, done: readerDone } = await reader.read();
        done = readerDone;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";
        for (const line of lines) {
          if (line.startsWith("event: ")) {
            const eventType = line.slice(7).trim();
            // Next line should be data
            continue;
          }
          if (line.startsWith("data: ")) {
            const dataStr = line.slice(6);
            try {
              const data = JSON.parse(dataStr);
              if (data.msg) {
                setInstallLogs((prev) => [...prev, data.msg]);
              }
              if (data.installed !== undefined) {
                // This is the done event with status
                setSingboxStatus(data);
                await checkSingbox();
                if (data.installed) {
                  message.success(t("sing-box 安装成功") + (data.version ? `: ${data.version}` : ""));
                }
              }
            } catch { /* ignore parse errors */ }
          }
        }
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("安装失败") + ": " + msg);
      setInstallLogs((prev) => [...prev, t("安装失败") + ": " + msg]);
    } finally {
      setSingboxInstalling(false);
    }
  }

  /* --- singbox log streaming --- */

  function openLogViewer() {
    setLogViewerOpen(true);
    setSingboxLogs([]);
    // Connect to SSE stream.
    logEsRef.current?.close();
    const es = new EventSource(eventStreamURL("/node-adapters/singbox/logs-stream"));
    logEsRef.current = es;
    es.addEventListener("log", (ev) => {
      try {
        const entry = JSON.parse((ev as MessageEvent).data) as SingboxLogEntry;
        setSingboxLogs((prev) => {
          const next = [...prev, entry];
          return next.length > 500 ? next.slice(-500) : next;
        });
      } catch { /* ignore */ }
    });
    es.onerror = () => { es.close(); };
  }

  function closeLogViewer() {
    logEsRef.current?.close();
    logEsRef.current = null;
    setLogViewerOpen(false);
  }

  // Auto-scroll log viewer.
  useEffect(() => {
    if (logViewerOpen && logEndRef.current) {
      logEndRef.current.scrollIntoView({ behavior: "smooth" });
    }
  }, [singboxLogs, logViewerOpen]);

  // Cleanup log SSE on unmount.
  useEffect(() => {
    return () => { logEsRef.current?.close(); };
  }, []);

  async function pauseSingbox() {
    try {
      await api("/node-adapters/singbox/pause", { method: "POST" });
      setSingboxPaused(true);
      message.success(t("sing-box 已暂停"));
      await loadAdapters();
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("暂停失败") + ": " + msg);
    }
  }

  async function resumeSingbox() {
    try {
      const result = await api<{ resumed: number }>("/node-adapters/singbox/resume", { method: "POST" });
      setSingboxPaused(false);
      message.success(tf("已恢复 {count} 个进程", { count: String(result?.resumed || 0) }));
      await loadAdapters();
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("恢复失败") + ": " + msg);
    }
  }

  async function uninstallSingbox() {
    const ok = await confirmDialog(t("确定卸载 sing-box？将停止所有节点转换进程并删除 sing-box 二进制文件。"));
    if (!ok) return;
    try {
      await api("/node-adapters/singbox/uninstall", { method: "POST" });
      message.success(t("sing-box 已卸载"));
      await checkSingbox();
      await checkEnv();
      await loadAdapters();
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("卸载失败") + ": " + msg);
    }
  }

  async function detectIps() {
    try {
      const ips = await api<string[]>("/node-adapters/detect-ips");
      setDetectedIps(ips || []);
      // Auto-set global config to first detected IP if not yet configured
      if (ips && ips.length > 0 && globalCfg.listenIp === "0.0.0.0") {
        const ip = ips[0];
        setGlobalCfg({ listenIp: ip, udpIp: ip });
        setGlobalForm({ listenIp: ip, udpIp: ip });
      }
    } catch { /* ignore */ }
  }

  /* --- create DB --- */

  async function createDb() {
    setCreatingDb(true);
    try {
      await api("/node-adapters/environment/create-db", { method: "POST" });
      message.success(t("数据库创建成功"));
      await checkEnv();
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("创建失败") + ": " + msg);
    } finally { setCreatingDb(false); }
  }

  /* --- link import --- */

  function openImport() {
    setLinkInput("");
    setParsedNodes([]);
    setSelectedParsed(new Set());
    setImportDialogOpen(true);
  }

  async function parseLink() {
    const content = linkInput.trim();
    if (!content) return;
    setLinkParsing(true);
    try {
      const nodes = await api<ParsedLink[]>("/node-adapters/parse-batch", {
        method: "POST",
        body: { content },
      });
      setParsedNodes(nodes || []);
      setSelectedParsed(new Set((nodes || []).map((_, i) => i)));
    } catch {
      try {
        const parsed = await api<ParsedLink>("/node-adapters/parse-link", {
          method: "POST",
          body: { link: content },
        });
        setParsedNodes([parsed]);
        setSelectedParsed(new Set([0]));
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : "";
        message.error(t("解析失败") + ": " + msg);
      }
    } finally { setLinkParsing(false); }
  }

  function toggleParsedSelection(idx: number) {
    setSelectedParsed((prev) => {
      const next = new Set(prev);
      if (next.has(idx)) next.delete(idx); else next.add(idx);
      return next;
    });
  }

  function saveParsedNodes() {
    if (parsedNodes.length === 0) return;
    const nodesToSave = parsedNodes.filter((_, i) => selectedParsed.has(i));
    if (nodesToSave.length === 0) {
      message.warning(t("请至少选择一个节点"));
      return;
    }
    if (nodesToSave.length === 1) {
      const node = nodesToSave[0];
      setEditing(null);
      const f = emptyForm();
      f.name = node.name;
      f.protocol = node.protocol;
      f.nodeConfigJson = configToString(node.nodeConfigJson);
      f.listenHost = globalCfg.listenIp;
      f.httpListenHost = globalCfg.listenIp;
      f.udpIp = globalCfg.udpIp;
      setForm(f);
      setImportDialogOpen(false);
      setDialogOpen(true);
      return;
    }
    void saveMultipleNodes(nodesToSave);
  }

  async function saveMultipleNodes(nodes: ParsedLink[]) {
    let successCount = 0;
    for (const node of nodes) {
      try {
        await api("/node-adapters", {
          method: "POST",
          body: {
            name: node.name,
            protocol: node.protocol,
            node_config_json: configObj(node.nodeConfigJson),
            listen_host: globalCfg.listenIp,
            listen_port: 0,
            udp_ip: globalCfg.udpIp,
            auth_enabled: false,
            username: "",
            password: "",
            enabled: false,
            socks5_configured: true,
            http_configured: false,
          },
        });
        successCount++;
      } catch { /* skip failed */ }
    }
    if (successCount > 0) {
      message.success(tf("成功添加 {count} 个节点", { count: String(successCount) }));
      setImportDialogOpen(false);
      await loadAdapters();
    } else {
      message.error(t("添加失败"));
    }
  }

  /* --- CRUD --- */

  function openAdd() {
    setEditing(null);
    const f = emptyForm();
    f.listenHost = globalCfg.listenIp;
    f.httpListenHost = globalCfg.listenIp;
    f.udpIp = globalCfg.udpIp;
    setForm(f);
    setDialogOpen(true);
  }

  function openEdit(a: NodeAdapter) {
    setEditing(a);
    setForm({ ...a, nodeConfigJson: configToString(a.nodeConfigJson) });
    setDialogOpen(true);
  }

  async function saveAdapter() {
    try {
      let parsedConfig: unknown;
      try { parsedConfig = JSON.parse(form.nodeConfigJson as string); }
      catch { parsedConfig = configObj(form.nodeConfigJson); }

      const payload: Record<string, unknown> = {
        name: form.name,
        protocol: form.protocol,
        node_config_json: parsedConfig,
        listen_host: form.listenHost,
        listen_port: form.listenPort,
        http_listen_host: form.httpListenHost,
        http_listen_port: form.httpListenPort,
        udp_ip: form.udpIp,
        auth_enabled: form.authEnabled,
        username: form.username,
        password: form.password,
        enabled: form.enabled,
        socks5_configured: form.socks5Configured,
        http_configured: form.httpConfigured,
      };
      if (editing) {
        await api(`/node-adapters/${editing.id}`, { method: "PUT", body: payload });
        message.success(t("更新成功"));
      } else {
        await api("/node-adapters", { method: "POST", body: payload });
        message.success(t("添加成功"));
      }
      setDialogOpen(false);
      await loadAdapters();
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("保存失败") + ": " + msg);
    }
  }

  async function deleteAdapter(a: NodeAdapter) {
    const ok = await confirmDialog(tf("确定删除节点 {name}？", { name: a.name }));
    if (!ok) return;
    try {
      await api(`/node-adapters/${a.id}`, { method: "DELETE" });
      message.success(t("删除成功"));
      await loadAdapters();
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("删除失败") + ": " + msg);
    }
  }

  async function convertToSocks5(a: NodeAdapter) {
    try {
      await api(`/node-adapters/${a.id}/actions/convert`, { method: "POST" });
      await loadAdapters();
      // Auto-check SOCKS5 connectivity after convert
      try {
        const probe = await api<Socks5CheckResult>(`/node-adapters/${a.id}/actions/check`, { method: "POST" });
        setSocks5Results((prev) => ({ ...prev, [a.id]: probe }));
        if (probe.reachable && probe.handshakeOk) {
          message.success(t("SOCKS5 代理已启动并验证可用"));
        } else {
          message.warning(t("SOCKS5 已启动但连通性异常") + ": " + (probe.diagnosis || ""));
        }
      } catch {
        message.success(t("SOCKS5 代理已启动"));
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("转换失败") + ": " + msg);
    }
  }

  async function removeSocks5(a: NodeAdapter) {
    try {
      await api(`/node-adapters/${a.id}/actions/remove-socks5`, { method: "POST" });
      message.success(t("SOCKS5 代理已停止"));
      await loadAdapters();
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("停止失败") + ": " + msg);
    }
  }

  async function convertToHTTP(a: NodeAdapter) {
    try {
      await api(`/node-adapters/${a.id}/actions/convert-http`, { method: "POST" });
      await loadAdapters();
      try {
        const probe = await api<HTTPCheckResult>(`/node-adapters/${a.id}/actions/check-http`, { method: "POST" });
        setHttpResults((prev) => ({ ...prev, [a.id]: probe }));
        if (probe.reachable) {
          message.success(t("HTTP 代理已启动并验证可用"));
        } else {
          message.warning(t("HTTP 已启动但连通性异常") + ": " + (probe.diagnosis || ""));
        }
      } catch {
        message.success(t("HTTP 代理已启动"));
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("转换失败") + ": " + msg);
    }
  }

  async function removeHTTP(a: NodeAdapter) {
    try {
      await api(`/node-adapters/${a.id}/actions/remove-http`, { method: "POST" });
      await loadAdapters();
      message.success(t("HTTP 代理已停止"));
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("移除失败") + ": " + msg);
    }
  }

  async function syncStatus(a: NodeAdapter) {
    await api(`/node-adapters/${a.id}/actions/sync-status`, { method: "POST" });
  }

  async function httpCheck(a: NodeAdapter) {
    setHttpCheckingId(a.id);
    try {
      const result = await api<HTTPCheckResult>(`/node-adapters/${a.id}/actions/check-http`, { method: "POST" });
      setHttpResults((prev) => ({ ...prev, [a.id]: result }));
      if (result.reachable) {
        message.success(t("HTTP 连通性正常") + (result.roundTripMs > 0 ? ` (${result.roundTripMs}ms)` : ""));
      } else {
        message.warning(result.diagnosis || t("HTTP 连通性异常"));
      }
    } catch (e: unknown) {
      message.error(t("检测失败") + ": " + (e instanceof Error ? e.message : ""));
    } finally {
      setHttpCheckingId("");
    }
  }

  function httpCheckBadge(a: NodeAdapter) {
    const r = httpResults[a.id];
    if (httpCheckingId === a.id) {
      return <ArrowSyncRegular className="h-4 w-4 animate-spin text-purple-500" />;
    }
    if (!r) return null;
    return r.reachable
      ? <CheckmarkCircleRegular className="h-4 w-4 text-green-500" />
      : <DismissCircleRegular className="h-4 w-4 text-red-500" />;
  }

  /* --- TCP Ping --- */

  async function tcpPing(a: NodeAdapter) {
    const cfg = configObj(a.nodeConfigJson);
    const host = cfg.server as string;
    const port = cfg.server_port as number;
    if (!host || !port) {
      message.warning(t("无法获取服务器地址"));
      return;
    }
    setPingingId(a.id);
    try {
      const result = await api<PingResult>("/node-adapters/tcp-ping", {
        method: "POST",
        body: { host, port },
      });
      setPingResults((prev) => ({ ...prev, [a.id]: result }));
    } catch { /* ignore */ }
    finally { setPingingId(""); }
  }

  /* --- SOCKS5 Check --- */

  async function socks5Check(a: NodeAdapter) {
    setSocks5CheckingId(a.id);
    try {
      const result = await api<Socks5CheckResult>(`/node-adapters/${a.id}/actions/check`, { method: "POST" });
      setSocks5Results((prev) => ({ ...prev, [a.id]: result }));
      if (result.reachable && result.handshakeOk) {
        message.success(t("SOCKS5 连通性正常") + (result.roundTripMs > 0 ? ` (${result.roundTripMs}ms)` : ""));
      } else {
        message.warning(result.diagnosis || t("SOCKS5 连通性异常"));
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("检测失败") + ": " + msg);
    } finally {
      setSocks5CheckingId("");
    }
  }

  function socks5CheckBadge(a: NodeAdapter) {
    const r = socks5Results[a.id];
    if (socks5CheckingId === a.id) {
      return <span className="text-xs text-gray-400">{t("检测中...")}</span>;
    }
    if (!r) return null;
    if (r.reachable && r.handshakeOk && r.udpExchangeOk) {
      return <span className="text-xs text-green-600" title={`RTT: ${r.roundTripMs}ms`}>{t("正常")} {r.roundTripMs}ms</span>;
    }
    if (r.reachable && r.handshakeOk) {
      return <span className="text-xs text-yellow-600" title={r.hint}>{t("握手OK/UDP异常")}</span>;
    }
    return <span className="text-xs text-red-500" title={r.diagnosis + " " + (r.hint || "")}>{r.diagnosis || t("不可达")}</span>;
  }

  /* --- View Config --- */

  function viewConfig(a: NodeAdapter) {
    setViewConfigData(configObj(a.nodeConfigJson));
    setViewConfigTab("json");
    setViewConfigOpen(true);
  }

  function copyConfig(a: NodeAdapter) {
    const cfg = configObj(a.nodeConfigJson);
    navigator.clipboard.writeText(JSON.stringify(cfg, null, 2))
      .then(() => message.success(t("已复制到剪贴板")))
      .catch(() => message.error(t("复制失败")));
  }

  /* --- Global Config --- */

  async function saveGlobalConfig() {
    try {
      await api("/node-adapters/global-config", { method: "PUT", body: globalForm });
      setGlobalCfg(globalForm);
      setGlobalDialogOpen(false);
      message.success(t("保存成功"));
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "";
      message.error(t("保存失败") + ": " + msg);
    }
  }

  /* --- env card --- */

  function envCard() {
    if (!env) return null;
    const items = [
      { ok: env.dbExists, label: t("数据库文件存在") },
      { ok: env.dbReady, label: t("数据库就绪") },
      { ok: env.singboxOk, label: t("sing-box 内核") },
    ];
    return (
      <div className="ui-panel p-4">
        <div className="flex items-center gap-3 mb-3">
          <DatabaseRegular className="h-5 w-5 text-gray-400" />
          <span className="font-medium">{t("环境检查")}</span>
        </div>
        <div className="flex flex-wrap gap-4 text-sm">
          {items.map((it) => (
            <div key={it.label} className="flex items-center gap-1.5">
              {it.ok
                ? <CheckmarkCircleRegular className="h-4 w-4 text-green-500" />
                : <ErrorCircleRegular className="h-4 w-4 text-red-400" />}
              <span>{it.label}</span>
            </div>
          ))}
        </div>
        {!env.dbExists && (
          <div className="mt-3">
            <Button variant="primary" size="small" onClick={createDb} disabled={creatingDb}>
              {creatingDb ? t("创建中...") : t("创建数据库")}
            </Button>
          </div>
        )}
        {env.dbError && <div className="mt-2 text-xs text-red-500">{env.dbError}</div>}
        {!env.singboxOk && env.singboxMsg && env.singboxMsg !== "未安装" && (
          <div className="mt-2 text-xs text-amber-600">{env.singboxMsg}</div>
        )}
      </div>
    );
  }

  /* --- status badge --- */

  function singboxCard() {
    if (!singboxStatus) return null;
    const hasRunning = adapters.some((a) => a.enabled && a.status === "running");
    return (
      <div className="ui-panel p-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <PlugConnectedRegular className="h-5 w-5 text-gray-400" />
            <div>
              <span className="font-medium">sing-box</span>
              {singboxStatus.installed && singboxStatus.version && (
                <span className="ml-2 text-xs text-gray-400">{singboxStatus.version}</span>
              )}
            </div>
            {singboxStatus.installed
              ? <CheckmarkCircleRegular className="h-4 w-4 text-green-500" />
              : <ErrorCircleRegular className="h-4 w-4 text-gray-400" />}
          </div>
          <div className="flex items-center gap-2">
            {!singboxStatus.installed ? (
              <>
                <Button variant="default" size="small" onClick={scanSingbox} disabled={singboxScanning || singboxInstalling}>
                  {singboxScanning ? t("扫描中...") : t("本地扫描")}
                </Button>
                <Button variant="primary" size="small" onClick={installSingbox} disabled={singboxInstalling || singboxScanning}>
                  {singboxInstalling ? t("安装中...") : t("远程下载")}
                </Button>
              </>
            ) : (
              <>
                <Button variant="default" size="small" onClick={() => logViewerOpen ? closeLogViewer() : openLogViewer()}
                  icon={logViewerOpen ? <ChevronUpRegular /> : <ChevronDownRegular />}>
                  {t("运行日志")}
                </Button>
                <Button variant="default" size="small" onClick={() => {
                  if (!singboxStatus?.installed) { message.warning(t("请先安装 sing-box")); return; }
                  adapters.filter(a => (a.enabled && a.socks5Configured && a.listenPort > 0) || (a.httpEnabled && a.httpConfigured && a.httpListenPort > 0)).forEach(a => syncStatus(a));
                }} icon={<ArrowSyncRegular />}>
                  {t("修复")}
                </Button>
                {singboxPaused || !hasRunning ? (
                  <Button variant="success" size="small" onClick={resumeSingbox} icon={<PlayRegular />}>
                    {t("恢复")}
                  </Button>
                ) : (
                  <Button variant="warning" size="small" onClick={pauseSingbox} icon={<PauseRegular />}>
                    {t("暂停")}
                  </Button>
                )}
                <Button variant="danger" size="small" onClick={uninstallSingbox} icon={<DeleteRegular />}>
                  {t("卸载")}
                </Button>
              </>
            )}
          </div>
        </div>
        {singboxStatus.installed && (
          <div className="mt-2 text-xs text-gray-400">
            {t("安装路径")}: <span className="font-mono">{singboxStatus.binaryPath}</span>
          </div>
        )}
        {singboxStatus.error && <div className="mt-2 text-xs text-red-500">{singboxStatus.error}</div>}
        {singboxInstalling && installLogs.length > 0 && (
          <div className="mt-3 border-t dark:border-white/10 pt-3">
            <div className="text-xs font-medium text-gray-500 mb-2">{t("安装进度")}</div>
            <div className="bg-gray-50 dark:bg-black/30 rounded p-2 max-h-32 overflow-y-auto">
              {installLogs.map((log, i) => (
                <div key={i} className="text-xs font-mono text-gray-600 dark:text-gray-400 py-0.5">{log}</div>
              ))}
            </div>
          </div>
        )}
        {logViewerOpen && (
          <div className="mt-3 border-t dark:border-white/10 pt-3">
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-medium text-gray-500">{t("sing-box 实时日志")}</span>
              <span className="text-xs text-gray-400">{singboxLogs.length} {t("行")}</span>
            </div>
            <div className="bg-black rounded-lg p-3 max-h-64 overflow-y-auto font-mono text-xs">
              {singboxLogs.length === 0 && (
                <div className="text-gray-500">{t("等待日志...")}</div>
              )}
              {singboxLogs.map((entry, i) => (
                <div key={i} className="py-0.5 flex gap-2">
                  <span className="text-gray-600 shrink-0">{formatLogTime(entry.time)}</span>
                  <span className={logSourceClass(entry.source)}>{entry.msg}</span>
                </div>
              ))}
              <div ref={logEndRef} />
            </div>
          </div>
        )}
      </div>
    );
  }

  function statusBadge(status: unknown, lastError?: string) {
    const s = typeof status === "string" ? status : "";
    if (s === "running") return <span className="inline-flex items-center gap-1 text-xs text-green-600"><CheckmarkCircleRegular className="h-3.5 w-3.5" />{t("运行中")}</span>;
    if (s === "error") return (
      <span className="inline-flex items-center gap-1 text-xs text-red-500" title={lastError || ""}>
        <ErrorCircleRegular className="h-3.5 w-3.5" />
        {t("错误")}
        {lastError ? <span className="ml-1 max-w-[120px] truncate opacity-70">({lastError})</span> : null}
      </span>
    );
    if (s === "stopped") return <span className="inline-flex items-center gap-1 text-xs text-gray-400"><WarningRegular className="h-3.5 w-3.5" />{t("已停止")}</span>;
    return <span className="text-xs text-gray-400">{s || t("未启用")}</span>;
  }

  /* --- ping display --- */

  function pingDisplay(a: NodeAdapter) {
    const pr = pingResults[a.id];
    if (pingingId === a.id) {
      return <span className="text-xs text-gray-400">{t("测试中...")}</span>;
    }
    if (!pr) return null;
    if (pr.reachable) {
      return <span className="text-xs text-green-600">{pr.latencyMs}ms</span>;
    }
    return <span className="text-xs text-red-500" title={pr.error}>{t("不可达")}</span>;
  }

  /* --- dialogs --- */

  function importDialog() {
    return (
      <Modal open={importDialogOpen} onClose={() => setImportDialogOpen(false)} title={t("按链接添加节点")} width="max-w-3xl">
        <div className="space-y-4">
          <div>
            <label className="block text-sm font-medium mb-1">{t("粘贴节点链接")}</label>
            <Textarea
              value={linkInput}
              onChange={(e) => setLinkInput(e.target.value)}
              placeholder={"vless://uuid@host:port?params#name\nvmess://...\ntrojan://...\n\n" + t("支持单个链接、多个链接（每行一个）或 base64 订阅内容")}
              rows={5}
            />
          </div>
          <div className="flex gap-2">
            <Button variant="primary" size="small" onClick={parseLink} disabled={linkParsing || !linkInput.trim()}>
              {linkParsing ? t("解析中...") : t("解析")}
            </Button>
          </div>
          {parsedNodes.length > 0 && (
            <div className="border rounded-lg overflow-hidden dark:border-white/10">
              <table className="w-full text-sm">
                <thead>
                  <tr className="bg-gray-50 dark:bg-white/5 text-xs text-gray-500">
                    <th className="px-3 py-2 w-8"></th>
                    <th className="px-3 py-2 text-left">{t("名称")}</th>
                    <th className="px-3 py-2 text-left">{t("协议")}</th>
                    <th className="px-3 py-2 text-left">{t("服务器")}</th>
                    <th className="px-3 py-2 text-left">{t("端口")}</th>
                  </tr>
                </thead>
                <tbody>
                  {parsedNodes.map((node, i) => (
                    <tr key={i} className="border-t dark:border-white/5 hover:bg-gray-50/50 dark:hover:bg-white/[.02]">
                      <td className="px-3 py-2">
                        <input type="checkbox" checked={selectedParsed.has(i)} onChange={() => toggleParsedSelection(i)} />
                      </td>
                      <td className="px-3 py-2 font-medium">{node.name}</td>
                      <td className="px-3 py-2 text-xs uppercase">{node.protocol}</td>
                      <td className="px-3 py-2 font-mono text-xs">{node.serverAddr}</td>
                      <td className="px-3 py-2 font-mono text-xs">{node.serverPort}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button variant="text" onClick={() => setImportDialogOpen(false)}>{t("取消")}</Button>
          {parsedNodes.length > 0 && (
            <Button variant="primary" onClick={saveParsedNodes}>
              {selectedParsed.size === 1 ? t("编辑并保存") : tf("保存选中 ({count})", { count: String(selectedParsed.size) })}
            </Button>
          )}
        </div>
      </Modal>
    );
  }

  function dialog() {
    return (
      <Modal open={dialogOpen} onClose={() => setDialogOpen(false)} title={editing ? t("编辑节点") : t("添加节点")} width="max-w-2xl">
        <div className="space-y-3">
          <div>
            <label className="block text-sm font-medium mb-1">{t("名称")}</label>
            <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-sm font-medium mb-1">{t("协议")}</label>
              <select className="w-full h-8 px-3 rounded border border-gray-200 dark:border-white/10 bg-transparent text-sm"
                value={form.protocol} onChange={(e) => setForm({ ...form, protocol: e.target.value })}>
                <option value="vless">VLESS</option>
                <option value="vmess">VMess</option>
                <option value="trojan">Trojan</option>
              </select>
            </div>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1">{t("节点配置 JSON")}</label>
            <Textarea value={form.nodeConfigJson as string} onChange={(e) => setForm({ ...form, nodeConfigJson: e.target.value })} rows={8} className="font-mono text-xs" />
          </div>
          <div className="border-t dark:border-white/10 pt-3">
            <div className="flex items-center gap-2 mb-2">
              <Switch checked={form.socks5Configured} onChange={(v) => setForm({ ...form, socks5Configured: v })} />
              <span className="text-sm font-medium">{t("SOCKS5 本地设置")}</span>
            </div>
            {form.socks5Configured && (
            <>
            <div className="grid grid-cols-3 gap-3">
              <div>
                <label className="block text-xs text-gray-500 mb-1">{t("监听地址")}</label>
                <Input value={form.listenHost} onChange={(e) => setForm({ ...form, listenHost: e.target.value })} />
              </div>
                          <div>
                <label className="block text-xs text-gray-500 mb-1">{t("端口")}</label>
                <Input type="number" value={form.listenPort || ""} onChange={(e) => setForm({ ...form, listenPort: Number(e.target.value) })} />
              </div>
              <div>
                <label className="block text-xs text-gray-500 mb-1">{t("UDP IP")}</label>
                <Input value={form.udpIp} onChange={(e) => setForm({ ...form, udpIp: e.target.value })} />
              </div>
            </div>
            <div className="mt-3">
              <div className="flex items-center gap-2 mb-2">
                <Switch checked={form.authEnabled} onChange={(v) => setForm({ ...form, authEnabled: v })} />
                <span className="text-sm">{t("启用鉴权")}</span>
              </div>


              {form.authEnabled && (
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">{t("用户名")}</label>
                    <Input value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />
                  </div>
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">{t("密码")}</label>
                    <Input type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} />
                  </div>
                </div>
              )}
            </div>
            </>
            )}
          </div>
        </div>

          <div className="border-t dark:border-white/10 pt-3">
            <div className="flex items-center gap-2 mb-2">
              <Switch checked={form.httpConfigured} onChange={(v) => setForm({ ...form, httpConfigured: v })} />
              <span className="text-sm font-medium">{t("HTTP 本地设置")}</span>
            </div>
            {form.httpConfigured && (
            <>
            <div className="grid grid-cols-2 gap-3 mb-3">
              <div>
                <label className="block text-xs text-gray-500 mb-1">{t("HTTP 监听 IP")}</label>
                <Input value={form.httpListenHost} onChange={(e) => setForm({ ...form, httpListenHost: e.target.value })} />
              </div>
              <div>
                <label className="block text-xs text-gray-500 mb-1">{t("HTTP 监听端口")}</label>
                <Input type="number" value={form.httpListenPort || ""} onChange={(e) => setForm({ ...form, httpListenPort: Number(e.target.value) })} />
              </div>
            </div>
            </>
            )}
          </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button variant="text" onClick={() => setDialogOpen(false)}>{t("取消")}</Button>
          <Button variant="primary" onClick={saveAdapter}>{t("保存")}</Button>
        </div>
      </Modal>
    );
  }

  function globalDialog() {
    return (
      <Modal open={globalDialogOpen} onClose={() => setGlobalDialogOpen(false)} title={t("全局设置")}>
        <div className="space-y-3">
          {detectedIps.length > 0 && (
            <div>
              <label className="block text-sm font-medium mb-1">{t("检测到的本机 IP")}</label>
              <div className="flex flex-wrap gap-2">
                {detectedIps.map((ip) => (
                  <button key={ip}
                    className={`px-3 py-1 text-xs rounded border ${globalForm.listenIp === ip ? "bg-blue-500 text-white border-blue-500" : "border-gray-200 dark:border-white/10 hover:bg-gray-100 dark:hover:bg-white/10"}`}
                    onClick={() => setGlobalForm({ listenIp: ip, udpIp: ip })}
                  >{ip}</button>
                ))}
              </div>
            </div>
          )}
          <div>
            <label className="block text-sm font-medium mb-1">{t("SOCKS5 监听 IP")}</label>
            <Input value={globalForm.listenIp} onChange={(e) => setGlobalForm({ ...globalForm, listenIp: e.target.value })} placeholder="0.0.0.0" />
          </div>
          <div>
            <label className="block text-sm font-medium mb-1">{t("UDP IP")}</label>
            <Input value={globalForm.udpIp} onChange={(e) => setGlobalForm({ ...globalForm, udpIp: e.target.value })} placeholder="0.0.0.0" />
          </div>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button variant="text" onClick={() => setGlobalDialogOpen(false)}>{t("取消")}</Button>
          <Button variant="primary" onClick={saveGlobalConfig}>{t("保存")}</Button>
        </div>
      </Modal>
    );
  }

  function viewConfigModal() {
    const cfgObj = viewConfigData && typeof viewConfigData === "object" ? viewConfigData as Record<string, unknown> : {};
    const cfgStr = viewConfigTab === "json"
      ? JSON.stringify(cfgObj, null, 2)
      : jsonToYaml(cfgObj);
    return (
      <Modal open={viewConfigOpen} onClose={() => setViewConfigOpen(false)} title={t("节点配置")} width="max-w-2xl">
        <div className="flex gap-2 mb-3">
          <button
            className={`px-3 py-1 text-xs rounded ${viewConfigTab === "json" ? "bg-blue-500 text-white" : "bg-gray-100 dark:bg-white/10"}`}
            onClick={() => setViewConfigTab("json")}
          >JSON</button>
          <button
            className={`px-3 py-1 text-xs rounded ${viewConfigTab === "yaml" ? "bg-blue-500 text-white" : "bg-gray-100 dark:bg-white/10"}`}
            onClick={() => setViewConfigTab("yaml")}
          >YAML</button>
          <button
            className="ml-auto px-3 py-1 text-xs rounded bg-gray-100 dark:bg-white/10 hover:bg-gray-200 dark:hover:bg-white/20"
            onClick={() => {
              navigator.clipboard.writeText(cfgStr).then(() => message.success(t("已复制")));
            }}
          >{t("复制")}</button>
        </div>
        <pre className="bg-gray-50 dark:bg-black/30 rounded-lg p-4 text-xs overflow-auto max-h-96 font-mono whitespace-pre-wrap">
          {cfgStr}
        </pre>
      </Modal>
    );
  }

  /* --- render --- */

  if (!env?.dbReady && !envLoading) {
    return (
      <div className="node-page space-y-4">
        <PageHeader title={t("节点转换")} subtitle={t("将其他协议节点转换为本地 SOCKS5 节点")} />
        {envCard()}
        {singboxCard()}
      </div>
    );
  }

  return (
    <div className="node-page space-y-4">
      <PageHeader title={t("节点转换")} subtitle={t("将其他协议节点转换为本地 SOCKS5 节点")} actions={
        <div className="flex items-center gap-2">
          <span className="text-xs text-gray-400">{t("IP")}: {globalCfg.listenIp}{detectedIps.length > 1 ? ` (${detectedIps.length} ${t("个可用")})` : ""}</span>
          <Button variant="default" size="small" onClick={() => { setGlobalForm(globalCfg); setGlobalDialogOpen(true); }}>
            {t("全局设置")}
          </Button>
          <Button variant="primary" size="small" onClick={openImport}>
            <LinkRegular className="h-4 w-4 mr-1" /> {t("按链接添加")}
          </Button>
          <Button size="small" onClick={openAdd}>
            <AddRegular className="h-4 w-4 mr-1" /> {t("手动添加")}
          </Button>
        </div>
      } />

      {envCard()}
      {singboxCard()}

      <div className="ui-panel overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-gray-100 dark:border-white/5 text-left text-xs text-gray-500">
              <th className="px-4 py-3">{t("名称")}</th>
              <th className="px-4 py-3">{t("协议")}</th>
              <th className="px-4 py-3">{t("服务器")}</th>
              <th className="px-4 py-3">{t("状态")}</th>
              <th className="px-4 py-3">{t("本地 SOCKS5")}</th>
              <th className="px-4 py-3">{t("本地 HTTP")}</th>
              <th className="px-4 py-3">{t("TCP Ping")}</th>
              <th className="px-4 py-3 text-right">{t("操作")}</th>
            </tr>
          </thead>
          <tbody>
            {adapters.length === 0 && (
              <tr><td colSpan={7} className="px-4 py-10 text-center text-gray-400">{t("暂无节点，点击「按链接添加」开始")}</td></tr>
            )}
            {adapters.map((a) => {
              const cfg = configObj(a.nodeConfigJson);
              return (
                <tr key={a.id} className="border-b border-gray-50 dark:border-white/[.03] hover:bg-gray-50/50 dark:hover:bg-white/[.02]">
                  <td className="px-4 py-3 font-medium">{a.name}</td>
                  <td className="px-4 py-3 text-xs uppercase">{a.protocol}</td>
                  <td className="px-4 py-3 font-mono text-xs">{String(cfg.server || "")}:{String(cfg.server_port || "")}</td>
                  <td className="px-4 py-3">{statusBadge(a.status, a.lastError)}</td>
                  <td className="px-4 py-3 font-mono text-xs">{a.listenHost}:{a.listenPort}</td>
                  <td className="px-4 py-3">
                    {a.httpEnabled ? (
                      <>
                        <div className="font-mono text-xs">{a.httpListenHost}:{a.httpListenPort}</div>
                        <div className="mt-1">{httpCheckBadge(a)}</div>
                      </>
                    ) : (
                      <span className="text-xs text-gray-400">-</span>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-2">
                      {pingDisplay(a)}
                      <button onClick={() => tcpPing(a)} disabled={pingingId === a.id}
                        className="rounded p-1 hover:bg-gray-100 dark:hover:bg-white/10" title={t("TCP Ping")}>
                        <ArrowSyncRegular className={`h-3.5 w-3.5 text-blue-500 ${pingingId === a.id ? "animate-spin" : ""}`} />
                      </button>
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center justify-end gap-1">
                      <button onClick={() => viewConfig(a)}
                        className="rounded p-1.5 hover:bg-gray-100 dark:hover:bg-white/10" title={t("查看配置")}>
                        <EyeRegular className="h-4 w-4 text-blue-500" />
                      </button>
                      <button onClick={() => copyConfig(a)}
                        className="rounded p-1.5 hover:bg-gray-100 dark:hover:bg-white/10" title={t("复制配置")}>
                        <CopyRegular className="h-4 w-4 text-gray-500" />
                      </button>
                      {a.enabled && (
                        <button onClick={() => socks5Check(a)} disabled={socks5CheckingId === a.id}
                          className="rounded p-1.5 hover:bg-gray-100 dark:hover:bg-white/10" title={t("SOCKS5 检测")}>
                          {socks5CheckingId === a.id
                            ? <ArrowSyncRegular className="h-4 w-4 animate-spin text-blue-500" />
                            : <CheckmarkCircleRegular className="h-4 w-4 text-blue-500" />}
                        </button>
                      )}
                      <button onClick={() => { if (!a.enabled && !a.socks5Configured) { message.warning(t("请先在编辑中启用 SOCKS5 本地设置")); return; } a.enabled ? removeSocks5(a) : convertToSocks5(a); }}
                        className="rounded p-1.5 hover:bg-gray-100 dark:hover:bg-white/10" title={a.enabled ? t("移除 SOCKS5") : t("转为 SOCKS5")}>
                        {a.enabled
                          ? <StopRegular className="h-4 w-4 text-orange-500" />
                          : <ArrowSwapRegular className="h-4 w-4 text-green-500" />}
                      </button>
                      {a.httpEnabled && (
                        <button onClick={() => httpCheck(a)} disabled={httpCheckingId === a.id}
                          className="rounded p-1.5 hover:bg-gray-100 dark:hover:bg-white/10" title={t("HTTP 检测")}>
                          {httpCheckingId === a.id
                            ? <ArrowSyncRegular className="h-4 w-4 animate-spin text-purple-500" />
                            : <CheckmarkCircleRegular className="h-4 w-4 text-purple-500" />}
                        </button>
                      )}
                      <button onClick={() => { if (!a.httpEnabled && !a.httpConfigured) { message.warning(t("请先在编辑中启用 HTTP 本地设置")); return; } a.httpEnabled ? removeHTTP(a) : convertToHTTP(a); }}
                        className="rounded p-1.5 hover:bg-gray-100 dark:hover:bg-white/10" title={a.httpEnabled ? t("移除 HTTP") : t("转为 HTTP")}>
                        {a.httpEnabled
                          ? <StopRegular className="h-4 w-4 text-purple-500" />
                          : <ArrowSwapRegular className="h-4 w-4 text-purple-500" />}
                      </button>
                      <button onClick={() => openEdit(a)}
                        className="rounded p-1.5 hover:bg-gray-100 dark:hover:bg-white/10" title={t("编辑")}>
                        <EditRegular className="h-4 w-4" />
                      </button>
                      <button onClick={() => deleteAdapter(a)}
                        className="rounded p-1.5 hover:bg-gray-100 dark:hover:bg-white/10" title={t("删除")}>
                        <DeleteRegular className="h-4 w-4 text-red-500" />
                      </button>
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {importDialog()}
      {dialog()}
      {globalDialog()}
      {viewConfigModal()}
    </div>
  );
}

function formatLogTime(ts: string): string {
  try {
    const d = new Date(ts);
    return d.toLocaleTimeString("zh-CN", { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0");
  } catch { return ts; }
}

function logSourceClass(source: string): string {
  if (source === "stderr" || source === "error") return "text-red-400";
  if (source === "system") return "text-yellow-400";
  return "text-gray-300";
}

function emptyForm(): NodeAdapter {
  return {
    id: "", name: "", protocol: "vless", nodeConfigJson: "{}",
    listenHost: "0.0.0.0", listenPort: 50000, udpIp: "0.0.0.0", httpListenHost: "0.0.0.0", httpListenPort: 60000,
    authEnabled: false, username: "", password: "",
    enabled: false, httpEnabled: false, socks5Configured: true, httpConfigured: false, status: "", httpStatus: "", lastError: "", httpLastError: "", lastCheckAt: 0,
    checkResultJson: "{}", createdAt: "", updatedAt: "",
  };
}
