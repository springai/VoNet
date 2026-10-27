import { useEffect, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { cx } from "../../lib/utils";

type DrawerSide = "left" | "right";

// el-drawer equivalent used for the mobile sidebar and focused editors.
export function Drawer({
  open,
  onClose,
  children,
  widthClass = "w-64",
  side = "left",
  closeOnOverlay = true,
  className,
}: {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  widthClass?: string;
  side?: DrawerSide;
  closeOnOverlay?: boolean;
  className?: string;
}) {
  useEffect(() => {
    if (!open) return;
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return createPortal(
    <div className="fixed inset-0 z-[2900]">
      <div
        className="ui-drawer-overlay absolute inset-0 bg-black/50 backdrop-blur-sm"
        onClick={() => {
          if (closeOnOverlay) onClose();
        }}
      />
      <div
        className={cx(
          "ui-drawer-panel absolute inset-y-0 overflow-hidden shadow-2xl",
          side === "left"
            ? "left-0 animate-[drawer-in_0.25s_cubic-bezier(0.4,0,0.2,1)]"
            : "right-0 animate-[drawer-in-right_0.25s_cubic-bezier(0.4,0,0.2,1)]",
          widthClass,
          className,
        )}
      >
        {children}
      </div>
    </div>,
    document.body,
  );
}
