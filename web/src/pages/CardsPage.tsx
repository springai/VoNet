import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import {
  AddRegular,
  ArrowDownloadRegular,
  ArrowUploadRegular,
  CardUiRegular,
  ChevronLeftRegular,
} from "@fluentui/react-icons";
import { SignalHigh, SignalLow, SignalMedium, Wifi, WifiLow, WifiOff } from "lucide-react";
import { api, apiMessage } from "../api";
import {
  Button,
  confirmDialog,
  EmptyState,
  ErrorState,
  Input,
  ListSkeleton,
  Modal,
  RefreshButton,
  Select,
  Tag,
  Textarea,
  message,
} from "../components/ui";
import { CardPolicyPanel } from "../components/devices/CardPolicyPanel";
import { CardDeviceConfig } from "../components/cards/CardDeviceConfig";
import { CardConfigTab } from "../components/cards/CardConfigTab";
import { CardAtUssdTab } from "../components/cards/CardAtUssdTab";
import { getCardPolicy } from "../components/devices/deviceActions";
import { createCard, deleteCard, importCards, isCardOperable, listCardCandidates, listCards } from "../components/cards/cardApi";
import { CardOverviewTab } from "../components/cards/CardOverviewTab";
import { buildCardTransfer, cardTransferFilename, parseCardTransfer } from "../components/cards/cardTransfer";
import { useMediaQuery } from "../lib/useMediaQuery";
import { tf, useI18n } from "../lib/i18n";
import { cx } from "../lib/utils";
import { displayICCID } from "../lib/sensitive";
import { useShowSensitive } from "../components/devices/shared";
import { WorkspaceSectionNav } from "../components/shell/WorkspaceSectionNav";
import type { DeviceDetail } from "../components/devices/types";
import type {
  CardPolicy,
  SimCard,
  SimCardCandidate,
  SimCardInput,
  SimCardStatus,
  SimCardType,
  DeviceListItem,
} from "../types";

type CardTab = "overview" | "card_config" | "config" | "policy" | "at_ussd";

const VALID_TABS = new Set<CardTab>(["overview", "card_config", "config", "policy", "at_ussd"]);
const EMPTY_CARD_FORM: SimCardInput = {
  iccid: "",
  displayName: "",
  notes: "",
  cardType: "physical_sim",
};

function cardTone(status: SimCardStatus) {
  if (status === "active") return "success";
  if (status === "conflict") return "danger";
  if (status === "unknown") return "warning";
  return "neutral";
}

function cardTagTone(status: SimCardStatus) {
  if (status === "active") return "success";
  if (status === "conflict") return "danger";
  if (status === "unknown") return "warning";
  return "info";
}

function cardStatusLabel(status: SimCardStatus) {
  if (status === "active") return "当前启用";
  if (status === "conflict") return "ICCID 冲突";
  if (status === "unknown") return "状态未知";
  return "离线";
}

function cardTypeLabel(type: SimCardType) {
  return type === "esim_profile" ? "eSIM Profile" : "实体 SIM";
}

function deviceStatusLabel(card: SimCard) {
  if (card.conflict) return "绑定冲突";
  if (isCardOperable(card)) return "设备在线";
  if (card.binding?.deviceId) return "设备不可用";
  return "未绑定设备";
}

function policyStatusLabel(card: SimCard) {
  switch (card.policyStatus) {
    case "applied":
      return "策略已执行";
    case "pending":
      return "策略执行中";
    case "waiting":
      return "等待设备";
    case "suspended":
      return "策略已暂停";
    case "failed":
      return "策略失败";
    case "missing":
      return "缺少策略";
    default:
      return "策略未执行";
  }
}

function deviceStatusTone(card: SimCard) {
  if (card.conflict) return "danger" as const;
  if (card.status === "active" && card.binding?.deviceId) return "success" as const;
  return "info" as const;
}

function policyStatusTone(card: SimCard) {
  if (card.policyStatus === "failed") return "danger" as const;
  if (card.policyStatus === "suspended") return "warning" as const;
  if (card.policyStatus === "applied" || card.policyStatus === "pending") return "success" as const;
  return "info" as const;
}

