import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { Link } from "react-router";

import { useT } from "../i18n/context";
import { nextItemIndex, placeMenu, returnsFocus, type MenuClose } from "../lib/moreMenu";
import { Button } from "./Button";
import "./MoreMenu.scss";

// Un choix du menu : une action sur place, ou un lien vers un écran.
export type MenuAction = { label: string; danger?: boolean } & ({ onSelect: () => void } | { to: string });

// Le menu « … » d'un objet : ses actions secondaires, hors de la ligne ou de
// la carte. Le menu se pose en position fixe, contre le bouton : il sort du
// cadre qui défile d'un tableau au lieu d'y être coupé. Au clavier, les
// flèches passent d'un choix à l'autre, Échap referme et rend le focus.
export function MoreMenu({ actions, label }: { actions: MenuAction[]; label?: string }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLSpanElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const listId = useId();

  const items = () => Array.from(list.current?.querySelectorAll<HTMLElement>("[role=menuitem]") ?? []);

  const close = useCallback((event: MenuClose) => {
    setOpen(false);
    if (returnsFocus(event)) {
      root.current?.querySelector<HTMLButtonElement>(":scope > button")?.focus();
    }
  }, []);

  // Avant d'être peint : placé contre le bouton, le focus sur le premier choix.
  useLayoutEffect(() => {
    const button = root.current?.querySelector<HTMLButtonElement>(":scope > button");
    const menu = list.current;
    if (!open || !button || !menu) {
      return;
    }
    const place = placeMenu(
      button.getBoundingClientRect(),
      { width: menu.offsetWidth, height: menu.offsetHeight },
      { width: window.innerWidth, height: window.innerHeight },
    );
    // Par le CSSOM : la CSP refuse l'attribut style, pas ces propriétés.
    menu.style.top = `${place.top}px`;
    menu.style.left = `${place.left}px`;
    menu.querySelector<HTMLElement>("[role=menuitem]")?.focus({ preventScroll: true });
  }, [open]);

  // Ouvert : un clic ailleurs le referme, un défilement aussi, sans quoi il
  // resterait en place pendant que son bouton s'en va.
  useEffect(() => {
    if (!open) {
      return;
    }
    const onPointerDown = (event: PointerEvent) => {
      if (event.target instanceof Node && root.current?.contains(event.target)) {
        return;
      }
      close("outside");
    };
    const onMove = () => close("scroll");
    document.addEventListener("pointerdown", onPointerDown);
    window.addEventListener("scroll", onMove, true);
    window.addEventListener("resize", onMove);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      window.removeEventListener("scroll", onMove, true);
      window.removeEventListener("resize", onMove);
    };
  }, [open, close]);

  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      close("escape");
      return;
    }
    // Tab laisse le focus suivre son chemin : les choix sont hors de l'ordre
    // de tabulation, il sort donc du menu, qui se referme derrière lui.
    if (event.key === "Tab") {
      close("tab");
      return;
    }
    const all = items();
    const current = all.findIndex((item) => item === document.activeElement);
    const next = nextItemIndex(current, all.length, event.key);
    if (next !== null) {
      event.preventDefault();
      all[next]?.focus();
    }
  };

  const choose = (action: MenuAction) => {
    close("select");
    if ("onSelect" in action) {
      action.onSelect();
    }
  };

  return (
    <span className={open ? "more-menu open" : "more-menu"} ref={root}>
      <Button
        variant="ghost"
        small
        icon="more"
        aria-label={label ?? t("action.more")}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        onClick={() => setOpen(!open)}
      />
      {open && (
        <div className="more-list" role="menu" id={listId} aria-label={label ?? t("action.more")} ref={list} onKeyDown={onKeyDown}>
          {actions.map((action) => {
            const className = action.danger ? "more-item danger" : "more-item";
            if ("to" in action) {
              return (
                <Link key={action.label} className={className} role="menuitem" tabIndex={-1} to={action.to} onClick={() => choose(action)}>
                  {action.label}
                </Link>
              );
            }
            return (
              <button key={action.label} className={className} type="button" role="menuitem" tabIndex={-1} onClick={() => choose(action)}>
                {action.label}
              </button>
            );
          })}
        </div>
      )}
    </span>
  );
}
