import type { PointerEvent } from "react";
import { AddRegular, ChatMultipleRegular, DeleteRegular, NumberSymbolRegular, PhoneRegular, SearchRegular } from "@fluentui/react-icons";
import { Button, EmptyState, Input, ListSkeleton, RefreshButton, Select, type SelectOption } from "../ui";
import { cx } from "../../lib/utils";
import { timeLabel, type SmsThread } from "./smsText";
import { tf, tl, useI18n } from "../../lib/i18n";

export interface ContactListProps {
  isMobile: boolean;
  isDesktop: boolean;
  selectedDevice: string;
  deviceOptions: SelectOption[];
  onSelectDevice: (id: string) => void;
  searchQuery: string;
  onSearchChange: (value: string) => void;
  loading: boolean;
  contacts: SmsThread[];
  activeKey: string;
  isUnread: (t: SmsThread) => boolean;
  deletingKey: string | null;
  canHover: boolean;
  onSelect: (key: string) => void;
  onDelete: (t: SmsThread) => void;
  onRowPointerDown: (t: SmsThread, e: PointerEvent) => void;
  onRowPointerMove: (e: PointerEvent) => void;
  onRowPointerEnd: (e: PointerEvent) => void;
  totalCount: number;
  unreadCount: number;
  threadFilter: "all" | "unread";
  onThreadFilterChange: (value: "all" | "unread") => void;
  onNewSms: () => void;
  onExport: () => void;
  onRefresh: () => void;
  refreshing: boolean;
}

export function ContactList(props: ContactListProps) {
  const { t } = useI18n();
  const {
    isMobile,
    isDesktop,
    selectedDevice,
    deviceOptions,
    onSelectDevice,
    searchQuery,
    onSearchChange,
    loading,
    contacts,
    activeKey,
    isUnread,
    deletingKey,
    canHover,
    onSelect,
    onDelete,
    onRowPointerDown,
    onRowPointerMove,
    onRowPointerEnd,
    totalCount,
    unreadCount,
    threadFilter,
    onThreadFilterChange,
    onNewSms,
    onExport,
    onRefresh,
    refreshing,
  } = props;

  return (
    <div className={cx("sms-contact-panel flex min-h-0 min-w-0 flex-col", isDesktop && "sms-contact-panel-desktop")}>
      <div className="sms-contact-header">
        <div className="sms-contact-heading">
          <div>
            <h2>{t("短信")}</h2>
            <span>{tf("{count} 个会话", { count: totalCount })}</span>
          </div>
          <div className="sms-contact-actions">
            <RefreshButton loading={refreshing} onClick={onRefresh} />
            <Button
              size="small"
              className="sms-export-button"
              onClick={onExport}
            >
              {t("导出")}
            </Button>
            <Button variant="primary" icon={<AddRegular />} onClick={onNewSms}>{t("新短信")}</Button>
          </div>
        </div>
        <div className="sms-contact-search">
          {isMobile && (
            <Select value={selectedDevice} onChange={onSelectDevice} options={deviceOptions} placeholder={t("选择卡片")} />
          )}
          <Input
            value={searchQuery}
            onChange={(e) => onSearchChange(e.target.value)}
            placeholder={t("搜索联系人/内容")}
            prefix={<SearchRegular />}
          />
        </div>
        <div className="sms-thread-segments" role="tablist">
          <button type="button" className={threadFilter === "all" ? "is-active" : ""} onClick={() => onThreadFilterChange("all")}>
            {t("全部")}
          </button>
          <button type="button" className={threadFilter === "unread" ? "is-active" : ""} onClick={() => onThreadFilterChange("unread")}>
            {t("未读")} {unreadCount}
          </button>
        </div>
        <p className="sms-contact-hint">{t("搜索和未读筛选只作用于已加载的会话。")}</p>
      </div>
      {loading && contacts.length === 0 ? (
        <ListSkeleton rows={10} />
      ) : contacts.length === 0 ? (
        <div className="flex flex-1 items-center justify-center p-6">
          <EmptyState title={t("暂无会话")} subtitle={t("等待设备收到测试短信，或点击“发送测试短信”")} icon={<ChatMultipleRegular />} />
        </div>
      ) : (
        <div className="sms-thread-list min-h-0 flex-1 overflow-auto">
          {contacts.map((thread) => (
            <div
              key={thread.key}
              className={cx(
                "sms-thread-item-shell border-b border-gray-100 dark:border-white/10",
                activeKey === thread.key && "sms-thread-item-shell-active",
              )}
            >
              <div
                className={cx(
                  "sms-thread-row group flex items-start gap-2 px-4 py-3 transition-all",
                  activeKey === thread.key ? "sms-thread-row-active" : "hover:bg-gray-50/60 dark:hover:bg-white/5",
                )}
                onPointerDown={(e) => onRowPointerDown(thread, e)}
                onPointerMove={onRowPointerMove}
                onPointerUp={onRowPointerEnd}
                onPointerCancel={onRowPointerEnd}
              >
                <button type="button" className="min-w-0 flex-1 text-left" onClick={() => onSelect(thread.key)}>
                  <div className="sms-thread-row-grid">
                    <span className="sms-thread-avatar">
                      {String(thread.peer || "").trim().startsWith("+") ? <PhoneRegular /> : <NumberSymbolRegular />}
                    </span>
                    <div className="sms-thread-copy">
                      <div className="sms-thread-title">{thread.peer}</div>
                      <div className="sms-thread-preview">{thread.lastMessage}</div>
                      <div className="sms-thread-device">{thread.localPhone || thread.lastDeviceName || t("全部卡片")}</div>
                    </div>
                    <div className="sms-thread-meta text-right">
                      <div>{timeLabel(thread.lastTs)}</div>
                      {isUnread(thread) ? <span className="sms-unread-badge">1</span> : null}
                    </div>
                  </div>
                </button>
{!isMobile && (
  <Button
    variant="text"
    size="small"
    loading={deletingKey === thread.key}
    aria-label={tl("删除与 ") + thread.peer + tl(" 的对话")}
    title={tl("删除对话")}
    onClick={(e) => {
      e.stopPropagation();
      onDelete(thread);
    }}
    className={cx(
      "sms-danger-ghost-btn sms-delete-trigger sms-thread-delete-btn",
      !canHover && "sms-delete-visible",
    )}
    icon={<DeleteRegular />}
  />
)}
              </div>
            </div>
          ))}
        </div>
      )}
      <div className="sms-contact-footer">
        <span>↻</span>
        {t("页面可见时每 5 秒自动刷新")}
      </div>
    </div>
  );
}