function isReaderCard(
  card: SimCard | null | undefined,
  policy: CardPolicy | null,
  candidates: SimCardCandidate[],
): boolean {
  if (!card) return false;
  const iccid = normalizeICCID(card.iccid);
  return (
    card.hostType === "usb_sim_reader" ||
    candidates.some((candidate) =>
      normalizeICCID(candidate.iccid) === iccid &&
      candidate.hostType === "usb_sim_reader",
    ) ||
    policy?.runtimeMode === "pcsc" ||
    card.policy?.runtimeMode === "pcsc" ||
    card.binding?.deviceType === "usb_sim_reader" ||
    card.binding?.backendMode === "pcsc"
  );
}

function isVoWiFiCard(card: SimCard, candidates: SimCardCandidate[]) {
  return isReaderCard(card, null, candidates);
}

function cardCarrierDisplay(card: SimCard, candidate: SimCardCandidate | undefined, device?: DeviceDetail) {
  const modem = device?.modem;
  const homeName = String(modem?.homeCarrierName || "").trim();
  const homePLMN = String(modem?.homeCarrierPlmn || "").trim();
  if (homeName) return homePLMN ? `${homeName} (${homePLMN})` : homeName;
  const operator = String(modem?.operator || "").trim();
  if (operator) return operator;
  const candidateCarrier = String(candidate?.carrier || candidate?.spn || "").trim();
  if (candidateCarrier) return candidateCarrier;
  return String(card.carrier || card.spn || card.homePlmn || "").trim() || "未知运营商";
}

function cardConnectionState(card: SimCard, device: DeviceDetail | undefined, candidates: SimCardCandidate[]) {
  const vowifi = isVoWiFiCard(card, candidates);
  const runtime = device?.vowifiRuntime;
  const runtimeICCID = normalizeICCID(String(runtime?.iccid || ""));
  const runtimeMatchesCard = !!runtimeICCID && runtimeICCID === normalizeICCID(card.iccid);
  if (card.conflict) return { label: "绑定冲突", tone: "danger" as const, online: false, pending: false, level: 0 };
  if (vowifi) {
    const ready = runtimeMatchesCard && !!runtime?.tunnelReady && !!runtime?.imsReady;
    const connecting = runtimeMatchesCard && (!!device?.vowifiActive || !!runtime?.tunnelReady || (!!runtime && runtime.phase !== "idle"));
    if (ready) return { label: "在线", tone: "success" as const, online: true, pending: false, level: 4 };
    if (connecting) return { label: "连接中", tone: "warning" as const, online: false, pending: true, level: 2 };
  } else {
    const level = Math.max(1, Math.min(4, Math.ceil(((device?.modem?.signalDbm ?? -120) + 120) / 15)));
    if (device?.radioRegistered) return { label: "在线", tone: "success" as const, online: true, pending: false, level };
    if (device?.controlOnline) return { label: "搜索网络", tone: "warning" as const, online: false, pending: true, level };
  }
  if (isCardOperable(card)) return { label: "连接中", tone: "warning" as const, online: false, pending: true, level: 2 };
  if (card.status === "unknown") return { label: "状态未知", tone: "warning" as const, online: false, pending: true, level: 1 };
  if (card.status === "active" && !card.binding?.deviceId) return { label: "待绑定", tone: "warning" as const, online: false, pending: true, level: 1 };
  return { label: "离线", tone: "neutral" as const, online: false, pending: false, level: 1 };
}

function CellularSignalIndicator({ online, pending, level }: { online: boolean; pending: boolean; level: number }) {
  const Icon = level >= 3 ? SignalHigh : level >= 2 ? SignalMedium : SignalLow;
  return (
    <span className={cx("card-connectivity-icon", online && "is-online", pending && "is-pending")} aria-hidden="true">
      <Icon />
    </span>
  );
}

function CardConnectivityIndicator({ card, state, candidates }: {
  card: SimCard;
  state: ReturnType<typeof cardConnectionState>;
  candidates: SimCardCandidate[];
}) {
  if (isVoWiFiCard(card, candidates)) {
    const Icon = state.online ? Wifi : state.pending ? WifiLow : WifiOff;
    return (
      <span className={cx("card-connectivity-icon", state.online && "is-online", state.pending && "is-pending")} aria-label={state.online ? "WiFi Calling 在线" : "WiFi Calling 离线"}>
        <Icon />
      </span>
    );
  }
  return <CellularSignalIndicator online={state.online} pending={state.pending} level={state.level} />;
}

function normalizeICCID(value: string) {
  return value.replace(/\s+/g, "");
}

