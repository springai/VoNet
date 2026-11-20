import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import { cx } from "../../lib/utils";

export type MobileSelectorStatusTone = "success" | "warning" | "danger" | "info" | "neutral";

export interface MobileSelectorItem {
  id: string;
  title: string;
  subtitle?: string;
  status?: string;
  statusTone?: MobileSelectorStatusTone;
  badge?: ReactNode;
  icon?: ReactNode;
}

interface MobileListSelectorProps {
  items: MobileSelectorItem[];
  selectedId: string;
  onSelect: (id: string) => void;
  icon: ReactNode;
  placeholder: string;
  ariaLabel: string;
  emptyText: string;
  footer?: ReactNode;
  className?: string;
}

export function MobileListSelector({
  items,
  selectedId,
  onSelect,
  icon,
  placeholder,
  ariaLabel,
  emptyText,
  footer,
  className,
}: MobileListSelectorProps) {
  const [open, setOpen] = useState(false);
  const [menuStyle, setMenuStyle] = useState<CSSProperties>({});
  const rootRef = useRef<HTMLDivElement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const selected = items.find((item) => item.id === selectedId);

  useEffect(() => {
    if (!open) return;
    const closeOnOutsideClick = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", closeOnOutsideClick);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutsideClick);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const updateMenuSize = () => {
      const rect = rootRef.current?.getBoundingClientRect();
      if (!rect) return;
      setMenuStyle({
        maxHeight: `${Math.max(180, window.innerHeight - rect.bottom - 92)}px`,
      });
    };
    updateMenuSize();
    window.addEventListener("resize", updateMenuSize);
    return () => window.removeEventListener("resize", updateMenuSize);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const frame = window.requestAnimationFrame(() => {
      menuRef.current
        ?.querySelector<HTMLElement>('[aria-selected="true"]')
        ?.scrollIntoView({ block: "nearest" });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [open]);

  return (
    <div ref={rootRef} className={cx("mobile-list-selector", className)}>
      <button
        type="button"
        className="mobile-list-selector-trigger"
        aria-label={ariaLabel}
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="mobile-list-selector-icon">{selected?.icon || icon}</span>
        <span className="mobile-list-selector-copy">
          <strong>{selected?.title || placeholder}</strong>
          {selected?.subtitle ? <small>{selected.subtitle}</small> : null}
        </span>
        {open ? <ChevronUp /> : <ChevronDown />}
      </button>
      {open ? (
        <div ref={menuRef} className="mobile-list-selector-menu" style={menuStyle} role="listbox" aria-label={ariaLabel}>
          {items.length === 0 ? (
            <div className="mobile-list-selector-empty">{emptyText}</div>
          ) : items.map((item) => (
            <button
              key={item.id}
              type="button"
              role="option"
              aria-selected={item.id === selectedId}
              className={cx("mobile-list-selector-option", item.id === selectedId && "is-active")}
              onClick={() => {
                onSelect(item.id);
                setOpen(false);
              }}
            >
              <span className="mobile-list-selector-icon">{item.icon || icon}</span>
              <span className="mobile-list-selector-copy">
                <strong>{item.title}</strong>
                {item.subtitle ? <small>{item.subtitle}</small> : null}
                {item.status ? (
                  <span className={cx("mobile-list-selector-status", `is-${item.statusTone || "neutral"}`)}>
                    <i />
                    {item.status}
                  </span>
                ) : null}
              </span>
              {item.badge ? <span className="mobile-list-selector-badge">{item.badge}</span> : null}
            </button>
          ))}
          {footer ? (
            <div
              className="mobile-list-selector-footer"
              onClick={(event) => {
                if ((event.target as HTMLElement).closest("button")) setOpen(false);
              }}
            >
              {footer}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
