import { EyeOffRegular, EyeRegular } from "@fluentui/react-icons";
import { Button } from "./Button";
import { useShowSensitive } from "../devices/shared";
import { useI18n } from "../../lib/i18n";

export function SensitiveInfoButton({
  showLabel = false,
  className,
}: {
  showLabel?: boolean;
  className?: string;
}) {
  const { t } = useI18n();
  const [showSensitive, toggleSensitive] = useShowSensitive();
  const label = showSensitive ? t("隐藏敏感信息") : t("显示敏感信息");

  return (
    <Button
      variant="text"
      size="small"
      className={className}
      icon={showSensitive ? <EyeRegular /> : <EyeOffRegular />}
      title={label}
      aria-label={label}
      onClick={toggleSensitive}
    >
      {showLabel ? label : null}
    </Button>
  );
}
