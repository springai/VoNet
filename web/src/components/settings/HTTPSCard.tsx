import { useState } from "react";
import { ArrowDownloadRegular, ArrowUploadRegular, CopyRegular, LockClosedRegular, ShieldCheckmarkRegular } from "@fluentui/react-icons";
import { api, apiMessage } from "../../api";
import type { HTTPSSettings } from "../../types";
import { useI18n } from "../../lib/i18n";
import { Button, Modal, Textarea, message } from "../ui";
import { copyText } from "./controls";
import { CardDecor, CardIcon, CardTitle } from "./Cards";

export function HTTPSCard({
  value,
  loading,
  onUploaded,
}: {
  value: HTTPSSettings | null;
  loading: boolean;
  onUploaded: (value: HTTPSSettings) => void;
}) {
  const { lang } = useI18n();
  const zh = lang === "zh";
  const [open, setOpen] = useState(false);
  const [certificate, setCertificate] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [saving, setSaving] = useState(false);
  const agentFingerprint = (value?.fingerprint ?? "").replaceAll(":", "").toLowerCase();

  async function uploadCertificate() {
    if (!certificate.trim() || !privateKey.trim() || saving) return;
    setSaving(true);
    try {
      const updated = await api<HTTPSSettings>("/settings/https/certificate", {
        method: "POST",
        body: { certificate, privateKey },
      });
      onUploaded(updated);
      setOpen(false);
      setCertificate("");
      setPrivateKey("");
      message.success(
        updated.restartPending
          ? (zh ? "HTTPS 证书已保存，服务正在重启以启用新证书" : "HTTPS certificate saved; the service is restarting to activate it")
          : (zh ? "HTTPS 证书已保存并启用" : "HTTPS certificate saved and enabled"),
      );
    } catch (error) {
      message.error(apiMessage(error) || (zh ? "HTTPS 证书保存失败" : "Failed to save HTTPS certificate"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="ui-card group relative overflow-hidden p-8">
      <CardDecor />
      <div className="relative z-10 mb-6 flex items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <CardIcon>
            <LockClosedRegular className="text-[24px]" />
          </CardIcon>
          <CardTitle
            title={zh ? "本机 HTTPS" : "Local HTTPS"}
            subtitle={zh ? "HTTPS 已强制启用" : "HTTPS is always enforced"}
          />
        </div>
        <div className="flex h-8 items-center gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-3 text-xs font-semibold text-emerald-700 dark:border-emerald-500/20 dark:bg-emerald-500/10 dark:text-emerald-300">
          <ShieldCheckmarkRegular className="text-[17px]" />
          {zh ? "已启用，不可关闭" : "Enabled, cannot be disabled"}
        </div>
      </div>
      <div className="relative z-10 space-y-4 text-sm text-gray-600 dark:text-gray-300">
        <p>
          {zh
            ? "HTTP 请求会自动跳转到 HTTPS。可上传 CA 签发的证书与匹配私钥；未上传时使用本机自签证书。"
            : "HTTP requests redirect to HTTPS. Upload a CA-issued certificate and matching private key, or keep using the local self-signed certificate."}
        </p>
        <div className="grid gap-2 rounded-xl bg-gray-50 p-3 dark:bg-white/5">
          <div className="flex items-center justify-between gap-3">
            <span className="text-xs font-bold uppercase tracking-wider text-gray-500">
              {zh ? "证书来源" : "Certificate source"}
            </span>
            <span>{value?.certificateSource === "uploaded" ? (zh ? "已上传证书" : "Uploaded certificate") : (zh ? "本机自签证书" : "Self-signed certificate")}</span>
          </div>
          {value?.certificateSubject ? (
            <div className="min-w-0 text-xs text-gray-500 dark:text-gray-400">
              <span className="font-semibold">{zh ? "主体：" : "Subject: "}</span>
              <span className="break-all">{value.certificateSubject}</span>
            </div>
          ) : null}
          {value?.fingerprint ? (
            <div className="flex min-w-0 items-center justify-between gap-3 text-xs text-gray-500 dark:text-gray-400">
              <div className="min-w-0">
                <div className="font-semibold">VoNet HTTPS证书SHA256</div>
                <div className="mt-1 break-all font-mono">{agentFingerprint}</div>
              </div>
              <Button
                size="small"
                plain
                icon={<CopyRegular className="text-[16px]" />}
                onClick={() => void copyText(agentFingerprint, zh ? "已复制证书 SHA256" : "Certificate SHA256 copied")}
              >
                {zh ? "复制" : "Copy"}
              </Button>
            </div>
          ) : null}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="primary"
            loading={saving}
            disabled={loading}
            icon={<ArrowUploadRegular />}
            onClick={() => setOpen(true)}
          >
            {zh ? "上传 CA 证书" : "Upload CA certificate"}
          </Button>
          <Button
            disabled={loading}
            icon={<ArrowDownloadRegular />}
            onClick={() => window.open("/api/settings/https/certificate", "_blank")}
          >
            {zh ? "下载当前证书" : "Download current certificate"}
          </Button>
        </div>
      </div>
      <Modal
        open={open}
        onClose={() => !saving && setOpen(false)}
        title={zh ? "上传 CA 证书" : "Upload CA certificate"}
        width="max-w-3xl"
        closeOnOverlay={!saving}
        showClose={!saving}
        footer={
          <>
            <Button disabled={saving} onClick={() => setOpen(false)}>{zh ? "取消" : "Cancel"}</Button>
            <Button
              variant="primary"
              loading={saving}
              disabled={!certificate.trim() || !privateKey.trim()}
              onClick={() => void uploadCertificate()}
            >
              {zh ? "保存证书" : "Save certificate"}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <div>
            <div className="mb-1.5 text-xs font-bold uppercase text-gray-500">{zh ? "证书内容（PEM）" : "Certificate (PEM)"}</div>
            <Textarea
              value={certificate}
              onChange={(event) => setCertificate(event.target.value)}
              rows={9}
              className="font-mono text-xs"
              placeholder={"-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----"}
            />
          </div>
          <div>
            <div className="mb-1.5 text-xs font-bold uppercase text-gray-500">{zh ? "私钥内容（PEM）" : "Private key (PEM)"}</div>
            <Textarea
              value={privateKey}
              onChange={(event) => setPrivateKey(event.target.value)}
              rows={9}
              className="font-mono text-xs"
              placeholder={"-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----"}
            />
          </div>
          <p className="text-xs text-amber-600 dark:text-amber-400">
            {zh
              ? "证书与私钥必须匹配。私钥仅保存到本机 data/etc/certs 目录，并使用仅所有者可读权限。"
              : "The certificate and private key must match. The key is stored only in the local data/etc/certs directory with owner-only permissions."}
          </p>
        </div>
      </Modal>
    </div>
  );
}
