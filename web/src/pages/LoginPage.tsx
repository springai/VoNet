import { useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  ArrowRightRegular,
  LockClosedRegular,
  PersonRegular,
  ShieldCheckmarkRegular,
} from "@fluentui/react-icons";
import { useAuth } from "../store/auth";
import { useI18n } from "../lib/i18n";
import { message } from "../components/ui/message";
import { BrandLogo } from "../components/shell/BrandLogo";

export default function LoginPage() {
  const { login } = useAuth();
  const { t } = useI18n();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [working, setWorking] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!username || !password) {
      message.warning(t("请输入用户名和密码"));
      return;
    }
    setWorking(true);
    await new Promise((resolve) => setTimeout(resolve, 420));
    const ok = await login(username, password);
    setWorking(false);
    if (ok) {
      message.success(t("欢迎回来"));
      const redirect = searchParams.get("redirect");
      navigate(redirect ? decodeURIComponent(redirect) : "/", { replace: true });
    } else {
      message.error(t("登录失败，请检查凭证"));
    }
  }

  return (
    <div className="reborn-login">
      <section className="reborn-login-brand">
        <div className="reborn-login-grid" aria-hidden="true" />
        <div className="reborn-login-brand-top">
          <BrandLogo className="reborn-login-logo" />
          <div>
            <strong>VoNet</strong>
            <span>MODEM · PC/SC · PANEL</span>
          </div>
        </div>

        <div className="reborn-login-brand-copy">
          <span className="reborn-login-kicker">CELLULAR CONTROL PLANE</span>
          <h1>VoNet</h1>
          <p>MODEM · PC/SC · PANEL</p>
        </div>

        <div className="reborn-login-console" aria-hidden="true">
          <div className="reborn-console-head">
            <span>LIVE LINK</span>
            <strong>READY</strong>
          </div>
          <div className="reborn-console-chart">
            {[34, 56, 42, 78, 62, 88, 54, 72, 46, 82, 64, 92].map((height, index) => (
              <span key={index} style={{ height: `${height}%`, animationDelay: `${index * 70}ms` }} />
            ))}
          </div>
          <div className="reborn-console-foot">
            <span><i /> SECURE SESSION</span>
            <span>12 MS</span>
          </div>
        </div>

        <div className="reborn-login-brand-foot">
          <span><ShieldCheckmarkRegular /> LOCAL CONTROL</span>
          <span>VO.NET / 2026</span>
        </div>
      </section>

      <section className="reborn-login-auth">
        <div className="reborn-login-auth-inner">
          <div className="reborn-login-auth-heading">
            <span>AUTHORIZED ACCESS</span>
            <h2>{t("登录")}</h2>
            <p>{t("欢迎回来")}</p>
          </div>

          <form className="reborn-login-form" onSubmit={submit}>
            <label>
              <span>{t("用户名")}</span>
              <div className="reborn-login-field">
                <PersonRegular />
                <input
                  placeholder={t("用户名")}
                  type="text"
                  autoComplete="username"
                  value={username}
                  onChange={(event) => setUsername(event.target.value)}
                />
              </div>
            </label>
            <label>
              <span>{t("密码")}</span>
              <div className="reborn-login-field">
                <LockClosedRegular />
                <input
                  placeholder={t("密码")}
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                />
              </div>
            </label>

            <button className="reborn-login-submit" type="submit" disabled={working}>
              <span>{working ? t("登录中...") : t("进入控制台")}</span>
              {working ? <i className="reborn-button-loader" /> : <ArrowRightRegular />}
            </button>
          </form>

          <div className="reborn-login-auth-foot">
            <span>VoNet © 2026</span>
            <span><i /> SYSTEM ONLINE</span>
          </div>
        </div>
      </section>
    </div>
  );
}
