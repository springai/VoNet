import { Outlet } from "react-router-dom";
import { LanguageSwitch } from "../ui/LanguageSwitch";
import { SwitchDark } from "../ui/SwitchDark";

// UnauthenticatedShell: centers the login card, theme toggle pinned top-right.
export function UnauthenticatedShell({
  isDark,
  onToggleTheme,
}: {
  isDark: boolean;
  onToggleTheme: () => void;
}) {
  return (
    <div className="reborn-login-shell">
      <div className="reborn-login-utility">
        <LanguageSwitch />
        <SwitchDark isDark={isDark} onToggle={onToggleTheme} />
      </div>
      <Outlet />
    </div>
  );
}
