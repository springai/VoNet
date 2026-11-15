import { cx } from "../../lib/utils";
import { Button, Input, Tag } from "../ui";
import type { EsimProfileItem } from "./types";
import type { DeviceType } from "../../types";
import { useI18n } from "../../lib/i18n";
import { esimMutationDisabled } from "./esimActionState";
import { useNavigate } from "react-router-dom";

export interface EsimProfileRowProps {
  deviceId: string;
  hostType?: DeviceType;
  eid?: string;
  deviceOnline: boolean;
  profilesStale: boolean;
  aidHex?: string;
  profile: EsimProfileItem;
  showSensitive: boolean;
  renaming: boolean;
  renameValue: string;
  switching: boolean;
  deleting: boolean;
  onRenameValueChange: (v: string) => void;
  onSwitch: () => void;
  onStartRename: () => void;
  onSubmitRename: () => void;
  onCancelRename: () => void;
  onDelete: () => void;
}

export function EsimProfileRow(props: EsimProfileRowProps) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const { profile: p, renaming } = props;
  const active = p.state === 1;
  const mutationDisabled = esimMutationDisabled({
    deviceOnline: props.deviceOnline,
    profilesStale: props.profilesStale,
  });
  return (
    <>
      <div className="flex items-center justify-between gap-3 px-4 py-3 transition-colors hover:bg-gray-50/50 dark:hover:bg-white/5">
        <div className="min-w-0 flex-1">
          {!renaming ? (
            <>
              <div className="flex items-center gap-2">
                <span className={cx("h-2 w-2 flex-shrink-0 rounded-full", active ? "bg-green-500" : "bg-gray-300 dark:bg-gray-600")} />
                <span className="truncate text-sm font-medium text-gray-900 dark:text-white">{p.name || p.iccid}</span>
                <Tag type={active ? "success" : "info"} className="flex-shrink-0">
                  {p.stateText}
                </Tag>
              </div>
              <div className="ml-4 mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-gray-500 transition-all dark:text-gray-400">
                <span>{p.serviceProviderName}</span>
                <span className={cx(!props.showSensitive && "select-none blur-sm")}>{p.iccid}</span>
              </div>
            </>
          ) : (
            <div className="flex items-center gap-2">
              <Input
                inputSize="default"
                value={props.renameValue}
                onChange={(e) => props.onRenameValueChange(e.target.value)}
                placeholder={t("输入新名称")}
                autoFocus
                className="!w-52"
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !mutationDisabled) props.onSubmitRename();
                  else if (e.key === "Escape") props.onCancelRename();
                }}
              />
              <Button variant="primary" size="small" disabled={mutationDisabled} onClick={props.onSubmitRename} className="!border-0">
                {t("保存")}
              </Button>
              <Button size="small" onClick={props.onCancelRename} className="!border-0">
                {t("取消")}
              </Button>
            </div>
          )}
        </div>
        {!renaming ? (
          <div className="flex flex-shrink-0 items-center gap-2">
            <Button variant={active ? "warning" : "success"} size="small" plain loading={props.switching} disabled={mutationDisabled} onClick={props.onSwitch}>
              {active ? t("禁用") : t("切换")}
            </Button>
            <Button variant="primary" size="small" plain disabled={mutationDisabled} onClick={props.onStartRename}>
              {t("改名")}
            </Button>
            <Button
              variant="default"
              size="small"
              plain
              onClick={() => {
                const params = new URLSearchParams({
                  new: p.iccid,
                  cardType: "esim_profile",
                  deviceId: props.deviceId,
                });
                if (props.hostType) params.set("hostType", props.hostType);
                if (props.eid) params.set("eid", props.eid);
                if (p.aidHex) params.set("profileAid", p.aidHex);
                if (p.name || p.serviceProviderName) {
                  params.set("displayName", p.name || p.serviceProviderName || "");
                }
                navigate(`/cards?${params.toString()}`);
              }}
            >
              {t("查看卡片")}
            </Button>
            <span title={active ? t("当前启用的 Profile 不能删除；请先切换到另一张卡") : undefined}>
              <Button variant="danger" size="small" plain loading={props.deleting} disabled={active || mutationDisabled} onClick={props.onDelete}>
                {t("删除")}
              </Button>
            </span>
          </div>
        ) : null}
      </div>
    </>
  );
}
