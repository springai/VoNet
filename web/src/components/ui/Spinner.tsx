import { cx } from "../../lib/utils";

// Circular spinner matching the login / loading indicators (border spinner).
export function Spinner({ className }: { className?: string }) {
  return (
    <span
      className={cx(
        "micro-spinner inline-flex items-center justify-center",
        className,
      )}
      aria-hidden="true"
    >
      <span />
    </span>
  );
}
