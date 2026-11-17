import { useState, type PointerEvent, type RefObject, type UIEvent, type KeyboardEvent } from "react";
import { CopyRegular, DeleteRegular, InfoRegular, MoreHorizontalRegular, SendRegular } from "@fluentui/react-icons";
import { Button, EmptyState, Textarea, message } from "../ui";
import { cx } from "../../lib/utils";
import type { SMSMessage } from "../../types";
import {
  messageBody,
  messageDeliveryIndicator,
  messageType,
  type MessageGroup,
  type SmsEncodingInfo,
  type SmsThread,
} from "./smsText";
import { tf, useI18n } from "../../lib/i18n";

export interface ThreadPanelProps {
  isMobile: boolean;
  isDesktop: boolean;
  selectedDevice: string;
  activeThread: SmsThread | null;
  canLoadMore: boolean;
  loadingMore: boolean;
  groups: MessageGroup[];
  deletingMessageId: number | null;
  canHover: boolean;
  composer: string;
  composerInfo: SmsEncodingInfo;
  composerLength: number;
  sending: boolean;
  detailRef: RefObject<HTMLDivElement | null>;
  composerRef: RefObject<HTMLTextAreaElement | null>;
  onBack: () => void;
  onScrollToBottom: () => void;
  onLoadMore: () => void;
  onDeleteMessage: (m: SMSMessage) => void;
  onComposerChange: (v: string) => void;
  onSend: () => void;
  onDetailScroll: (e: UIEvent<HTMLDivElement>) => void;
  onMsgPointerDown: (m: SMSMessage, e: PointerEvent) => void;
  onMsgPointerMove: (e: PointerEvent) => void;
  onMsgPointerEnd: (e: PointerEvent) => void;
  onDeleteThread: (thread: SmsThread) => void;
}

function DeleteMsgButton({
  loading,
  id,
  canHover,
  onDelete,
}: {
  loading: boolean;
  id: number;
  canHover: boolean;
  onDelete: () => void;
}) {
  const { t } = useI18n();
  return (
    <Button
      variant="text"
      size="small"
      loading={loading}
      aria-label={t("删除短信 ") + id}
      title={t("删除短信")}
      onClick={onDelete}
      className={cx("sms-danger-ghost-btn sms-delete-trigger sms-message-delete-btn", !canHover && "sms-delete-visible")}
      icon={<DeleteRegular />}
    />
  );
}

function verificationCode(text: string): string {
  const labeled = text.match(/(?:验证码|校验码|verification code|code)\D{0,16}(\d{4,8})/i);
  if (labeled?.[1]) return labeled[1];
  return /(?:验证码|校验码|verification|otp|code|密码)/i.test(text)
    ? text.match(/\b(\d{4,8})\b/)?.[1] || ""
    : "";
}

