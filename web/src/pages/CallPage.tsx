import { useCallback, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Delete, PhoneIncoming, PhoneMissed, PhoneOutgoing, Search } from "lucide-react";
import { ErrorState, Input, Spinner } from "../components/ui";
import { useI18n } from "../lib/i18n";
import { usePolling } from "../lib/usePolling";
import { cx } from "../lib/utils";
import { CallPanel } from "../components/call/CallPanel";
import {
  deleteCallRecord,
  getCallHistory,
  type CallRecord,
} from "../components/call/callApi";
import { activeCardDevice, listCards } from "../components/cards/cardApi";
import type { SimCard } from "../types";
import { compareNaturalCards } from "../lib/naturalSort";

function CallHistoryItem({
  record,
  onSelect,
  onDelete,
}: {
  record: CallRecord;
  onSelect: (number: string) => void;
  onDelete: (id: number) => void;
}) {
  const { t } = useI18n();
  const incoming = record.direction === "incoming";
  const missed = record.status === "missed";
  const date = new Date(record.startedAt * 1000);
  const today = date.toDateString() === new Date().toDateString();
  const time = today
    ? date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : `${date.toLocaleDateString([], { month: "short", day: "numeric" })} ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
  const duration = record.durationSeconds > 0
    ? `${Math.floor(record.durationSeconds / 60)}:${String(record.durationSeconds % 60).padStart(2, "0")}`
    : "";
  const Icon = missed ? PhoneMissed : incoming ? PhoneIncoming : PhoneOutgoing;

  return (
    <div className="call-history-item">
      <button type="button" className="call-history-select" onClick={() => record.number && onSelect(record.number)}>
        <span className={cx("call-history-icon", missed && "is-missed")}><Icon /></span>
        <span className="call-history-copy">
          <strong>{record.number || t("未知号码")}</strong>
          <small>{incoming ? t("来电") : t("拨出")} · {time}</small>
        </span>
        <span className="call-history-meta">
          {missed ? t("未接") : duration || t("已接通")}
        </span>
      </button>
      <button
        type="button"
        aria-label={t("删除通话记录")}
        className="call-history-delete"
        onClick={() => onDelete(record.id)}
      >
        <Delete />
      </button>
    </div>
  );
}

export default function CallPage() {
  const { t } = useI18n();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedICCID = (searchParams.get("iccid") || "").replace(/\s+/g, "");
  const requestedDevice = searchParams.get("device") || "";
  const [cards, setCards] = useState<SimCard[]>([]);
  const [selectedCardId, setSelectedCardId] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [history, setHistory] = useState<CallRecord[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const [dialNumber, setDialNumber] = useState("");

  const selectedCard = cards.find((card) => card.iccid === selectedCardId) || null;
  const selectedDeviceId = activeCardDevice(selectedCard);
  const cardOperable = !!selectedDeviceId && selectedCard?.status === "active";

  const selectCard = useCallback((iccid: string) => {
    setSelectedCardId(iccid);
    setSearchParams((previous) => {
      const params = new URLSearchParams(previous);
      params.set("iccid", iccid);
      params.delete("device");
      return params;
    }, { replace: true });
  }, [setSearchParams]);

  const loadCards = useCallback(async () => {
    try {
      const list = [...(await listCards())].sort(compareNaturalCards);
      setCards(list);
      if (list.length > 0) {
        const preferred =
          list.find((card) => card.iccid === requestedICCID)?.iccid ||
          list.find((card) => card.binding?.deviceId === requestedDevice)?.iccid ||
          list.find((card) => card.status === "active")?.iccid ||
          list[0].iccid;
        setSelectedCardId((current) => current && list.some((card) => card.iccid === current) ? current : preferred);
      }
      setError(null);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("加载失败"));
    } finally {
      setLoading(false);
    }
  }, [requestedDevice, requestedICCID, t]);

  const loadHistory = useCallback(async () => {
    if (!cardOperable || !selectedDeviceId) return;
    setHistoryLoading(true);
    try {
      const response = await getCallHistory(selectedDeviceId, searchQuery || undefined, 50, 0, selectedCard?.iccid);
      setHistory(response.records || []);
    } catch {
      // Preserve visible history on transient errors.
    } finally {
      setHistoryLoading(false);
    }
  }, [cardOperable, searchQuery, selectedCard?.iccid, selectedDeviceId]);

  useEffect(() => {
    void loadCards();
  }, [loadCards]);
  usePolling(loadCards, 10000);

  useEffect(() => {
    void loadHistory();
  }, [loadHistory]);
  usePolling(loadHistory, 10000);

  const readyCount = useMemo(() => cards.filter((card) => card.status === "active" && activeCardDevice(card)).length, [cards]);

  async function removeHistory(id: number) {
    if (!selectedDeviceId || !selectedCard) return;
    await deleteCallRecord(selectedDeviceId, id, selectedCard.iccid);
    setHistory((current) => current.filter((record) => record.id !== id));
  }

  if (loading) {
    return <div className="flex items-center justify-center py-20"><Spinner /></div>;
  }
  if (error) return <ErrorState message={error} />;

  return (
    <div className="call-page workspace-page">
      <div className="call-reference-layout">
        <aside className="call-lines-panel">
          <div className="call-lines-heading">
            <strong>{t("通话线路")}</strong>
            <span>{readyCount} {t("条就绪")}</span>
          </div>
          <div className="call-lines-list">
            {cards.map((card) => {
              const operable = card.status === "active" && activeCardDevice(card);
              return (
                <button
                  key={card.iccid}
                  type="button"
                  className={cx("call-line-item reference-list-item", selectedCardId === card.iccid && "is-active")}
                  onClick={() => selectCard(card.iccid)}
                >
                  <span className="reference-list-icon"><PhoneOutgoing /></span>
                  <span className="reference-list-copy">
                    <span className="reference-list-title">{card.displayName || card.carrier || card.iccid}</span>
                    <span className="reference-list-subtitle">{card.binding?.deviceName || card.binding?.deviceId || card.iccid}</span>
                    <span className={cx("reference-list-connection", operable ? "is-online" : "is-danger")}>
                      <i />
                      {operable ? t("在线") : t("离线")}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>

          <div className="call-history-panel">
            <div className="call-history-head">
              <strong>{t("最近通话")}</strong>
              <Input value={searchQuery} onChange={(event) => setSearchQuery(event.target.value)} placeholder={t("搜索号码")} prefix={<Search />} />
            </div>
            <div className="call-history-list">
              {historyLoading && history.length === 0 ? <Spinner className="h-5 w-5" /> : null}
              {!historyLoading && history.length === 0 ? <p>{t("暂无通话记录")}</p> : null}
              {history.map((record) => (
                <CallHistoryItem
                  key={record.id}
                  record={record}
                  onSelect={(number) => setDialNumber(number)}
                  onDelete={(id) => void removeHistory(id)}
                />
              ))}
            </div>
          </div>
        </aside>

        <main className="call-main-panel">
          {selectedCard ? (
            <CallPanel
              card={selectedCard}
              externalNumber={dialNumber}
              onNumberConsumed={() => setDialNumber("")}
            />
          ) : (
            <div className="call-empty-panel">{t("请选择通话线路")}</div>
          )}
        </main>
      </div>
    </div>
  );
}
