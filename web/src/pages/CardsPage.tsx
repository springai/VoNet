import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { AddRegular, ArrowDownloadRegular, ArrowUploadRegular, CardUiRegular, ChevronLeftRegular, DeleteRegular, MailRegular, PhoneRegular } from "@fluentui/react-icons";
import { apiMessage } from "../api";
import {
  Button,
  confirmDialog,
  EmptyState,
  ErrorState,
  Input,
  ListSkeleton,
  Modal,
  PageHeader,
  RefreshButton,
  Select,
  StatusDot,
  Tabs,
  Tag,
  Textarea,
  message,
} from "../components/ui";
import { CardPolicyPanel } from "../components/devices/CardPolicyPanel";
import { DeviceAtTab } from "../components/devices/DeviceAtTab";
import { DeviceUssdTab } from "../components/devices/DeviceUssdTab";
import { CardDeviceConfig } from "../components/cards/CardDeviceConfig";
import { CardConfigTab } from "../components/cards/CardConfigTab";
import { getCardPolicy } from "../components/devices/deviceActions";
import { createCard, deleteCard, importCards, isCardOperable, listCardCandidates, listCards } from "../components/cards/cardApi";
import { CardOverviewTab } from "../components/cards/CardOverviewTab";
import { buildCardTransfer, cardTransferFilename, parseCardTransfer } from "../components/cards/cardTransfer";
import { SensitiveInfoButton } from "../components/ui/SensitiveInfoButton";
import { useMediaQuery } from "../lib/useMediaQuery";
import { usePolling } from "../lib/usePolling";
import { tf, useI18n } from "../lib/i18n";
import { cx } from "../lib/utils";
import { displayICCID } from "../lib/sensitive";
import { useShowSensitive } from "../components/devices/shared";
import type {
  CardPolicy,
  SimCard,
  SimCardCandidate,
  SimCardInput,
  SimCardStatus,
  SimCardType,
} from "../types";

type CardTab = "overview" | "card_config" | "config" | "policy" | "at" | "ussd";

