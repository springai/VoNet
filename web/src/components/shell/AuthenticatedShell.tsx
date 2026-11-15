import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import {
  BoardRegular, CardUiRegular, PhoneRegular,
  DocumentTextRegular,
  GlobeRegular,
  MailRegular,
  SendClockRegular,
  PanelLeftContractRegular,
  PanelLeftExpandRegular,
  RouterRegular,
  PlugConnectedRegular,
  PulseRegular, SettingsRegular,
  SignOutRegular,
} from "@fluentui/react-icons";
import { useAuth } from "../../store/auth";
import { useI18n } from "../../lib/i18n";
import { confirmDialog } from "../ui/MessageBox";
import { LanguageSwitch } from "../ui/LanguageSwitch";
import { SwitchDark } from "../ui/SwitchDark";
import { Drawer } from "../ui/Drawer";
import { ErrorBoundary } from "../ui/ErrorBoundary";
import { cx } from "../../lib/utils";
import { BrandLogo } from "./BrandLogo";
import { VersionBadge } from "./VersionBadge";
import { listPlugins, type InstalledPlugin } from "../../extensions";
import { api } from "../../api";
import type { SystemInfo } from "../../types";
import { IncomingCallMonitor } from "../call/IncomingCallMonitor";

type SidebarNavItem = {
  to: string;
  label: string;
  icon: typeof GlobeRegular;
  end?: boolean;
  pluginLabelZH?: string;
};

type SidebarNavGroup = {
  key: string;
  label: string;
  items: SidebarNavItem[];
};

const NAV_GROUPS: SidebarNavGroup[] = [
  {
    key: "monitoring",
    label: "监控",
    items: [
      { to: "/", label: "仪表盘", icon: BoardRegular, end: true },
      { to: "/diagnostics", label: "系统诊断", icon: PulseRegular },
    ],
  },
  {
    key: "cards",
    label: "卡片",
    items: [
      { to: "/cards", label: "SIM 管理", icon: CardUiRegular },
      { to: "/call", label: "通话", icon: PhoneRegular },
      { to: "/sms", label: "短信", icon: MailRegular },
    ],
  },
  {
    key: "devices",
    label: "设备",
    items: [
      { to: "/devices", label: "设备管理", icon: RouterRegular },
      { to: "/imei-pool", label: "IMEI 池", icon: PulseRegular },
    ],
  },
  {
    key: "network",
    label: "网络",
    items: [
      { to: "/proxy", label: "代理中心", icon: GlobeRegular },
      { to: "/node-adapters", label: "节点转换", icon: PlugConnectedRegular },
    ],
  },
  {
    key: "operations",
    label: "运维",
    items: [
      { to: "/automatic-tasks", label: "自动任务", icon: SendClockRegular },
      { to: "/logs", label: "实时日志", icon: DocumentTextRegular },
    ],
  },
  {
    key: "system",
    label: "系统",
    items: [{ to: "/settings", label: "系统设置", icon: SettingsRegular }],
  },
];

