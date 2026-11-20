import { useEffect, useRef } from "react";
import { NavLink, useLocation } from "react-router-dom";
import {
  ArrowDownloadRegular,
  CardUiRegular,
  DocumentTextRegular,
  GlobeRegular,
  PulseRegular,
  RouterRegular,
  SendClockRegular,
  SettingsRegular,
  PlugConnectedRegular,
} from "@fluentui/react-icons";
import { useI18n } from "../../lib/i18n";
import { cx } from "../../lib/utils";

type NavigationIcon = typeof GlobeRegular;

type SectionItem = {
  to: string;
  label: string;
  icon: NavigationIcon;
};

const DEVICE_ITEMS: SectionItem[] = [
  { to: "/cards", label: "SIM 管理", icon: CardUiRegular },
  { to: "/devices", label: "设备管理", icon: RouterRegular },
];

const SETTINGS_ITEMS: SectionItem[] = [
  { to: "/settings", label: "系统设置", icon: SettingsRegular },
  { to: "/diagnostics", label: "系统诊断", icon: PulseRegular },
  { to: "/logs", label: "实时日志", icon: DocumentTextRegular },
  { to: "/node-adapters", label: "节点转换", icon: PlugConnectedRegular },
  { to: "/automatic-tasks", label: "自动化任务", icon: SendClockRegular },
  { to: "/imei-pool", label: "IMEI 池", icon: PulseRegular },
  { to: "/proxy", label: "代理中心", icon: GlobeRegular },
  { to: "/export-proxy", label: "导出代理", icon: ArrowDownloadRegular },
];

function itemsForPath(pathname: string) {
  if (DEVICE_ITEMS.some((item) => pathname === item.to || pathname.startsWith(`${item.to}/`))) return DEVICE_ITEMS;
  if (SETTINGS_ITEMS.some((item) => pathname === item.to || pathname.startsWith(`${item.to}/`))) return SETTINGS_ITEMS;
  return null;
}

export function WorkspaceSectionNav() {
  const { pathname } = useLocation();
  const { t } = useI18n();
  const navRef = useRef<HTMLElement | null>(null);
  const items = itemsForPath(pathname);

  useEffect(() => {
    const frame = window.requestAnimationFrame(() => {
      navRef.current
        ?.querySelector<HTMLElement>(".workspace-section-tab.is-active")
        ?.scrollIntoView({ behavior: "smooth", block: "nearest", inline: "center" });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [pathname, items]);

  if (!items) return null;

  return (
    <nav ref={navRef} className="workspace-section-tabs" aria-label={t("页面分类")}>
      {items.map((item) => {
        const Icon = item.icon;
        return (
          <NavLink
            key={item.to}
            to={item.to}
            className={({ isActive }) => cx("workspace-section-tab", isActive && "is-active")}
          >
            <Icon />
            <span>{t(item.label)}</span>
          </NavLink>
        );
      })}
    </nav>
  );
}