export default function CardsPage() {
  const { t } = useI18n();
  const location = useLocation();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [showSensitive] = useShowSensitive();
  const isMobile = useMediaQuery("(max-width: 900px)");
  const [cards, setCards] = useState<SimCard[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [addOpen, setAddOpen] = useState(false);
  const [addSaving, setAddSaving] = useState(false);
  const [addForm, setAddForm] = useState<SimCardInput>(EMPTY_CARD_FORM);
  const [addICCIDLocked, setAddICCIDLocked] = useState(false);
  const [candidates, setCandidates] = useState<SimCardCandidate[]>([]);
  const [deviceSummaries, setDeviceSummaries] = useState<Record<string, DeviceDetail>>({});
  const [deleting, setDeleting] = useState(false);
  const [importingCards, setImportingCards] = useState(false);
  const [policy, setPolicy] = useState<CardPolicy | null>(null);
  const handledNewICCIDRef = useRef("");
  const importCardsInputRef = useRef<HTMLInputElement>(null);

  const pathICCID = decodeURIComponent(location.pathname.split("/")[2] || "");
  const requestedICCID = normalizeICCID(searchParams.get("iccid") || pathICCID);
  const rawTab = searchParams.get("tab") || "overview";
  const requestedTab = (rawTab === "at" || rawTab === "ussd" ? "at_ussd" : rawTab) as CardTab;
  const activeTab = VALID_TABS.has(requestedTab) ? requestedTab : "overview";
  const selectedCard = useMemo(
    () => cards.find((card) => normalizeICCID(card.iccid) === requestedICCID) || null,
    [cards, requestedICCID],
  );
  const selectedReaderPolicy = isReaderCard(selectedCard, policy, candidates);
  const selectedDeviceOnline = isCardOperable(selectedCard);
  const pendingNewICCID = normalizeICCID(
    searchParams.get("new") || (!selectedCard && pathICCID ? pathICCID : ""),
  );

  useEffect(() => {
    if (loading || !pendingNewICCID || handledNewICCIDRef.current === pendingNewICCID) return;
    const existing = cards.find((card) => normalizeICCID(card.iccid) === pendingNewICCID);
    if (existing) {
      const p = new URLSearchParams(searchParams);
      p.delete("new");
      p.set("iccid", normalizeICCID(existing.iccid));
      if (!p.get("tab")) p.set("tab", "overview");
      handledNewICCIDRef.current = pendingNewICCID;
      setSearchParams(p, { replace: true });
      return;
    }
    const candidate = candidates.find((item) => normalizeICCID(item.iccid) === pendingNewICCID);
    const queryType = searchParams.get("cardType");
    const cardType: SimCardType = queryType === "esim_profile" || candidate?.cardType === "esim_profile"
      ? "esim_profile"
      : "physical_sim";
    setAddForm({
      iccid: pendingNewICCID,
      displayName: searchParams.get("displayName") || candidate?.displayName || candidate?.carrier || "",
      notes: "",
      cardType,
      imsi: candidate?.imsi || "",
      msisdn: candidate?.msisdn || "",
      carrier: candidate?.carrier || "",
      spn: candidate?.spn || "",
      mcc: candidate?.mcc || "",
      mnc: candidate?.mnc || "",
      eid: searchParams.get("eid") || candidate?.eid || "",
      profileAid: searchParams.get("profileAid") || candidate?.profileAid || "",
      deviceId: searchParams.get("deviceId") || candidate?.deviceId || "",
      hostType: (searchParams.get("hostType") || candidate?.hostType || "") as SimCardInput["hostType"],
    });
    setAddICCIDLocked(true);
    handledNewICCIDRef.current = pendingNewICCID;
    setAddOpen(true);
  }, [candidates, cards, loading, pendingNewICCID, searchParams]);

  const refreshDeviceSummaries = useCallback(async () => {
    const deviceResult = await api<{ devices?: DeviceListItem[] }>("/devices").catch(() => ({ devices: [] as DeviceListItem[] }));
    const summaries = await Promise.all((deviceResult.devices || []).map(async (summary) => {
      const overview = await api<{ devices?: DeviceDetail[] }>(`/devices/${encodeURIComponent(summary.id)}/overview`)
        .then((result) => result.devices?.[0])
        .catch(() => undefined);
      const mergedModem = { ...summary.modem } as DeviceDetail["modem"];
      for (const [key, value] of Object.entries(overview?.modem || {})) {
        if (value !== undefined && value !== null && value !== "") {
          Object.assign(mergedModem, { [key]: value });
        }
      }
      const merged = overview ? {
        ...summary,
        ...overview,
        modem: mergedModem,
        vowifiRuntime: overview.vowifiRuntime || summary.vowifiRuntime,
      } as DeviceDetail : summary as unknown as DeviceDetail;
      return [summary.id, merged] as const;
    }));
    setDeviceSummaries(Object.fromEntries(summaries));
  }, []);

  const loadCards = useCallback(async (manual = false) => {
    if (manual) setRefreshing(true);
    else setLoading(true);
    try {
      const cardList = await listCards();
      const candidateList = await listCardCandidates(cardList).catch(() => [] as SimCardCandidate[]);
      setCards(cardList);
      setCandidates(candidateList);
      await refreshDeviceSummaries();
      setError("");
    } catch (e) {
      setError(apiMessage(e) || t("加载卡片列表失败"));
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [refreshDeviceSummaries, t]);

  useEffect(() => {
    void loadCards();
  }, [loadCards]);

  useEffect(() => {
    const timer = window.setInterval(() => void refreshDeviceSummaries(), 5000);
    return () => window.clearInterval(timer);
  }, [refreshDeviceSummaries]);

  const exportCards = useCallback(() => {
    if (cards.length === 0 || importingCards) return;
    const blob = new Blob([JSON.stringify(buildCardTransfer(cards), null, 2)], {
      type: "application/json;charset=utf-8",
    });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = cardTransferFilename();
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
    message.success(tf("已导出 {count} 张卡片配置", { count: cards.length }));
  }, [cards, importingCards]);

  const importCardsFile = useCallback(async (file: File) => {
    if (importingCards) return;
    setImportingCards(true);
    try {
      const parsed = parseCardTransfer(JSON.parse(await file.text()));
      if (parsed.length === 0) {
        message.warning(t("卡片导入文件没有卡片数据"));
        return;
      }
      const confirmed = await confirmDialog(
        t("导入会按 ICCID 创建缺失卡片，并覆盖已有卡片的名称、类型和备注，不会修改绑定或网络策略。"),
        tf("导入 {count} 张卡片", { count: parsed.length }),
        { type: "warning", confirmText: t("确认导入"), cancelText: t("取消") },
      );
      if (!confirmed) return;
      const result = await importCards(parsed);
      await loadCards(true);
      message.success(tf("导入完成：新增 {created} 张，更新 {updated} 张", {
        created: result.created,
        updated: result.updated,
      }));
    } catch (error) {
      message.error(`${t("导入卡片失败")}：${apiMessage(error)}`);
    } finally {
      setImportingCards(false);
    }
  }, [importingCards, loadCards, t]);

  const loadPolicy = useCallback(async (iccid?: string) => {
    const clean = normalizeICCID(iccid || "");
    if (!clean) {
      setPolicy(null);
      return;
    }
    try {
      setPolicy(await getCardPolicy(clean));
    } catch {
      setPolicy(null);
    }
  }, []);

  useEffect(() => {
    void loadPolicy(selectedCard?.iccid);
  }, [loadPolicy, selectedCard?.iccid]);

  useEffect(() => {
    if (!requestedICCID && cards.length) {
      const p = new URLSearchParams(searchParams);
      p.set("iccid", normalizeICCID(cards[0].iccid));
      if (!p.get("tab")) p.set("tab", "overview");
      setSearchParams(p, { replace: true });
    }
  }, [cards, requestedICCID, searchParams, setSearchParams]);

  const selectCard = useCallback((iccid: string, tab: CardTab = activeTab) => {
    const p = new URLSearchParams(searchParams);
    p.delete("new");
    p.set("iccid", normalizeICCID(iccid));
    p.set("tab", tab);
    setSearchParams(p);
  }, [activeTab, searchParams, setSearchParams]);

  const selectTab = useCallback((tab: string) => {
    if (!selectedCard) return;
    selectCard(selectedCard.iccid, tab as CardTab);
  }, [selectCard, selectedCard]);

  const openAdd = useCallback(() => {
    handledNewICCIDRef.current = "";
    setAddForm(EMPTY_CARD_FORM);
    setAddICCIDLocked(false);
    setAddOpen(true);
  }, []);

  const useCandidate = useCallback((candidate: SimCardCandidate) => {
    handledNewICCIDRef.current = normalizeICCID(candidate.iccid);
    setAddForm({
      iccid: normalizeICCID(candidate.iccid),
      displayName: candidate.displayName || candidate.carrier || "",
      notes: "",
      cardType: candidate.cardType,
      imsi: candidate.imsi || "",
      msisdn: candidate.msisdn || "",
      carrier: candidate.carrier || "",
      spn: candidate.spn || "",
      mcc: candidate.mcc || "",
      mnc: candidate.mnc || "",
      eid: candidate.eid || "",
      profileAid: candidate.profileAid || "",
      deviceId: candidate.deviceId || "",
      hostType: candidate.hostType,
    });
    setAddICCIDLocked(true);
    setAddOpen(true);
  }, []);

  const removeSelectedCard = useCallback(async () => {
    if (!selectedCard || deleting) return;
    const confirmed = await confirmDialog(
      t("确定删除这张 SIM 卡吗？卡策略、APN、号码关联和代理绑定也会被删除。"),
      t("删除 SIM 卡"),
      { type: "danger", confirmText: t("删除") },
    );
    if (!confirmed) return;
    setDeleting(true);
    try {
      await deleteCard(selectedCard.iccid);
      message.success(t("SIM 卡已删除"));
      const p = new URLSearchParams(searchParams);
      p.delete("iccid");
      p.delete("tab");
      setSearchParams(p, { replace: true });
      await loadCards();
    } catch (error) {
      message.error(apiMessage(error) || t("删除 SIM 卡失败"));
    } finally {
      setDeleting(false);
    }
  }, [deleting, loadCards, searchParams, selectedCard, setSearchParams, t]);

  const submitAdd = useCallback(async () => {
    const iccid = normalizeICCID(addForm.iccid);
    if (!/^\d{18,22}$/.test(iccid)) {
      message.warning(t("ICCID 必须是 18 到 22 位数字"));
      return;
    }
    if (cards.some((card) => normalizeICCID(card.iccid) === iccid)) {
      message.warning(t("该 ICCID 已经存在"));
      return;
    }
    setAddSaving(true);
    try {
      const created = await createCard({ ...addForm, iccid });
      setAddOpen(false);
      setAddForm(EMPTY_CARD_FORM);
      await loadCards();
      selectCard(created.iccid, "card_config");
      setAddICCIDLocked(false);
      message.success(t("卡片已保存"));
    } catch (e) {
      message.error(apiMessage(e) || t("保存卡片失败"));
    } finally {
      setAddSaving(false);
    }
  }, [addForm, cards, loadCards, selectCard, t]);

  const filteredCards = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return cards;
    return cards.filter((card) =>
      `${card.iccid} ${card.displayName || ""} ${card.carrier || ""} ${card.imsi || ""}`
        .toLowerCase()
        .includes(q),
    );
  }, [cards, query]);
  const availableCandidates = useMemo(
    () => candidates.filter((candidate) => !candidate.registered),
    [candidates],
  );

  const tabs = [
    { key: "overview", label: t("概览") },
    { key: "card_config", label: t("卡片配置") },
    { key: "config", label: t("设备配置") },
    { key: "policy", label: t("卡策略") },
    { key: "at_ussd", label: t("AT/USSD") },
  ];

  return (
    <div className="cards-page workspace-page min-w-0">
      <WorkspaceSectionNav />
      <input
        ref={importCardsInputRef}
        type="file"
        accept=".json,application/json"
        className="hidden"
        onChange={(event) => {
          const file = event.currentTarget.files?.[0];
          event.currentTarget.value = "";
          if (file) void importCardsFile(file);
        }}
      />

      {error ? (
        <ErrorState title={t("卡片列表加载失败")} message={error} retryText={t("重试")} onRetry={() => void loadCards(true)} />
      ) : null}

      <div className={cx("workspace-layout", isMobile && "is-mobile")}>
        {(!isMobile || !selectedCard) && (
          <aside className="workspace-list-panel">
            <div className="workspace-list-header">
              <div className="min-w-0">
                <h2>SIM</h2>
                <span>{tf("{count} 张卡片", { count: cards.length })}</span>
              </div>
              <div className="workspace-list-actions">
                <RefreshButton loading={refreshing} onClick={() => void loadCards(true)} />
                <Button
                  size="small"
                  className="workspace-icon-button"
                  loading={importingCards}
                  icon={<ArrowUploadRegular />}
                  title={t("一键导入卡片")}
                  aria-label={t("一键导入卡片")}
                  onClick={() => importCardsInputRef.current?.click()}
                />
                <Button
                  size="small"
                  className="workspace-icon-button"
                  icon={<ArrowDownloadRegular />}
                  disabled={cards.length === 0 || importingCards}
                  title={t("一键导出卡片")}
                  aria-label={t("一键导出卡片")}
                  onClick={exportCards}
                />
              </div>
            </div>
            <div className="workspace-list-search">
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder={t("搜索卡片名称或运营商")}
              />
            </div>
            <div className="workspace-list-scroll">
              {loading ? (
                <ListSkeleton rows={7} />
              ) : filteredCards.length === 0 ? (
                <EmptyState title={t("暂无卡片")} subtitle={t("请在设备稳定读卡后手动保存 SIM 卡")} />
              ) : (
                filteredCards.map((card) => {
                  const active = normalizeICCID(card.iccid) === requestedICCID;
                  const candidate = candidates.find((item) => normalizeICCID(item.iccid) === normalizeICCID(card.iccid));
                  const device = card.binding?.deviceId ? deviceSummaries[card.binding.deviceId] : undefined;
                  const connection = cardConnectionState(card, device, candidates);
                  const carrier = cardCarrierDisplay(card, candidate, device);
                  return (
                    <button
                      key={card.iccid}
                      type="button"
                      onClick={() => selectCard(card.iccid)}
                      className={cx(
                        "reference-list-item",
                        active
                          ? "is-active"
                          : "",
                      )}
                    >
                      <span className="reference-list-icon">
                        <CardUiRegular className="text-[18px]" />
                      </span>
                      <span className="reference-list-copy">
                        <span className="reference-list-title">
                          {card.displayName || t("未命名卡片")}
                        </span>
                        <span className="reference-list-subtitle">
                          {carrier}
                        </span>
                        <span className={cx("reference-list-connection", connection.tone === "success" && "is-online", connection.tone === "warning" && "is-pending", connection.tone === "danger" && "is-danger")}>
                          <i />
                          {t(connection.label)}
                        </span>
                      </span>
                      <CardConnectivityIndicator card={card} state={connection} candidates={candidates} />
                    </button>
                  );
                })
              )}
            </div>
            <div className="workspace-list-footer">
              <Button block variant="primary" icon={<AddRegular />} onClick={openAdd}>
                {t("添加卡片")}
              </Button>
            </div>
          </aside>
        )}

        <section className="workspace-detail-panel min-w-0">
          {selectedCard ? (
            <>
              {isMobile ? (
                <Button variant="text" className="mb-2" icon={<ChevronLeftRegular />} onClick={() => setSearchParams({})}>
                  {t("返回卡片列表")}
                </Button>
              ) : null}

              <CardOverviewTab
                card={selectedCard}
                tabs={tabs}
                activeTab={activeTab}
                onTabChange={selectTab}
                onOpenSms={() => navigate(`/sms?iccid=${encodeURIComponent(selectedCard.iccid)}`)}
                onOpenCall={() => navigate(`/call?iccid=${encodeURIComponent(selectedCard.iccid)}`)}
                onDelete={() => void removeSelectedCard()}
                deleting={deleting}
              >
                {activeTab === "card_config" ? (
                  <div className="config-tab-surface">
                    <CardConfigTab card={selectedCard} onSaved={() => void loadCards()} />
                  </div>
                ) : null}
                {activeTab === "config" ? (
                  <div className="config-tab-surface">
                    <CardDeviceConfig card={selectedCard} onSaved={() => void loadCards()} />
                  </div>
                ) : null}
                {activeTab === "policy" ? (
                  <CardPolicyPanel
                    deviceId={selectedCard.binding?.deviceId || ""}
                    iccid={selectedCard.iccid}
                    policy={policy}
                    deviceOnline={selectedDeviceOnline}
                    onPolicyChanged={() => loadPolicy(selectedCard.iccid)}
                    wifiCallingOnly={selectedReaderPolicy}
                  />
                ) : null}
                {activeTab === "at_ussd" ? (
                  <CardAtUssdTab
                    key={`at-ussd:${selectedCard.iccid}`}
                    deviceId={selectedCard.binding?.deviceId || ""}
                    iccid={selectedCard.iccid}
                    backendMode={selectedCard.binding?.backendMode || ""}
                    atPort={selectedCard.binding?.atPort || ""}
                    atRunning={
                      !!selectedCard.binding?.deviceId &&
                      selectedCard.binding?.online !== false &&
                      selectedCard.binding?.running !== false
                    }
                    atReader={selectedReaderPolicy}
                    operable={selectedDeviceOnline}
                  />
                ) : null}
              </CardOverviewTab>
            </>
          ) : loading ? (
            <ListSkeleton rows={8} />
          ) : (
            <EmptyState title={t("请选择一张卡片")} subtitle={t("卡片详情包含概览、卡片配置、设备配置、卡策略、AT 和 USSD")} />
          )}
        </section>
      </div>

      <Modal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        title={t("添加卡片")}
        footer={
          <>
            <Button onClick={() => setAddOpen(false)}>{t("取消")}</Button>
            <Button variant="primary" loading={addSaving} onClick={() => void submitAdd()}>
              {t("保存")}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          {availableCandidates.length > 0 ? (
            <div className="rounded-lg border border-sky-200/70 bg-sky-50/70 p-3 dark:border-sky-800/50 dark:bg-sky-900/20">
              <div className="mb-2 text-xs font-semibold text-sky-800 dark:text-sky-200">
                {t("发现待登记卡片")}
              </div>
              <div className="flex flex-wrap gap-2">
                {availableCandidates.map((candidate) => (
                  <Button
                    key={`${candidate.deviceId}:${candidate.iccid}`}
                    size="small"
                    plain
                    disabled={candidate.conflict || candidate.state !== "stable"}
                    onClick={() => useCandidate(candidate)}
                  >
                    <span className="font-mono">{displayICCID(candidate.iccid, showSensitive)}</span>
                    {candidate.carrier ? <span className="ml-1">· {candidate.carrier}</span> : null}
                    <Tag
                      className="ml-1"
                      type={candidate.conflict ? "danger" : candidate.state === "stable" ? "success" : candidate.state === "offline" ? "info" : "warning"}
                    >
                      {candidate.conflict
                        ? t("ICCID 冲突")
                        : candidate.state === "stable"
                          ? t("稳定读取")
                          : candidate.state === "offline"
                            ? t("离线")
                            : t("待稳定确认")}
                    </Tag>
                  </Button>
                ))}
              </div>
            </div>
          ) : null}
          <div>
            <div className="mb-1 text-xs font-bold uppercase text-gray-500">ICCID</div>
            <Input
              value={addICCIDLocked ? displayICCID(addForm.iccid, showSensitive) : addForm.iccid}
              onChange={(event) => setAddForm((form) => ({ ...form, iccid: event.target.value }))}
              inputMode="numeric"
              disabled={addICCIDLocked}
              readOnly={addICCIDLocked}
              aria-readonly={addICCIDLocked}
              title={addICCIDLocked ? t("ICCID 由设备自动读取，不可修改") : undefined}
              className={addICCIDLocked ? "opacity-70" : undefined}
              placeholder={addICCIDLocked ? t("设备自动读取") : t("输入 18 到 22 位 ICCID")}
            />
          </div>
          <div>
            <div className="mb-1 text-xs font-bold uppercase text-gray-500">{t("卡片名称")}</div>
            <Input
              value={addForm.displayName || ""}
              onChange={(event) => setAddForm((form) => ({ ...form, displayName: event.target.value }))}
              placeholder={t("例如：英国 O2")}
            />
          </div>
          <div>
            <div className="mb-1 text-xs font-bold uppercase text-gray-500">{t("卡片类型")}</div>
            <Select
              value={addForm.cardType}
              onChange={(value) => setAddForm((form) => ({ ...form, cardType: value as SimCardType }))}
              options={[
                { value: "physical_sim", label: t("实体 SIM") },
                { value: "esim_profile", label: t("eSIM Profile") },
              ]}
            />
          </div>
          <div>
            <div className="mb-1 text-xs font-bold uppercase text-gray-500">{t("备注")}</div>
            <Textarea
              value={addForm.notes || ""}
              onChange={(event) => setAddForm((form) => ({ ...form, notes: event.target.value }))}
              rows={3}
              placeholder={t("可选")}
            />
          </div>
        </div>
      </Modal>
    </div>
  );
}