export function ThreadPanel(props: ThreadPanelProps) {
  const { t } = useI18n();
  const [menuOpen, setMenuOpen] = useState(false);
  const {
    isMobile,
    isDesktop,
    selectedDevice,
    activeThread,
    canLoadMore,
    loadingMore,
    groups,
    deletingMessageId,
    canHover,
    composer,
    composerInfo,
    composerLength,
    sending,
    detailRef,
    composerRef,
    onBack,
    onScrollToBottom,
    onLoadMore,
    onDeleteMessage,
    onComposerChange,
    onSend,
    onDetailScroll,
    onMsgPointerDown,
    onMsgPointerMove,
    onMsgPointerEnd,
    onDeleteThread,
  } = props;

  const subtitle =
    selectedDevice === "all"
      ? activeThread && (activeThread.localPhone || activeThread.lastDeviceName)
        ? tf("本机：{phone}", { phone: activeThread.localPhone || activeThread.lastDeviceName })
        : t("全部卡片")
      : tf("卡片：{device}", { device: selectedDevice });

  function onComposerKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey) {
      e.preventDefault();
      onSend();
    }
  }

  return (
    <div className="sms-thread-panel flex min-h-0 min-w-0 flex-col">
      <div className="sms-thread-header">
        <div className="sms-thread-identity">
          <span className="sms-thread-peer-icon">
            {String(activeThread?.peer || "").trim().startsWith("+") ? "☎" : "#"}
          </span>
          <div className="min-w-0">
            <strong>{activeThread?.peer || t("请选择会话")}</strong>
            <p>{subtitle}</p>
          </div>
        </div>
        {activeThread ? (
          <div className="sms-thread-actions">
            {isMobile ? <Button variant="text" onClick={onBack}>{t("返回")}</Button> : null}
            <Button variant="text" onClick={onScrollToBottom}>{t("最新")}</Button>
            <div className="sms-thread-menu-wrap">
              <button
                type="button"
                className="sms-icon-button"
                aria-label={t("会话操作")}
                onClick={() => setMenuOpen((value) => !value)}
              >
                <MoreHorizontalRegular />
              </button>
              {menuOpen ? (
                <div className="sms-thread-menu">
                  <button
                    type="button"
                    onClick={() => {
                      setMenuOpen(false);
                      message.info(`${activeThread.peer} · ${subtitle}`);
                    }}
                  >
                    <InfoRegular />
                    {t("会话信息")}
                  </button>
                  <button
                    type="button"
                    className="is-danger"
                    onClick={() => {
                      setMenuOpen(false);
                      onDeleteThread(activeThread);
                    }}
                  >
                    <DeleteRegular />
                    {t("删除整个会话")}
                  </button>
                </div>
              ) : null}
            </div>
          </div>
        ) : null}
      </div>
      {activeThread ? (
        <div ref={detailRef} onScroll={onDetailScroll} className="sms-detail-scroll min-h-0 flex-1 overflow-y-auto">
          <div className="space-y-5 p-5">
            {canLoadMore ? (
              <div className="flex justify-center">
                <Button variant="text" loading={loadingMore} onClick={onLoadMore}>
                  {t("加载更多")}
                </Button>
              </div>
            ) : null}
            {groups.map((g) => (
              <div key={g.date} className="sms-day-group">
                <div className="sms-day-label">{g.date}</div>
                {g.items.map((m) => {
                  const outbound = messageType(m) !== 1;
                  const delivery = messageDeliveryIndicator(m);
                  const code = verificationCode(messageBody(m));
                  return (
                    <div key={m.id} className={cx("sms-message-row", outbound && "is-outbound")}>
                      <div
                        className="sms-msg-wrapper group"
                        onPointerDown={(e) => onMsgPointerDown(m, e)}
                        onPointerMove={onMsgPointerMove}
                        onPointerUp={onMsgPointerEnd}
                        onPointerCancel={onMsgPointerEnd}
                      >
                        <div className="sms-message-meta">
                          {!outbound ? <span>{m.sender || activeThread.peer}</span> : null}
                          {m.deviceName ? <span className="sms-message-device">{m.deviceName}</span> : null}
                          <span>{new Date(m.timestamp).toLocaleTimeString()}</span>
                          {outbound && delivery === "delivered" ? (
                            <span className="is-delivered" title={t("已确认送达")}>✓✓</span>
                          ) : outbound && delivery === "accepted_unconfirmed" ? (
                            <span className="is-accepted" title={t("已提交，尚未确认送达")}>✓</span>
                          ) : outbound && delivery === "failed" ? (
                            <span className="is-failed" title={t("发送失败")}>✗</span>
                          ) : outbound ? (
                            <span title={t("发送状态未知")}>?</span>
                          ) : null}
                          <DeleteMsgButton
                            loading={deletingMessageId === m.id}
                            id={m.id}
                            canHover={canHover}
                            onDelete={() => onDeleteMessage(m)}
                          />
                        </div>
                        <div className={cx("sms-message-bubble", outbound && "is-outbound")}>
                          {m.decodeError ? (
                            <div className="sms-decode-error" role="status">{t("短信解析失败")}：{m.decodeError}</div>
                          ) : null}
                          {messageBody(m) || <span className="text-gray-400">{t("短信正文为空")}</span>}
                        </div>
                        {code ? (
                          <button
                            type="button"
                            className="sms-verification-chip"
                            onClick={() => void navigator.clipboard.writeText(code)}
                          >
                            <span>{t("验证码")}</span>
                            <strong>{code}</strong>
                            <CopyRegular />
                            {t("复制")}
                          </button>
                        ) : null}
                      </div>
                    </div>
                  );
                })}
              </div>
            ))}
            <div className="h-2" />
          </div>
        </div>
      ) : (
        <div className="flex flex-1 items-center justify-center p-6">
          <EmptyState title={t("请选择一个会话")} subtitle={t("从左侧联系人列表进入短信明细")} />
        </div>
      )}
      {activeThread ? (
        <div className="sms-composer-panel">
          <div className="sms-composer-box">
            <Textarea
              ref={composerRef}
              value={composer}
              onChange={(e) => onComposerChange(e.target.value)}
              onKeyDown={onComposerKeyDown}
              placeholder={t("短信内容")}
              rows={1}
              className="sms-composer-textarea"
            />
            <Button
              variant="primary"
              className="sms-send-circle"
              loading={sending}
              disabled={!composer.trim()}
              onClick={onSend}
              icon={<SendRegular />}
              aria-label={t("发送")}
              title={t("发送")}
            />
          </div>
          <div className="sms-composer-meta">
            <span>{tf("经 {device} 发送", { device: activeThread.localPhone || activeThread.lastDeviceName || t("当前卡片") })}</span>
            <span>
              {composerInfo.encoding === "GSM7"
                ? t("单段：GSM7 160 字符，中文 70 字")
                : tf("单段：UCS2 70 字 · 当前 {length} 字", { length: composerLength })}
            </span>
          </div>
        </div>
      ) : null}
    </div>
  );
}