const VALID_TABS = new Set<CardTab>(["overview", "card_config", "config", "policy", "at", "ussd"]);
const COMPACT_CARD_TAG = "!px-1.5 !py-0 !text-[10px]";

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
  const [candidates, setCandidates] = useState<SimCardCandidate[]>([]);
  const [deleting, setDeleting] = useState(false);
  const [importingCards, setImportingCards] = useState(false);
  const [policy, setPolicy] = useState<CardPolicy | null>(null);
  const handledNewICCIDRef = useRef("");
  const importCardsInputRef = useRef<HTMLInputElement>(null);

  const pathICCID = decodeURIComponent(location.pathname.split("/")[2] || "");
  const requestedICCID = normalizeICCID(searchParams.get("iccid") || pathICCID);
  const requestedTab = (searchParams.get("tab") || "overview") as CardTab;
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
    handledNewICCIDRef.current = pendingNewICCID;
    setAddOpen(true);
  }, [candidates, cards, loading, pendingNewICCID, searchParams]);

  const loadCards = useCallback(async (manual = false) => {
    if (manual) setRefreshing(true);
    else setLoading(true);
    try {
      const cardList = await listCards();
      const candidateList = await listCardCandidates(cardList).catch(() => [] as SimCardCandidate[]);
      setCards(cardList);
      setCandidates(candidateList);
      setError("");
    } catch (e) {
      setError(apiMessage(e) || t("加载卡片列表失败"));
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [t]);

  useEffect(() => {
    void loadCards();
  }, [loadCards]);

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

  usePolling(() => {
    if (!addOpen) void loadCards();
  }, 10000, false);

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

  useEffect(() => {
    if (selectedCard && activeTab === "at" && selectedReaderPolicy) {
      selectCard(selectedCard.iccid, "overview");
    }
  }, [activeTab, selectCard, selectedCard, selectedReaderPolicy]);

  const openAdd = useCallback(() => {
    handledNewICCIDRef.current = "";
    setAddForm(EMPTY_CARD_FORM);
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
    ...(selectedReaderPolicy
      ? []
      : [{
          key: "at",
          label: t("AT 终端"),
          disabled:
            !selectedCard?.binding?.deviceId ||
            selectedCard?.binding?.online === false ||
            selectedCard?.binding?.running === false,
        }]),
    { key: "ussd", label: t("USSD") },
  ];

  return (
    <div className="min-w-0 space-y-4">
      <PageHeader
        title={t("SIM 管理")}
        subtitle={t("卡片是网络、通信和代理策略的主入口")}
        actions={
          <>
            <RefreshButton loading={refreshing} onClick={() => void loadCards(true)} />
            <Button loading={importingCards} icon={<ArrowUploadRegular />} onClick={() => importCardsInputRef.current?.click()}>
              {t("一键导入卡片")}
            </Button>
            <Button icon={<ArrowDownloadRegular />} disabled={cards.length === 0 || importingCards} onClick={exportCards}>
              {t("一键导出卡片")}
            </Button>
            <Button variant="primary" icon={<AddRegular />} onClick={openAdd}>
              {t("保存 SIM 卡")}
            </Button>
            {selectedCard ? (
              <Button variant="danger" icon={<DeleteRegular />} loading={deleting} onClick={() => void removeSelectedCard()}>
                {t("删除卡片")}
              </Button>
            ) : null}
          </>
        }
      />
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

      <div className={cx("grid min-h-[620px] gap-4", isMobile ? "grid-cols-1" : "grid-cols-[320px_minmax(0,1fr)]")}>
        {(!isMobile || !selectedCard) && (
          <section className="ui-card overflow-hidden">
            <div className="border-b border-gray-100 p-4 dark:border-white/10">
              <div className="flex items-center gap-2">
                <Input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder={t("搜索 ICCID、名称或运营商")}
                />
                <SensitiveInfoButton className="shrink-0" />
              </div>
            </div>
            <div className="max-h-[720px] space-y-1.5 overflow-y-auto p-2">
              {loading ? (
                <ListSkeleton rows={7} />
              ) : filteredCards.length === 0 ? (
                <EmptyState title={t("暂无卡片")} subtitle={t("请在设备稳定读卡后手动保存 SIM 卡")} />
              ) : (
                filteredCards.map((card) => {
                  const active = normalizeICCID(card.iccid) === requestedICCID;
                  return (
                    <button
                      key={card.iccid}
                      type="button"
                      onClick={() => selectCard(card.iccid)}
                      className={cx(
                        "flex w-full items-start gap-2.5 rounded-lg border p-2.5 text-left transition-all",
                        active
                          ? "border-sky-300 bg-sky-50/80 shadow-sm ring-1 ring-sky-200 dark:border-sky-500/50 dark:bg-sky-500/10 dark:ring-sky-500/20"
                          : "border-gray-200 bg-white hover:border-gray-300 hover:bg-gray-50/70 dark:border-white/10 dark:bg-white/[0.02] dark:hover:border-white/20 dark:hover:bg-white/5",
                      )}
                    >
                      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-amber-200/70 bg-amber-50 text-amber-600 dark:border-amber-500/20 dark:bg-amber-500/10 dark:text-amber-300">
                        <CardUiRegular className="text-[18px]" />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex min-w-0 items-center justify-between gap-2">
                          <span className="min-w-0">
                            <span className="block truncate text-[13px] font-semibold text-gray-900 dark:text-white">
                              {card.displayName || displayICCID(card.iccid, showSensitive)}
                            </span>
                          </span>
                          <span className="flex shrink-0 items-center gap-1.5">
                            <StatusDot tone={cardTone(card.status)} />
                            <Tag className={COMPACT_CARD_TAG} type={cardTagTone(card.status)}>{cardStatusLabel(card.status)}</Tag>
                          </span>
                        </span>
                        <span className="mt-1.5 flex min-w-0 items-center gap-1.5 rounded-md bg-gray-50 px-1.5 py-0.5 dark:bg-white/5">
                          <span className="shrink-0 text-[9px] font-bold uppercase text-gray-400">ICCID</span>
                          <span className="min-w-0 truncate font-mono text-[10px] text-gray-600 dark:text-gray-300">
                            {displayICCID(card.iccid, showSensitive)}
                          </span>
                        </span>
                        <span className="mt-1 block truncate text-[10px] text-gray-500 dark:text-gray-400">
                          {[card.carrier, card.binding?.deviceName].filter(Boolean).join(" · ") || "--"}
                        </span>
                        <span className="mt-1.5 flex flex-wrap gap-1">
                          <Tag className={COMPACT_CARD_TAG} type={deviceStatusTone(card)}>{deviceStatusLabel(card)}</Tag>
                          <Tag className={COMPACT_CARD_TAG} type={policyStatusTone(card)}>
                            {policyStatusLabel(card)}
                          </Tag>
                          <Tag className={COMPACT_CARD_TAG}>{cardTypeLabel(card.cardType)}</Tag>
                        </span>
                      </span>
                    </button>
                  );
                })
              )}
            </div>
          </section>
        )}

        <section className="ui-card min-w-0 p-5">
          {selectedCard ? (
            <>
              <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  {isMobile ? (
                    <Button variant="text" className="mb-2" icon={<ChevronLeftRegular />} onClick={() => setSearchParams({})}>
                      {t("返回卡片列表")}
                    </Button>
                  ) : null}
                  <div className="flex items-center gap-3">
                    <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-amber-50 text-amber-600 dark:bg-amber-500/10 dark:text-amber-300">
                      <CardUiRegular className="text-[23px]" />
                    </span>
                    <div className="min-w-0">
                      <h2 className="truncate text-xl font-bold text-gray-900 dark:text-white">
                        {selectedCard.displayName || displayICCID(selectedCard.iccid, showSensitive)}
                      </h2>
                      <div className="mt-1 flex flex-wrap items-center gap-2">
                        <span className="font-mono text-xs text-gray-500">{displayICCID(selectedCard.iccid, showSensitive)}</span>
                        <Tag type={cardTagTone(selectedCard.status)}>{cardStatusLabel(selectedCard.status)}</Tag>
                        <Tag type={deviceStatusTone(selectedCard)}>{deviceStatusLabel(selectedCard)}</Tag>
                        <Tag type={policyStatusTone(selectedCard)}>
                          {policyStatusLabel(selectedCard)}
                        </Tag>
                        <Tag>{cardTypeLabel(selectedCard.cardType)}</Tag>
                      </div>
                    </div>
                  </div>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Button icon={<MailRegular />} onClick={() => navigate(`/sms?iccid=${encodeURIComponent(selectedCard.iccid)}`)}>
                    {t("短信")}
                  </Button>
                  <Button icon={<PhoneRegular />} onClick={() => navigate(`/call?iccid=${encodeURIComponent(selectedCard.iccid)}`)}>
                    {t("通话")}
                  </Button>
                  <SensitiveInfoButton />
                </div>
              </div>

              <Tabs tabs={tabs} value={activeTab} onChange={selectTab} />
              <div className="mt-5">
                {activeTab === "overview" ? <CardOverviewTab card={selectedCard} /> : null}
                {activeTab === "card_config" ? (
                  <CardConfigTab card={selectedCard} onSaved={() => void loadCards()} />
                ) : null}
                {activeTab === "config" ? <CardDeviceConfig card={selectedCard} onSaved={() => void loadCards()} /> : null}
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
                {activeTab === "at" && !selectedReaderPolicy && selectedCard.binding?.deviceId ? (
                  <DeviceAtTab
                    key={`at:${selectedCard.iccid}`}
                    deviceId={selectedCard.binding.deviceId}
                    iccid={selectedCard.iccid}
                    backendMode={selectedCard.binding.backendMode || ""}
                    atPort={selectedCard.binding.atPort || ""}
                    running={!!selectedCard.binding.running}
                  />
                ) : null}
                {activeTab === "ussd" ? (
                  <DeviceUssdTab
                    key={`ussd:${selectedCard.iccid}`}
                    deviceId={selectedCard.binding?.deviceId || ""}
                    iccid={selectedCard.iccid}
                    operable={selectedDeviceOnline}
                  />
                ) : null}
              </div>
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
        title={t("保存 SIM 卡")}
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
              value={addForm.iccid}
              onChange={(event) => setAddForm((form) => ({ ...form, iccid: event.target.value }))}
              inputMode="numeric"
              placeholder={t("输入 18 到 22 位 ICCID")}
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
