import { useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import {
  AppsRegular,
  ArrowDownloadRegular,
  BoardRegular,
  CardUiRegular,
  DocumentTextRegular,
  GlobeRegular,
  MailRegular,
  PhoneRegular,
  PlugConnectedRegular,
  PulseRegular,
  RouterRegular,
  SendClockRegular,
  SettingsRegular,
  SignOutRegular,
} from "@fluentui/react-icons";
import { useAuth } from "../../store/auth";
import { useI18n } from "../../lib/i18n";
import { confirmDialog } from "../ui/MessageBox";
import { LanguageSwitch } from "../ui/LanguageSwitch";
import { SwitchDark } from "../ui/SwitchDark";
import { ErrorBoundary } from "../ui/ErrorBoundary";
import { cx } from "../../lib/utils";
import { BrandLogo } from "./BrandLogo";
import { VersionBadge } from "./VersionBadge";
import { listPlugins, type InstalledPlugin } from "../../extensions";
import { GlobalCallWidget } from "../call/GlobalCallWidget";

type NavigationIcon = typeof GlobeRegular;

type NavigationItem = {
  to: string;
  label: string;
  icon: NavigationIcon;
  end?: boolean;
  pluginLabelZH?: string;
};

type PluginNavigationItem = NavigationItem & { after: string };

type DockSection = {
  key: string;
  label: string;
  icon: NavigationIcon;
  to?: string;
  end?: boolean;
  items?: NavigationItem[];
};

const DEVICE_ITEMS: NavigationItem[] = [
  { to: "/cards", label: "SIM 管理", icon: CardUiRegular },
  { to: "/devices", label: "设备管理", icon: RouterRegular },
];

const SETTINGS_ITEMS: NavigationItem[] = [
  { to: "/settings", label: "系统设置", icon: SettingsRegular },
  { to: "/diagnostics", label: "系统诊断", icon: PulseRegular },
  { to: "/logs", label: "实时日志", icon: DocumentTextRegular },
  { to: "/node-adapters", label: "节点转换", icon: PlugConnectedRegular },
  { to: "/automatic-tasks", label: "自动化任务", icon: SendClockRegular },
  { to: "/imei-pool", label: "IMEI 池", icon: PulseRegular },
  { to: "/proxy", label: "代理中心", icon: GlobeRegular },
  { to: "/export-proxy", label: "导出代理", icon: ArrowDownloadRegular },
];

const BASE_DOCK_SECTIONS: DockSection[] = [
  { key: "monitoring", label: "监控", icon: BoardRegular, to: "/", end: true },
  { key: "devices", label: "设备", icon: AppsRegular, items: DEVICE_ITEMS },
  { key: "sms", label: "短信", icon: MailRegular, to: "/sms" },
  { key: "call", label: "通话", icon: PhoneRegular, to: "/call" },
  { key: "settings", label: "设置", icon: SettingsRegular, items: SETTINGS_ITEMS },
];

function isItemActive(item: NavigationItem, pathname: string) {
  return item.end
    ? pathname === item.to
    : pathname === item.to || pathname.startsWith(`${item.to}/`);
}

function isSectionActive(section: DockSection, pathname: string) {
  if (section.to) {
    return isItemActive({ to: section.to, label: section.label, icon: section.icon, end: section.end }, pathname);
  }
  return section.items?.some((item) => isItemActive(item, pathname)) || false;
}

function sectionTarget(section: DockSection) {
  return section.to || section.items?.[0]?.to || "/";
}

export function AuthenticatedShell({
  isDark,
  onToggleTheme,
}: {
  isDark: boolean;
  onToggleTheme: () => void;
}) {
  const [plugins, setPlugins] = useState<InstalledPlugin[]>([]);
  const { logout, user } = useAuth();
  const { t, lang } = useI18n();
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    let active = true;
    const load = () => listPlugins().then((items) => {
      if (active) setPlugins(items || []);
    }).catch(() => undefined);
    void load();
    window.addEventListener("vonet:plugins-changed", load);
    return () => {
      active = false;
      window.removeEventListener("vonet:plugins-changed", load);
    };
  }, []);

  async function onLogout() {
    const ok = await confirmDialog(t("确认退出登录？"), t("提示"), {
      confirmText: t("退出"),
      cancelText: t("取消"),
      type: "warning",
    });
    if (ok) {
      await logout();
      navigate("/login");
    }
  }

  const pluginItems: PluginNavigationItem[] = plugins
    .filter((plugin) => plugin.enabled)
    .flatMap((plugin) => plugin.contributions
      .filter((contribution) => contribution.location === "sidebar")
      .map((contribution) => ({
        to: `/extensions/${encodeURIComponent(plugin.id)}/${encodeURIComponent(contribution.id)}`,
        label: contribution.label,
        pluginLabelZH: contribution.labelZh,
        icon: GlobeRegular,
        after: contribution.after || "",
      })));

  const deviceItems = [...DEVICE_ITEMS];
  const settingsItems = [...SETTINGS_ITEMS];
  const deviceTargets = new Set(DEVICE_ITEMS.map((item) => item.to));
  for (const item of pluginItems) {
    if (deviceTargets.has(item.after)) deviceItems.push(item);
    else settingsItems.push(item);
  }

  const dockSections: DockSection[] = BASE_DOCK_SECTIONS.map((section) => {
    if (section.key === "devices") return { ...section, items: deviceItems };
    if (section.key === "settings") return { ...section, items: settingsItems };
    return section;
  });

  const allItems: NavigationItem[] = dockSections.flatMap((section) => (
    section.items || (section.to ? [{
      to: section.to,
      label: section.label,
      icon: section.icon,
      end: section.end,
    }] : [])
  ));
  const currentNavItem = [...allItems]
    .sort((a, b) => b.to.length - a.to.length)
    .find((item) => isItemActive(item, location.pathname));
  const currentLabel = currentNavItem
    ? (currentNavItem.pluginLabelZH && lang === "zh" ? currentNavItem.pluginLabelZH : t(currentNavItem.label))
    : location.pathname.startsWith("/extensions")
      ? t("扩展")
      : t("控制台");

  const userLabel = user?.username || "Admin";

  return (
    <div className="app-shell-frame">
      <GlobalCallWidget />

      <div className="app-shell-main">
        <header className="reborn-topbar">
          <div className="reborn-topbar-left">
            <div className="topbar-mobile-brand">
              <BrandLogo className="topbar-brand-logo" />
              <div className="reborn-topbar-brand">
                <strong>VoNet</strong>
                <span>MODEM · PC/SC · PANEL</span>
              </div>
            </div>
            <div className="reborn-topbar-context">
              <span>WORKSPACE</span>
              <strong>{currentLabel}</strong>
            </div>
          </div>
          <div className="reborn-topbar-actions">
            <VersionBadge />
            <LanguageSwitch />
            <SwitchDark isDark={isDark} onToggle={onToggleTheme} />
            <div className="reborn-user-chip" title={userLabel}>
              <span className="reborn-user-avatar" aria-hidden="true">{userLabel.slice(0, 1).toUpperCase()}</span>
              <span className="reborn-user-name">{userLabel}</span>
              <button
                type="button"
                onClick={onLogout}
                aria-label={t("退出登录")}
                title={t("退出登录")}
                className="reborn-user-action"
              >
                <SignOutRegular />
              </button>
            </div>
          </div>
        </header>

        <main className="reborn-main">
          <div className="reborn-content">
            <ErrorBoundary title={t("页面渲染失败")}>
              <Outlet />
            </ErrorBoundary>
          </div>
        </main>
      </div>

      <div className="bottom-dock-layer">
        <nav className="bottom-dock-shell" aria-label={t("主导航")}>
          <div className="bottom-dock">
            {dockSections.map((section) => {
              const Icon = section.icon;
              const active = isSectionActive(section, location.pathname);
              return (
                <button
                  key={section.key}
                  type="button"
                  className={cx("bottom-dock-button", active && "is-active")}
                  aria-current={active ? "page" : undefined}
                  title={t(section.label)}
                  onClick={() => navigate(sectionTarget(section))}
                >
                  <span className="bottom-dock-icon"><Icon /></span>
                  <span className="bottom-dock-label">{t(section.label)}</span>
                </button>
              );
            })}
          </div>
        </nav>
      </div>
    </div>
  );
}
