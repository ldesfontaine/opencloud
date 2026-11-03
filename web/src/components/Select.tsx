import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router";

import { useT } from "../i18n/context";
import {
  closedMenu,
  filterOptions,
  fitMenu,
  hasSearch,
  openMenu,
  placeMenu,
  pressKey,
  searchMenu,
  typeaheadResetMs,
  type MenuOutcome,
  type MenuState,
  type SelectOption,
} from "../lib/select";
import { Icon, type IconName } from "./Icon";
import "./Select.scss";

export interface SelectProps {
  value: string;
  options: readonly SelectOption[];
  onChange: (value: string) => void;
  // Posé sur le déclencheur : le <label htmlFor> de Field y mène.
  id?: string;
  icon?: IconName;
  disabled?: boolean;
  "aria-label"?: string;
  // Ce que montre le déclencheur quand la valeur n'est aucune des options.
  placeholder?: string;
}

// Un menu déroulant dessiné par la page, jamais par le navigateur : la
// liste native ne suit ni le thème ni la direction artistique. Le
// déclencheur montre toujours le libellé de la valeur reçue, relu à chaque
// rendu : il ne garde aucun choix à lui.
export function Select({ value, options, onChange, id, icon, disabled = false, "aria-label": ariaLabel, placeholder }: SelectProps) {
  const t = useT();
  const listID = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const [storedMenu, setMenu] = useState<MenuState>(closedMenu);
  const searchable = hasSearch(options.length);
  const visible = filterOptions(options, storedMenu.query);
  const menu = fitMenu(storedMenu, visible.length);
  const chosen = options.find((option) => option.value === value);
  const optionID = (index: number) => `${listID}-${index}`;
  const activeID = menu.isOpen && menu.active >= 0 ? optionID(menu.active) : undefined;

  const close = (returnsFocus: boolean) => {
    setMenu(closedMenu);
    if (returnsFocus) {
      triggerRef.current?.focus();
    }
  };

  const choose = (option: SelectOption) => {
    if (option.disabled === true) {
      return;
    }
    close(true);
    if (option.value !== value) {
      onChange(option.value);
    }
  };

  const apply = (outcome: MenuOutcome) => {
    setMenu(outcome.state);
    if (outcome.returnsFocus) {
      triggerRef.current?.focus();
    }
    if (outcome.chosen !== undefined && outcome.chosen !== value) {
      onChange(outcome.chosen);
    }
  };

  const onKeyDown = (event: KeyboardEvent, source: "trigger" | "search") => {
    const outcome = pressKey(menu, event.key, { visible, value, searchable, source });
    if (outcome === null) {
      return;
    }
    if (!outcome.keepsDefault) {
      event.preventDefault();
    }
    apply(outcome);
  };

  const toggle = () => {
    if (menu.isOpen) {
      close(false);
      return;
    }
    setMenu(openMenu(options, value));
  };

  useTypeaheadReset(menu.typed, () => setMenu((current) => ({ ...current, typed: "" })));
  useOutsideClose(menu.isOpen, [triggerRef, menuRef], () => close(false));
  useMenuPlace(menu.isOpen, triggerRef, menuRef);

  // Désactivé pendant l'ouverture (la saisie dont il dépend a changé) : la
  // liste se ferme, rien ne se choisit plus.
  useEffect(() => {
    if (disabled) {
      setMenu(closedMenu);
    }
  }, [disabled]);

  // Avec une recherche, le focus va au champ ; sans, il reste au
  // déclencheur, qui désigne l'option active par aria-activedescendant.
  useEffect(() => {
    if (menu.isOpen && searchable) {
      searchRef.current?.focus();
    }
  }, [menu.isOpen, searchable]);

  // L'option active reste dans la vue quand le clavier la déplace.
  useEffect(() => {
    if (activeID !== undefined) {
      document.getElementById(activeID)?.scrollIntoView({ block: "nearest" });
    }
  }, [activeID]);

  const classes = ["select"];
  if (menu.isOpen) {
    classes.push("open");
  }
  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        id={id}
        className={classes.join(" ")}
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={menu.isOpen}
        aria-controls={menu.isOpen ? listID : undefined}
        aria-activedescendant={searchable ? undefined : activeID}
        aria-label={ariaLabel}
        disabled={disabled}
        onClick={toggle}
        onKeyDown={(event) => onKeyDown(event, "trigger")}
        // Firefox clique au relâcher de l'espace même quand l'appui est
        // empêché : la liste que l'espace vient d'ouvrir se refermerait.
        onKeyUp={(event) => {
          if (event.key === " ") {
            event.preventDefault();
          }
        }}
      >
        {icon && <Icon name={icon} />}
        <span className={chosen ? "select-value" : "select-value select-placeholder"}>{chosen ? <OptionText option={chosen} /> : (placeholder ?? t("select.choose"))}</span>
        <span className="chev">
          <Icon name="chevron-down" />
        </span>
      </button>
      {menu.isOpen &&
        createPortal(
          <div ref={menuRef} className="select-menu">
            {searchable && (
              <span className="select-search">
                <Icon name="search" />
                <input
                  ref={searchRef}
                  type="text"
                  role="combobox"
                  aria-expanded
                  aria-controls={listID}
                  aria-autocomplete="list"
                  aria-activedescendant={activeID}
                  aria-label={t("select.search")}
                  placeholder={t("select.search")}
                  autoComplete="off"
                  value={menu.query}
                  onChange={(event) => setMenu(searchMenu(menu, options, event.target.value))}
                  onKeyDown={(event) => onKeyDown(event, "search")}
                />
              </span>
            )}
            <div id={listID} role="listbox" aria-label={ariaLabel} aria-labelledby={ariaLabel === undefined ? id : undefined} className="select-list">
              {visible.map((option, index) => {
                const row = (
                  <div
                    key={option.value}
                    id={optionID(index)}
                    role="option"
                    aria-selected={option.value === value}
                    aria-disabled={option.disabled === true ? true : undefined}
                    className={optionClass(option, index === menu.active)}
                    // Le focus ne quitte pas le déclencheur ou la recherche au clic.
                    onMouseDown={(event) => event.preventDefault()}
                    onMouseMove={() => setMenu((current) => (current.active === index ? current : { ...current, active: index }))}
                    onClick={() => choose(option)}
                  >
                    <span className="select-option-label">
                      <OptionText option={option} />
                    </span>
                    {option.value === value && <Icon name="check" />}
                  </div>
                );
                if (option.reason === undefined) {
                  return row;
                }
                // La raison et le lien vivent hors de l'option : l'option grisée
                // est désactivée pour les lecteurs d'écran, et son lien doit rester actif.
                return (
                  <div key={option.value} role="group" className="select-option-group">
                    {row}
                    <span className="select-option-reason">
                      {option.reason}
                      {option.link !== undefined && (
                        <>
                          {" "}
                          <Link to={option.link.to} onClick={() => close(false)}>
                            {option.link.label}
                          </Link>
                        </>
                      )}
                    </span>
                  </div>
                );
              })}
              {visible.length === 0 && <div className="select-empty">{t("select.no_result")}</div>}
            </div>
          </div>,
          overlayHost(triggerRef.current),
        )}
    </>
  );
}

