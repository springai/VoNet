import { useCallback, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api";
import type { DashboardDevice, DashboardHost, DashboardUpcomingTask, SimCard } from "../types";
import { usePolling } from "../lib/usePolling";
import { useI18n } from "../lib/i18n";
import { PageHeader } from "../components/ui/PageHeader";
import { RefreshButton } from "../components/ui/RefreshButton";
import { ErrorState } from "../components/ui/ErrorState";
import { ListSkeleton } from "../components/ui/ListSkeleton";
import { EmptyState } from "../components/ui/EmptyState";
import { DeviceCard } from "../components/DeviceCard";
import { HostInfoCard } from "../components/dashboard/HostInfoCard";
import { HostPerfCard } from "../components/dashboard/HostPerfCard";
import { UpcomingTasksCard } from "../components/dashboard/UpcomingTasksCard";
import { OnlineRateCard } from "../components/dashboard/OnlineRateCard";
import { DashboardCardCard } from "../components/dashboard/DashboardCardCard";
import { buildDashboardCardViews, moduleOnlineRate } from "../components/dashboard/dashboardModels";
import { Tabs } from "../components/ui/Tabs";
import { SensitiveInfoButton } from "../components/ui/SensitiveInfoButton";
import { listCards } from "../components/cards/cardApi";

interface LoadError { message: string; status?: number; method?: string; url?: string }

// 任务卡只展示最近的三条；定时任务变化慢，轮询间隔比设备/性能数据更宽。
const UPCOMING_TASK_COUNT = 3;
const TASKS_POLL_INTERVAL = 15000;

export default function DashboardPage() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [devices, setDevices] = useState<DashboardDevice[]>([]);
  const [devicesLoading, setDevicesLoading] = useState(false);
  const [devicesError, setDevicesError] = useState<LoadError | null>(null);
  const [devicesOkAt, setDevicesOkAt] = useState<number | null>(null);
  const [cards, setCards] = useState<SimCard[]>([]);
  const [cardsLoading, setCardsLoading] = useState(false);
  const [cardsError, setCardsError] = useState<LoadError | null>(null);
  const [dashboardTab, setDashboardTab] = useState<"devices" | "cards">("devices");
  const [host, setHost] = useState<DashboardHost | null>(null);
  const [upcomingTasks, setUpcomingTasks] = useState<DashboardUpcomingTask[]>([]);

  const fetchDevices = useCallback(async () => {
    setDevicesLoading(true);
    try {
      const list = await api<DashboardDevice[]>("/dashboard/devices");
      setDevices(list || []);
      setDevicesError(null);
      setDevicesOkAt(Date.now());
    } catch (e: any) {
      setDevicesError({ message: e?.message || t("加载失败"), status: e?.status });
    } finally {
      setDevicesLoading(false);
    }
  }, []);

  const fetchCards = useCallback(async () => {
    setCardsLoading(true);
    try {
      setCards(await listCards());
      setCardsError(null);
    } catch (e: any) {
      setCardsError({ message: e?.message || t("加载失败"), status: e?.status });
    } finally {
      setCardsLoading(false);
    }
  }, [t]);

  // 宿主机信息 + 性能数据：2s 轮询让网络速率足够"实时"。
  const fetchHost = useCallback(async () => {
    try {
      setHost(await api<DashboardHost>("/dashboard/host"));
    } catch {
      /* 宿主机数据失败不打断设备监控；保留上一次成功值。 */
    }
  }, []);

  const fetchUpcomingTasks = useCallback(async () => {
    try {
      const data = await api<{ tasks?: DashboardUpcomingTask[] }>("/automatic-tasks");
      const upcoming = (data.tasks || [])
        .filter((task) => task.enabled && task.nextRunAt && !task.nextRunAt.startsWith("0001-"))
        .sort((a, b) => new Date(a.nextRunAt).getTime() - new Date(b.nextRunAt).getTime())
        .slice(0, UPCOMING_TASK_COUNT);
      setUpcomingTasks(upcoming);
    } catch {
      /* 任务列表加载失败时保留旧数据。 */
    }
  }, []);

  usePolling(fetchDevices, 5000);
  usePolling(fetchCards, 5000);
  usePolling(fetchHost, 2000);
  usePolling(fetchUpcomingTasks, TASKS_POLL_INTERVAL);

  const moduleRate = useMemo(() => moduleOnlineRate(devices), [devices]);
  const dashboardCards = useMemo(() => buildDashboardCardViews(cards, devices), [cards, devices]);
  const openDevice = (id: string) => navigate(`/devices?device=${encodeURIComponent(id)}&tab=config`);
  const openCard = (iccid: string) => navigate(`/cards?iccid=${encodeURIComponent(iccid)}&tab=overview`);
  const dashboardTabs = [
    { key: "devices", label: t("设备列表") },
    { key: "cards", label: t("卡片列表") },
  ];

  return (
    <div className="dashboard-page">
      <PageHeader
        title={t("设备监控")}
        subtitle={t("实时监测模组检测状态与出口连通性")}
        actions={<RefreshButton loading={devicesLoading} onClick={fetchDevices} />}
      />
      <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        <HostInfoCard info={host?.host} />
        <HostPerfCard perf={host?.perf} />
        <UpcomingTasksCard tasks={upcomingTasks} />
        <OnlineRateCard online={moduleRate.online} total={moduleRate.total} />
      </div>
      <Tabs
        className="mb-5"
        tabs={dashboardTabs}
        value={dashboardTab}
        onChange={(key) => setDashboardTab(key === "cards" ? "cards" : "devices")}
      />
      {dashboardTab === "devices" ? (
        <>
          {devicesError ? (
            <ErrorState className="mb-6" title={t("设备列表加载失败")} message={devicesError.message} statusCode={devicesError.status} requestMethod={devicesError.method} requestUrl={devicesError.url} lastSuccessAt={devicesOkAt} retryText={t("重试")} onRetry={fetchDevices} />
          ) : null}
          {devicesLoading && devices.length === 0 ? (
            <ListSkeleton rows={10} />
          ) : devices.length === 0 ? (
            <EmptyState title={t("暂无设备接入")} subtitle={t("请先在设备管理中添加或接管设备")} />
          ) : (
            <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
              {devices.map((device) => (
                <DeviceCard key={device.id} device={device} onOpen={openDevice} />
              ))}
            </div>
          )}
        </>
      ) : (
        <>
          {cardsError ? (
            <ErrorState className="mb-6" title={t("卡片列表加载失败")} message={cardsError.message} statusCode={cardsError.status} requestMethod={cardsError.method} requestUrl={cardsError.url} retryText={t("重试")} onRetry={fetchCards} />
          ) : null}
          {cardsLoading && cards.length === 0 ? (
            <ListSkeleton rows={10} />
          ) : dashboardCards.length === 0 ? (
            <EmptyState title={t("暂无卡片")} subtitle={t("请在 SIM 管理中保存当前识别到的卡片")} />
          ) : (
            <>
              <div className="mb-3 flex justify-end">
                <SensitiveInfoButton showLabel />
              </div>
              <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
                {dashboardCards.map((item) => (
                  <DashboardCardCard key={item.card.iccid} item={item} onOpen={openCard} />
                ))}
              </div>
            </>
          )}
        </>
      )}
    </div>
  );
}
