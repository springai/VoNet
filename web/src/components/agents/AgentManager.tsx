import { useCallback, useEffect, useState, type ReactNode } from "react";
import { AddRegular, ArrowClockwiseRegular, DeleteRegular, EditRegular, KeyRegular, RouterRegular, SaveRegular } from "@fluentui/react-icons";
import { api, apiMessage } from "../../api";
import type { VoNetAgent } from "../../types";
import { Button, Input, Modal, Select, Tag, Textarea, confirmDialog, message } from "../ui";
import { FieldRow } from "../devices/FieldRow";
import { formatDateTime } from "../../lib/utils";
import { tf, useI18n } from "../../lib/i18n";

interface Props {
  selectedAgentId: string;
  onSelect: (id: string) => void;
  children?: ReactNode;
}

export function AgentManager({ selectedAgentId, onSelect, children }: Props) {
  const { t } = useI18n();
  const [agents, setAgents] = useState<VoNetAgent[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [rotatingId, setRotatingId] = useState("");
  const [deletingId, setDeletingId] = useState("");
  const [editingAgent, setEditingAgent] = useState<VoNetAgent | null>(null);
  const [editName, setEditName] = useState("");
  const [editNotes, setEditNotes] = useState("");
  const [savingEdit, setSavingEdit] = useState(false);
  const [name, setName] = useState("");
  const [notes, setNotes] = useState("");
  const [issuedKey, setIssuedKey] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await api<{ agents?: VoNetAgent[] }>("/agents");
      setAgents(data.agents || []);
    } catch (error) {
      message.error(apiMessage(error) || t("加载 VoNet-Agent 失败"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  async function createAgent() {
    if (!name.trim()) return message.warning(t("请填写 Agent 名称"));
    setSubmitting(true);
    try {
      const result = await api<{ agent: VoNetAgent; communicationKey: string }>("/agents", {
        method: "POST",
        body: { name, notes },
      });
      setIssuedKey(result.communicationKey);
      setCreating(false);
      setName("");
      setNotes("");
      await load();
    } catch (error) {
      message.error(apiMessage(error) || t("创建 VoNet-Agent 失败"));
    } finally {
      setSubmitting(false);
    }
  }

  async function rotateKey(id: string) {
    setRotatingId(id);
    try {
      const result = await api<{ agent: VoNetAgent; communicationKey: string }>(
        `/agents/${encodeURIComponent(id)}/rotate-key`,
        { method: "POST" },
      );
      setIssuedKey(result.communicationKey);
      await load();
    } catch (error) {
      message.error(apiMessage(error) || t("换发通信密钥失败"));
    } finally {
      setRotatingId("");
    }
  }

  async function confirmRotateKey(agent: VoNetAgent) {
    const name = agent.name || agent.id;
    const confirmed = await confirmDialog(
      tf("换发 {name} 的通信密钥后，旧密钥将立即失效，Agent 会断开连接并保持离线，直到使用新密钥重新入网。确定继续吗？", { name }),
      t("确认换发通信密钥"),
      {
        type: "danger",
        confirmVariant: "danger",
        confirmText: t("确认换发"),
        cancelText: t("取消"),
      },
    );
    if (confirmed) await rotateKey(agent.id);
  }

  async function updateAgent() {
    if (!editingAgent || !editName.trim()) return;
    setSavingEdit(true);
    try {
      await api(`/agents/${encodeURIComponent(editingAgent.id)}`, {
        method: "PATCH",
        body: { name: editName.trim(), notes: editNotes.trim() },
      });
      setEditingAgent(null);
      await load();
      message.success(t("Agent 信息已更新"));
    } catch (error) {
      message.error(apiMessage(error) || t("更新 Agent 失败"));
    } finally {
      setSavingEdit(false);
    }
  }

  async function confirmDeleteAgent(agent: VoNetAgent) {
    const name = agent.name || agent.id;
    const confirmed = await confirmDialog(
      tf("删除 {name} 后，其通信密钥和证书将失效，关联设备配置及运行时绑定也会删除。已保存的 SIM 卡不会删除。确定继续吗？", { name }),
      t("确认删除 Agent"),
      {
        type: "danger",
        confirmVariant: "danger",
        confirmText: t("删除 Agent"),
        cancelText: t("取消"),
      },
    );
    if (!confirmed) return;
    setDeletingId(agent.id);
    try {
      await api(`/agents/${encodeURIComponent(agent.id)}`, { method: "DELETE" });
      if (selectedAgentId === agent.id) onSelect("");
      await load();
      message.success(t("Agent 已删除"));
    } catch (error) {
      message.error(apiMessage(error) || t("删除 Agent 失败"));
    } finally {
      setDeletingId("");
    }
  }

  function openEditAgent(agent: VoNetAgent) {
    setEditingAgent(agent);
    setEditName(agent.name || "");
    setEditNotes(agent.notes || "");
  }

  const selectedAgent = agents.find((agent) => agent.id === selectedAgentId) || null;
  const agentOptions = agents.map((agent) => ({
    value: agent.id,
    label: `${agent.name || agent.id} · ${t(agent.status)}`,
  }));

  return (
    <section className="agent-manager-shell">
      <div className="agent-manager-toolbar">
        <div className="agent-manager-title">
          <span className="agent-manager-icon">
            <RouterRegular className="text-[21px]" />
          </span>
          <div>
            <h2>{t("VoNet-Agent")}</h2>
            <p>{t("选择远程硬件执行节点")}</p>
          </div>
        </div>
        <div className="agent-selector">
          <Select
            value={selectedAgentId}
            options={agentOptions}
            placeholder={t("选择 VoNet-Agent")}
            disabled={loading || agents.length === 0}
            onChange={onSelect}
            size="large"
          />
        </div>
        <Button
          variant="primary"
          icon={<AddRegular />}
          onClick={() => {
            setIssuedKey("");
            setCreating(true);
          }}
        >
          {t("新建 Agent")}
        </Button>
      </div>

      <div className="agent-focus-panel">
        {loading ? (
          <div className="agent-focus-empty">{t("加载中...")}</div>
        ) : !selectedAgent ? (
          <div className="agent-focus-empty">{t("尚未配置 VoNet-Agent")}</div>
        ) : (
          <>
            <div className="agent-info-hero">
              <div className="agent-info-identity">
                <span className="agent-info-icon"><RouterRegular /></span>
                <div className="min-w-0">
                  <div className="agent-info-title">
                    <strong>{selectedAgent.name || selectedAgent.id}</strong>
                    <Tag type={selectedAgent.status === "online" ? "success" : selectedAgent.status === "revoked" ? "danger" : "warning"}>
                      {t(selectedAgent.status)}
                    </Tag>
                  </div>
                  <p>{selectedAgent.notes || t("远程硬件执行节点")}</p>
                </div>
              </div>
              <div className="agent-info-actions">
                <Button size="small" icon={<EditRegular />} onClick={() => openEditAgent(selectedAgent)}>{t("编辑")}</Button>
                <Button
                  size="small"
                  icon={<ArrowClockwiseRegular />}
                  loading={rotatingId === selectedAgent.id}
                  disabled={!!rotatingId || !!deletingId}
                  onClick={() => void confirmRotateKey(selectedAgent)}
                >
                  {t("换发密钥")}
                </Button>
                <Button
                  size="small"
                  variant="danger"
                  icon={<DeleteRegular />}
                  loading={deletingId === selectedAgent.id}
                  disabled={!!rotatingId || !!deletingId}
                  onClick={() => void confirmDeleteAgent(selectedAgent)}
                >
                  {t("删除")}
                </Button>
              </div>
            </div>

            <div className="agent-info-grid">
              <FieldRow label={t("Agent ID")} value={selectedAgent.id} copyable monospace />
              <FieldRow label={t("Agent 名称")} value={selectedAgent.name || selectedAgent.id} />
              <FieldRow label={t("运行状态")} value={t(selectedAgent.status)} />
              <FieldRow label={t("密钥版本")} value={selectedAgent.keyVersion} monospace />
              <FieldRow label={t("协议版本")} value={selectedAgent.protocolVersion} monospace />
              <FieldRow label={t("证书指纹")} value={selectedAgent.certificateFingerprint} copyable monospace />
              <FieldRow label={t("创建时间")} value={formatDateTime(selectedAgent.createdAt)} />
              <FieldRow label={t("最近活动")} value={formatDateTime(selectedAgent.lastSeenAt)} />
              <FieldRow label={t("更新时间")} value={formatDateTime(selectedAgent.updatedAt)} />
            </div>
            <div className="agent-info-notes">
              <span>{t("备注")}</span>
              <p>{selectedAgent.notes || t("无备注")}</p>
            </div>
          </>
        )}
        <div className="agent-focus-content">{children}</div>
      </div>

      <Modal open={creating} onClose={() => { if (!submitting) setCreating(false); }} title={t("新建 VoNet-Agent")}>
        <div className="space-y-4">
          <div>
            <div className="mb-1.5 text-xs font-bold uppercase text-gray-500">{t("Agent 名称")}</div>
            <Input value={name} onChange={(event) => setName(event.target.value)} maxLength={128} />
          </div>
          <div>
            <div className="mb-1.5 text-xs font-bold uppercase text-gray-500">{t("备注")}</div>
            <Textarea value={notes} onChange={(event) => setNotes(event.target.value)} rows={3} maxLength={2000} />
          </div>
          <Button
            variant="primary"
            loading={submitting}
            disabled={!name.trim() || submitting}
            onClick={() => void createAgent()}
          >
            {t("生成通信密钥")}
          </Button>
        </div>
      </Modal>
      <Modal open={!!issuedKey} onClose={() => setIssuedKey("")} title={t("Agent 通信密钥")}>
        <div className="space-y-4">
          <p className="text-sm text-gray-600 dark:text-gray-300">{t("请在 VoNet-Agent 本地面板中填写 VoNet 地址和以下通信密钥。")}</p>
          <div className="rounded-lg bg-gray-50 p-3 font-mono text-sm break-all dark:bg-white/5">{issuedKey}</div>
          <Button icon={<KeyRegular />} onClick={() => void navigator.clipboard.writeText(issuedKey)}>{t("复制密钥")}</Button>
        </div>
      </Modal>
      <Modal
        open={!!editingAgent}
        onClose={() => { if (!savingEdit) setEditingAgent(null); }}
        title={t("编辑 VoNet-Agent")}
      >
        <div className="space-y-4">
          <div>
            <div className="mb-1.5 text-xs font-bold uppercase text-gray-500">{t("Agent 名称")}</div>
            <Input value={editName} onChange={(event) => setEditName(event.target.value)} maxLength={128} />
          </div>
          <div>
            <div className="mb-1.5 text-xs font-bold uppercase text-gray-500">{t("备注")}</div>
            <Textarea value={editNotes} onChange={(event) => setEditNotes(event.target.value)} rows={3} maxLength={2000} />
          </div>
          <Button
            variant="primary"
            icon={<SaveRegular />}
            loading={savingEdit}
            disabled={!editName.trim() || savingEdit}
            onClick={() => void updateAgent()}
          >
            {t("保存")}
          </Button>
        </div>
      </Modal>
    </section>
  );
}
