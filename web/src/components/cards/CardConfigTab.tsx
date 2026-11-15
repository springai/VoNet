import { useEffect, useState } from "react";
import { CardUiRegular, SaveRegular } from "@fluentui/react-icons";
import { apiMessage } from "../../api";
import type { SimCard, SimCardType } from "../../types";
import { Button, Input, Select, Textarea, message } from "../ui";
import { useI18n } from "../../lib/i18n";
import { updateCard } from "./cardApi";
import { cardConfigDirty, cardConfigDraft, cardConfigPatch, type CardConfigDraft } from "./cardConfigModel";
import { SensitiveInfoButton } from "../ui/SensitiveInfoButton";
import { useShowSensitive } from "../devices/shared";
import { displayICCID } from "../../lib/sensitive";

export function CardConfigTab({ card, onSaved }: { card: SimCard; onSaved: () => void | Promise<void> }) {
  const { t } = useI18n();
  const [draft, setDraft] = useState<CardConfigDraft>(() => cardConfigDraft(card));
  const [saving, setSaving] = useState(false);
  const [showSensitive] = useShowSensitive();
  const dirty = cardConfigDirty(card, draft);

  useEffect(() => {
    setDraft(cardConfigDraft(card));
  }, [card.iccid]);

  async function save() {
    if (!dirty || saving) return;
    setSaving(true);
    try {
      await updateCard(card.iccid, cardConfigPatch(draft));
      message.success(t("卡片配置已保存"));
      await onSaved();
    } catch (error) {
      message.error(apiMessage(error) || t("卡片配置保存失败"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="max-w-3xl">
      <div className="mb-5 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-indigo-50 text-indigo-600 dark:bg-indigo-500/10 dark:text-indigo-400">
            <CardUiRegular className="text-[22px]" />
          </div>
          <div>
            <div className="text-lg font-bold text-gray-900 dark:text-white">{t("卡片配置")}</div>
            <div className="text-xs text-gray-500 dark:text-gray-400">{t("修改当前卡片的显示名称、卡片类型和备注")}</div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <SensitiveInfoButton />
          <Button
            variant="primary"
            loading={saving}
            disabled={!dirty}
            onClick={() => void save()}
            className="!border-0"
            icon={<SaveRegular />}
          >
            {t("保存卡片配置")}
          </Button>
        </div>
      </div>
      <div className="space-y-5">
        <div>
          <div className="mb-1.5 text-xs font-bold uppercase text-gray-500">{t("卡片名称")}</div>
          <Input
            value={draft.displayName}
            onChange={(event) => setDraft((current) => ({ ...current, displayName: event.target.value }))}
            placeholder={t("例如：英国 O2")}
            maxLength={128}
          />
        </div>
        <div>
          <div className="mb-1.5 text-xs font-bold uppercase text-gray-500">{t("卡片类型")}</div>
          <Select
            value={draft.cardType}
            onChange={(value) => setDraft((current) => ({ ...current, cardType: value as SimCardType }))}
            options={[
              { value: "physical_sim", label: t("实体 SIM") },
              { value: "esim_profile", label: t("eSIM Profile") },
            ]}
          />
        </div>
        <div>
          <div className="mb-1.5 text-xs font-bold uppercase text-gray-500">{t("备注")}</div>
          <Textarea
            value={draft.notes}
            onChange={(event) => setDraft((current) => ({ ...current, notes: event.target.value }))}
            placeholder={t("记录卡片用途、套餐或注意事项")}
            rows={4}
            maxLength={2000}
          />
          <div className="mt-1.5 text-right text-xs text-gray-400">{draft.notes.length} / 2000</div>
        </div>
        <div>
          <div className="mb-1.5 text-xs font-bold uppercase text-gray-500">ICCID</div>
          <Input
            value={displayICCID(card.iccid, showSensitive)}
            disabled
            readOnly
            aria-readonly="true"
            title={t("ICCID 创建后不可修改")}
            className="opacity-70"
          />
          <div className="mt-1.5 text-xs text-gray-400">{t("ICCID 创建后不可修改")}</div>
        </div>
      </div>
    </div>
  );
}
