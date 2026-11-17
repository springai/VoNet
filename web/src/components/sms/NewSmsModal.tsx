import { useEffect, useMemo, useState, type ReactNode } from "react";
import { DismissRegular, SendRegular } from "@fluentui/react-icons";
import { Button, Input, Modal, Select, Textarea } from "../ui";
import type { SimCard } from "../../types";
import { activeCardDevice } from "../cards/cardApi";
import { analyzeSmsEncoding } from "./smsText";
import { tf, useI18n } from "../../lib/i18n";

export interface NewSmsPayload {
  iccid: string;
  phone: string;
  message: string;
}

export interface NewSmsModalProps {
  open: boolean;
  cards: SimCard[];
  defaultICCID: string;
  sending: boolean;
  onClose: () => void;
  onSend: (payload: NewSmsPayload) => void;
  variant?: "modal" | "panel";
}

function FormItem({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div>
      <label className="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">{label}</label>
      {children}
    </div>
  );
}

export function NewSmsModal({ open, cards, defaultICCID, sending, onClose, onSend, variant = "modal" }: NewSmsModalProps) {
  const { t } = useI18n();
  const [iccid, setICCID] = useState(defaultICCID);
  const [phone, setPhone] = useState("");
  const [content, setContent] = useState("");

  // Reset the form every time the dialog opens (reference `Yt`).
  useEffect(() => {
    if (open) {
      const requested = cards.find((card) => card.iccid === defaultICCID && activeCardDevice(card));
      const firstActive = cards.find((card) => activeCardDevice(card));
      setICCID(requested?.iccid || firstActive?.iccid || "");
      setPhone("");
      setContent("");
    }
  }, [open, defaultICCID, cards]);

  const deviceOptions = useMemo(
    () => cards.map((card) => ({
      value: card.iccid,
      label: `${card.displayName || card.carrier || card.iccid}${card.status === "active" ? "" : "（离线）"}`,
      disabled: !activeCardDevice(card),
    })),
    [cards],
  );
  const info = useMemo(() => analyzeSmsEncoding(content), [content]);
  const length = useMemo(() => Array.from(content || "").length, [content]);

  if (!open) return null;

  if (variant === "panel") {
    return (
      <section className="sms-new-panel">
        <header className="sms-new-header">
          <div className="sms-new-title">
            <span className="sms-new-icon"><SendRegular /></span>
            <div>
              <strong>{t("新短信")}</strong>
              <p>{t("选择发送设备并填写收件人")}</p>
            </div>
          </div>
          <button type="button" className="sms-icon-button" onClick={onClose} aria-label={t("关闭")}>
            <DismissRegular />
          </button>
        </header>
        <div className="sms-new-fields">
          <FormItem label={t("发送设备")}>
            <Select value={iccid} onChange={setICCID} options={deviceOptions} placeholder={t("选择卡片")} size="large" />
          </FormItem>
          <FormItem label={t("目标号码")}>
            <Input value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="+12025550177" inputSize="large" />
          </FormItem>
        </div>
        <div className="sms-new-composer">
          <Textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            placeholder={t("短信内容")}
            rows={3}
            className="sms-new-textarea"
          />
          <Button
            variant="primary"
            className="sms-send-circle"
            loading={sending}
            disabled={!iccid || !phone.trim() || !content.trim() || !activeCardDevice(cards.find((card) => card.iccid === iccid))}
            icon={<SendRegular />}
            aria-label={t("发送")}
            title={t("发送")}
            onClick={() => onSend({ iccid, phone, message: content })}
          />
          <div className="sms-composer-meta">
            <span>{t("经当前卡片发送")}</span>
            <span>{tf("{encoding} · 预计 {parts} 段 · {length} 字", { encoding: info.encoding, parts: info.parts, length })}</span>
          </div>
        </div>
      </section>
    );
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("发送短信")}
      width="max-w-[min(520px,92vw)]"
      footer={
        <>
          <Button onClick={onClose}>{t("取消")}</Button>
          <Button variant="primary" loading={sending} disabled={!iccid || !activeCardDevice(cards.find((card) => card.iccid === iccid))} icon={<SendRegular />} onClick={() => onSend({ iccid, phone, message: content })}>
            {t("发送")}
          </Button>
        </>
      }
    >
      <div className="mt-2 space-y-4">
        <FormItem label={t("发送卡片")}>
          <Select value={iccid} onChange={setICCID} options={deviceOptions} placeholder={t("选择卡片")} />
        </FormItem>
        <FormItem label={t("目标号码")}>
          <Input value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="+12025550177" />
        </FormItem>
        <FormItem label={t("短信内容")}>
          <Textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            placeholder={t("输入短信内容...")}
            rows={4}
            className="max-h-64 resize-none [field-sizing:content]"
          />
          <div className="mt-2 flex justify-end text-xs text-gray-400">
            {tf("{encoding} · 预计 {parts} 段 · {length} 字", { encoding: info.encoding, parts: info.parts, length })}
          </div>
        </FormItem>
      </div>
    </Modal>
  );
}