function optionClass(option: SelectOption, active: boolean): string {
  const classes = ["select-option"];
  if (active) {
    classes.push("active");
  }
  if (option.disabled === true) {
    classes.push("select-option-disabled");
  }
  return classes.join(" ");
}

function OptionText({ option }: { option: SelectOption }) {
  return (
    <>
      {option.label}
      {option.hint !== undefined && <span className="select-hint"> {option.hint}</span>}
    </>
  );
}

// Les lettres tapées se suivent tant qu'on ne marque pas de pause.
function useTypeaheadReset(typed: string, reset: () => void) {
  const resetRef = useRef(reset);
  resetRef.current = reset;
  useEffect(() => {
    if (typed === "") {
      return;
    }
    const timer = window.setTimeout(() => resetRef.current(), typeaheadResetMs);
    return () => window.clearTimeout(timer);
  }, [typed]);
}

// Un clic hors du déclencheur et de la liste ferme le menu, sans voler le
// focus à ce qui a été cliqué.
function useOutsideClose(isOpen: boolean, insides: readonly RefObject<HTMLElement | null>[], close: () => void) {
  const closeRef = useRef(close);
  closeRef.current = close;
  useEffect(() => {
    if (!isOpen) {
      return;
    }
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!insides.some((inside) => inside.current?.contains(target))) {
        closeRef.current();
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [isOpen]);
}

// Où poser une liste flottante : dans la fenêtre modale ouverte qui contient
// le déclencheur, sinon dans <body>. Un <dialog> ouvert par showModal() vit
// dans la couche supérieure du navigateur et rend tout ce qui est hors de lui
// inerte : une liste portée dans <body> resterait dessous, invisible et
// inclicable, quel que soit son z-index.
function overlayHost(trigger: Element | null): HTMLElement {
  return trigger?.closest<HTMLElement>("dialog[open]") ?? document.body;
}

// La liste vit dans <body> (ou dans la fenêtre modale), en position fixe : une carte en overflow hidden
// ou un tableau qui défile ne la coupe pas. Sa place suit le déclencheur au
// défilement et au redimensionnement. Elle s'écrit par le CSSOM : la CSP
// refuse les attributs style.
function useMenuPlace(isOpen: boolean, triggerRef: RefObject<HTMLElement | null>, menuRef: RefObject<HTMLElement | null>) {
  useLayoutEffect(() => {
    if (!isOpen) {
      return;
    }
    const place = () => {
      const trigger = triggerRef.current;
      const menu = menuRef.current;
      if (trigger === null || menu === null) {
        return;
      }
      const spot = placeMenu(trigger.getBoundingClientRect(), { width: window.innerWidth, height: window.innerHeight });
      menu.style.setProperty("top", spot.top === null ? "auto" : `${spot.top}px`);
      menu.style.setProperty("bottom", spot.bottom === null ? "auto" : `${spot.bottom}px`);
      menu.style.setProperty("left", `${spot.left}px`);
      menu.style.setProperty("min-width", `${spot.minWidth}px`);
      menu.style.setProperty("max-width", `${spot.maxWidth}px`);
      menu.style.setProperty("max-height", `${spot.maxHeight}px`);
    };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [isOpen]);
}

type EnumSelectProps<Value extends string> = Omit<SelectProps, "value" | "options" | "onChange"> & {
  value: Value;
  values: readonly Value[];
  labelKey: string;
  onChange: (value: Value) => void;
};

// Un choix dans une liste fermée : chaque option se traduit par la clé
// labelKey + valeur (channel.format_ + slack), et la valeur choisie se
// retrouve dans values plutôt que d'être forcée au type.
export function EnumSelect<Value extends string>({ value, values, labelKey, onChange, ...rest }: EnumSelectProps<Value>) {
  const t = useT();
  const options = values.map((candidate) => ({ value: candidate, label: t(`${labelKey}${candidate}`) }));
  const choose = (chosen: string) => {
    const known = values.find((candidate) => candidate === chosen);
    if (known !== undefined) {
      onChange(known);
    }
  };
  return <Select value={value} options={options} onChange={choose} {...rest} />;
}