export function AuthenticatedShell({
  isDark,
  onToggleTheme,
}: {
  isDark: boolean;
  onToggleTheme: () => void;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [isMobile, setIsMobile] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [plugins, setPlugins] = useState<InstalledPlugin[]>([]);
  const [developer, setDeveloper] = useState(false);
  const { logout, user } = useAuth();
  const { t, lang } = useI18n();
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    let active = true;
    const load = () => api<SystemInfo>("/system/info").then((info) => {
      if (active) setDeveloper(!!info.developer);
    }).catch(() => {
      if (active) setDeveloper(false);
    });
    void load();
    const timer = window.setInterval(load, 10_000);
    return () => { active = false; window.clearInterval(timer); };
  }, []);

  useEffect(() => {
    const mq = window.matchMedia("(max-width: 767px)");
    const update = () => {
      setIsMobile(mq.matches);
      if (!mq.matches) setMobileOpen(false);
    };
    update();
    window.addEventListener("resize", update, { passive: true });
    return () => window.removeEventListener("resize", update);
  }, []);

  useEffect(() => {
    let active = true;
    const load = () => listPlugins().then((items) => {
      if (active) setPlugins(items || []);
    }).catch(() => undefined);
    void load();
    window.addEventListener("vonet:plugins-changed", load);
    return () => { active = false; window.removeEventListener("vonet:plugins-changed", load); };
  }, []);

  useEffect(() => {
    setMobileOpen(false);
  }, [location.pathname]);

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

  function toggle() {
    if (isMobile) setMobileOpen(true);
    else setCollapsed((value) => !value);
  }

  const currentNavItem = NAV_GROUPS
    .flatMap((group) => group.items)
    .slice()
    .sort((a, b) => b.to.length - a.to.length)
    .find((item) =>
      item.to === location.pathname ||
      (!item.end && item.to !== "/" && location.pathname.startsWith(`${item.to}/`)),
    );
  const currentLabel = currentNavItem
    ? t(currentNavItem.label)
    : location.pathname.startsWith("/extensions")
      ? t("扩展")
      : t("控制台");

  function menuList(collapse: boolean) {
    const sidebarPlugins = plugins
      .filter((plugin) => plugin.enabled)
      .flatMap((plugin) => plugin.contributions
        .filter((contribution) => contribution.location === "sidebar")
        .map((contribution) => ({ plugin, contribution })));
    const groups = NAV_GROUPS.map((group) => ({
      ...group,
      items: [...group.items],
    }));

    if (developer) {
      const networkGroup = groups.find((group) => group.key === "network");
      networkGroup?.items.splice(1, 0, {
        to: "/export-proxy",
        label: "导出代理",
        icon: GlobeRegular,
      });
    }

    for (const extension of sidebarPlugins) {
      const itemKey = extension.contribution.after?.replace(/^\//, "") || "sms";
      const targetGroup = groups.find((group) => group.items.some((item) => (item.to.replace(/^\//, "") || "dashboard") === itemKey));
      if (!targetGroup) continue;
      targetGroup.items.push({
          to: `/extensions/${encodeURIComponent(extension.plugin.id)}/${encodeURIComponent(extension.contribution.id)}`,
          label: extension.contribution.label,
          pluginLabelZH: extension.contribution.labelZh,
          icon: GlobeRegular,
      });
    }

    return (
      <nav className={cx("sidebar-menu mt-2", collapse && "is-collapsed")} aria-label={t("主导航")}>
        {groups.map((group) => (
          <section className={cx("sidebar-menu-group", collapse && "is-collapsed")} key={group.key}>
            <div className="sidebar-menu-group-label" title={collapse ? t(group.label) : undefined}>
              <span className="sidebar-menu-group-marker" aria-hidden="true" />
              {!collapse && <span>{t(group.label)}</span>}
            </div>
            <div className="sidebar-menu-group-items">
              {group.items.map((item) => {
                const Icon = item.icon;
                const label = item.pluginLabelZH
                  ? (lang === "zh" ? item.pluginLabelZH : item.label)
                  : t(item.label);
                return (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    end={item.end}
                    title={collapse ? label : undefined}
                    className={({ isActive }) => cx("vonet-menu-item", isActive && "is-active")}
                  >
                    <span className="vonet-menu-icon">
                      <Icon />
                    </span>
                    <span className="sidebar-menu-label">{label}</span>
                  </NavLink>
                );
              })}
            </div>
          </section>
        ))}
      </nav>
    );
  }

  function userCard() {
    return (
      <div className="reborn-user-card">
        <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-indigo-50 text-indigo-600 dark:bg-indigo-500/10 dark:text-indigo-300">
          <SettingsRegular className="h-[18px] w-[18px]" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-bold">{user?.username || "Admin"}</div>
          <div className="truncate text-xs text-gray-400">{user?.role || "Administrator"}</div>
        </div>
        <button
          type="button"
          onClick={onLogout}
          aria-label={t("退出登录")}
          className="rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-red-50 hover:text-red-500 dark:hover:bg-red-500/10"
        >
          <SignOutRegular className="h-[18px] w-[18px]" />
        </button>
      </div>
    );
  }

  return (
    <div className="flex h-full">
      <IncomingCallMonitor />
      {!isMobile && (
        <aside
          className={cx(
            "reborn-sidebar relative h-full transition-[width] duration-200",
            collapsed ? "w-[64px]" : "w-[260px]",
          )}
        >
          <div className={cx("flex h-20 items-center gap-3 px-4", collapsed && "justify-center px-0")}>
            <BrandLogo className={cx("sidebar-brand-logo", collapsed && "is-collapsed")} />
            {!collapsed && (
              <div className="min-w-0">
                <div className="sidebar-brand-title">VoNet</div>
                <div className="sidebar-brand-description">MODEM · PC/SC · PANEL</div>
              </div>
            )}
          </div>
          {menuList(collapsed)}
          {!collapsed && <div className="absolute bottom-4 w-full px-3">{userCard()}</div>}
        </aside>
      )}

      <Drawer open={isMobile && mobileOpen} onClose={() => setMobileOpen(false)} className="mobile-drawer">
        <div className="reborn-sidebar reborn-mobile-sidebar relative h-full">
          <div className="flex h-20 items-center gap-3 px-4">
            <BrandLogo className="sidebar-brand-logo" />
            <div className="min-w-0">
              <div className="sidebar-brand-title">VoNet</div>
              <div className="sidebar-brand-description">MODEM · PC/SC · PANEL</div>
            </div>
          </div>
          {menuList(false)}
          <div className="absolute bottom-4 w-full px-3">{userCard()}</div>
        </div>
      </Drawer>

      <div className="flex h-full min-w-0 flex-1 flex-col">
        <header className="reborn-topbar">
          <div className="reborn-topbar-left">
            <button
              type="button"
              onClick={toggle}
              aria-label={collapsed ? t("展开侧栏") : t("收起侧栏")}
              className="reborn-icon-button"
            >
              {!isMobile && !collapsed ? (
                <PanelLeftContractRegular className="h-5 w-5" />
              ) : (
                <PanelLeftExpandRegular className="h-5 w-5" />
              )}
            </button>
            <div className="reborn-topbar-context">
              <span>WORKSPACE</span>
              <strong>{currentLabel}</strong>
            </div>
          </div>
          <div className="reborn-topbar-actions">
            <VersionBadge />
            <LanguageSwitch />
            <SwitchDark isDark={isDark} onToggle={onToggleTheme} />
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
    </div>
  );
}
