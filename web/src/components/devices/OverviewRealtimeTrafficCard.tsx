import { useEffect, useMemo, useState } from "react";
import { PulseRegular } from "@fluentui/react-icons";
import { EChart } from "../EChart";
import { api } from "../../api";
import { formatBytes } from "../../lib/utils";
import { useI18n } from "../../lib/i18n";
import type { DeviceDetail } from "./types";

interface TrafficPoint {
  rx: number;
  tx: number;
}

interface Props {
  device: DeviceDetail;
}

function rate(value: number) {
  return `${formatBytes(Math.max(0, value))}/s`;
}

export function OverviewRealtimeTrafficCard({ device }: Props) {
  const { t } = useI18n();
  const [points, setPoints] = useState<TrafficPoint[]>([]);
  const [current, setCurrent] = useState<TrafficPoint>(() => ({
    rx: Number(device.trafficRaw?.rate || 0),
    tx: Number(device.trafficRaw?.rate_tx || 0),
  }));

  useEffect(() => {
    let cancelled = false;
    const sample = async () => {
      try {
        const result = await api<{ devices?: DeviceDetail[] }>(`/devices/${encodeURIComponent(device.id)}/overview`);
        const overview = result.devices?.[0];
        if (cancelled || !overview) return;
        const next = {
          rx: Math.max(0, Number(overview.trafficRaw?.rate || 0)),
          tx: Math.max(0, Number(overview.trafficRaw?.rate_tx || 0)),
        };
        setCurrent(next);
        setPoints((previous) => [...previous, next].slice(-60));
      } catch {
        // Keep the last sample visible when a live overview request is transiently unavailable.
      }
    };
    void sample();
    const timer = window.setInterval(() => void sample(), 1000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [device.id]);

  const chart = useMemo(() => ({
    animationDuration: 240,
    color: ["#2a78d6", "#23a05d"],
    grid: { top: 14, right: 8, bottom: 22, left: 38 },
    xAxis: {
      type: "category",
      boundaryGap: false,
      data: Array.from({ length: Math.max(points.length, 2) }, (_, index) => (
        index === Math.max(points.length, 2) - 1 ? t("现在") : ""
      )),
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: { color: "#94a3b8", fontSize: 10 },
    },
    yAxis: {
      type: "value",
      min: 0,
      axisLabel: { color: "#94a3b8", fontSize: 10, formatter: (value: number) => formatBytes(value) },
      splitLine: { lineStyle: { color: "rgba(148, 163, 184, 0.16)" } },
    },
    series: [
      { name: t("下行"), type: "line", smooth: true, showSymbol: false, data: points.map((point) => point.rx), lineStyle: { width: 2 } },
      { name: t("上行"), type: "line", smooth: true, showSymbol: false, data: points.map((point) => point.tx), lineStyle: { width: 2 } },
    ],
  }), [points, t]);

  return (
    <section className="overview-realtime-card">
      <div className="overview-panel-heading">
        <PulseRegular />
        <span>{t("实时流量")}</span>
        <small>{t("最近 60 秒 · 每秒")}</small>
      </div>
      <div className="overview-realtime-values">
        <div><span>{t("下行")}</span><strong>{rate(current.rx)}</strong></div>
        <div><span>{t("上行")}</span><strong>{rate(current.tx)}</strong></div>
      </div>
      <EChart option={chart} className="overview-realtime-chart" />
      <div className="overview-realtime-foot">
        <span>{t("接口累计 下行")} {device.traffic?.rx || "--"}</span>
        <span>{t("上行")} {device.traffic?.tx || "--"}</span>
      </div>
    </section>
  );
}
