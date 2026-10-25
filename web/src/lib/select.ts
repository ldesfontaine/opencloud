// Le menu déroulant maison : ce qui se décide sans navigateur (la touche
// pressée, la lettre tapée, la recherche, la place de la liste) vit ici
// pour se tester ; le composant (components/Select.tsx) ne fait que le
// brancher sur le DOM.

export interface SelectOption {
  value: string;
  label: string;
}

// Au-delà, la liste ouverte porte un champ de recherche en tête.
export const searchThreshold = 8;

// Une suite de lettres tapées vaut un seul mot tant que les frappes se
// suivent de moins que ce délai.
export const typeaheadResetMs = 500;

export function hasSearch(optionCount: number): boolean {
  return optionCount > searchThreshold;
}

// Comparer sans casse ni accent : « etat » trouve « État des services ».
function folded(text: string): string {
  return text.normalize("NFD").replace(/\p{Diacritic}/gu, "").toLowerCase();
}

export function filterOptions(options: readonly SelectOption[], query: string): readonly SelectOption[] {
  const wanted = folded(query.trim());
  if (wanted === "") {
    return options;
  }
  return options.filter((option) => folded(option.label).includes(wanted));
}

// L'option dont le libellé commence par ce qui a été tapé, en partant de
// l'option active. Une même lettre répétée (« sss ») passe d'une option en
// « s » à la suivante, comme le menu natif.
export function typeaheadIndex(options: readonly SelectOption[], typed: string, from: number): number {
  const first = typed.charAt(0);
  const isRepeat = typed.length > 1 && [...typed].every((letter) => letter === first);
  const prefix = folded(isRepeat ? first : typed);
  const start = prefix.length === 1 ? from + 1 : Math.max(from, 0);
  for (let step = 0; step < options.length; step++) {
    const index = (start + step) % options.length;
    const option = options[index];
    if (option !== undefined && folded(option.label).startsWith(prefix)) {
      return index;
    }
  }
  return -1;
}

export interface MenuState {
  isOpen: boolean;
  // L'option sous le clavier ou la souris, dans la liste visible ; -1 : aucune.
  active: number;
  query: string;
  typed: string;
}

export const closedMenu: MenuState = { isOpen: false, active: -1, query: "", typed: "" };

// À l'ouverture, l'option active est celle qui est choisie, sinon la première.
export function openMenu(options: readonly SelectOption[], value: string): MenuState {
  const chosen = options.findIndex((option) => option.value === value);
  const active = chosen >= 0 ? chosen : options.length > 0 ? 0 : -1;
  return { isOpen: true, active, query: "", typed: "" };
}

// Les options ont pu changer pendant l'ouverture (une relecture du direct) :
// l'option active ne désigne jamais une ligne qui n'existe plus.
export function fitMenu(state: MenuState, visibleCount: number): MenuState {
  const last = visibleCount - 1;
  if (state.active <= last) {
    return state;
  }
  return { ...state, active: last };
}

export interface MenuOutcome {
  state: MenuState;
  // La valeur choisie, quand la touche en choisit une.
  chosen?: string;
  // Le focus revient au déclencheur : Entrée, Espace, Échap, Tab.
  returnsFocus?: boolean;
  // Tab ferme la liste mais laisse le navigateur passer au champ suivant ;
  // le focus rendu avant, il part du déclencheur et non de la recherche,
  // qui disparaît.
  keepsDefault?: boolean;
}

export interface KeyContext {
  // Les options visibles : toutes, ou celles que la recherche garde.
  visible: readonly SelectOption[];
  value: string;
  searchable: boolean;
  // D'où vient la touche : le déclencheur ou le champ de recherche, où
  // l'espace et les lettres s'écrivent.
  source: "trigger" | "search";
}

const openingKeys = ["ArrowDown", "ArrowUp", "Enter", " ", "Home", "End"];

function isPrintable(key: string): boolean {
  return key.length === 1 && key !== " ";
}

// Ce que fait une touche ; null quand elle ne concerne pas le menu, et le
// navigateur la garde.
export function pressKey(state: MenuState, key: string, context: KeyContext): MenuOutcome | null {
  if (!state.isOpen) {
    return pressKeyClosed(key, context);
  }
  return pressKeyOpen(state, key, context);
}

function pressKeyClosed(key: string, context: KeyContext): MenuOutcome | null {
  const opened = openMenu(context.visible, context.value);
  const last = context.visible.length - 1;
  if (key === "Home") {
    return { state: { ...opened, active: Math.min(0, last) } };
  }
  if (key === "End") {
    return { state: { ...opened, active: last } };
  }
  if (openingKeys.includes(key)) {
    return { state: opened };
  }
  if (!isPrintable(key)) {
    return null;
  }
  // Une lettre ouvre la liste : elle va dans la recherche s'il y en a une,
  // sinon elle saute à la première option qui commence par elle.
  if (context.searchable) {
    const found = filterOptions(context.visible, key);
    return { state: { ...opened, query: key, active: found.length > 0 ? 0 : -1 } };
  }
  const typed = key;
  const index = typeaheadIndex(context.visible, typed, opened.active);
  return { state: { ...opened, typed, active: index >= 0 ? index : opened.active } };
}

function pressKeyOpen(state: MenuState, key: string, context: KeyContext): MenuOutcome | null {
  const last = context.visible.length - 1;
  switch (key) {
    case "ArrowDown":
      return { state: { ...state, active: Math.min(state.active + 1, last) } };
    case "ArrowUp":
      return { state: { ...state, active: last < 0 ? -1 : Math.max(state.active - 1, 0) } };
    case "Home":
      return context.source === "search" ? null : { state: { ...state, active: Math.min(0, last) } };
    case "End":
      return context.source === "search" ? null : { state: { ...state, active: last } };
    case "Enter":
      return chooseActive(state, context.visible);
    case " ":
      return context.source === "search" ? null : chooseActive(state, context.visible);
    case "Escape":
      return { state: closedMenu, returnsFocus: true };
    case "Tab":
      return { state: closedMenu, returnsFocus: true, keepsDefault: true };
  }
  if (context.source === "search" || !isPrintable(key)) {
    return null;
  }
  const typed = state.typed + key;
  const index = typeaheadIndex(context.visible, typed, state.active);
  return { state: { ...state, typed, active: index >= 0 ? index : state.active } };
}

function chooseActive(state: MenuState, visible: readonly SelectOption[]): MenuOutcome {
  const option = visible[state.active];
  if (option === undefined) {
    return { state: closedMenu, returnsFocus: true };
  }
  return { state: closedMenu, chosen: option.value, returnsFocus: true };
}

// La recherche change : l'option active redevient la première trouvée.
export function searchMenu(state: MenuState, options: readonly SelectOption[], query: string): MenuState {
  const found = filterOptions(options, query);
  return { ...state, query, active: found.length > 0 ? 0 : -1 };
}

export interface Box {
  top: number;
  bottom: number;
  left: number;
  width: number;
}

export interface Viewport {
  width: number;
  height: number;
}

// La place de la liste ouverte, en position fixe : top quand elle s'ouvre
// dessous, bottom quand elle s'ouvre dessus.
export interface MenuPlace {
  top: number | null;
  bottom: number | null;
  left: number;
  minWidth: number;
  maxWidth: number;
  maxHeight: number;
}

const menuGap = 4;
const viewportMargin = 8;
const menuMaxHeight = 280;
// En dessous de cette place sous le déclencheur, la liste s'ouvre dessus
// s'il y en a davantage là-haut.
const menuMinRoom = 160;

// Sous le déclencheur, dessus s'il manque de place en bas ; au moins aussi
// large que lui, jamais hors de la fenêtre.
export function placeMenu(trigger: Box, viewport: Viewport): MenuPlace {
  const roomBelow = viewport.height - trigger.bottom - menuGap - viewportMargin;
  const roomAbove = trigger.top - menuGap - viewportMargin;
  const opensAbove = roomBelow < menuMinRoom && roomAbove > roomBelow;
  const widest = viewport.width - 2 * viewportMargin;
  const minWidth = Math.min(trigger.width, widest);
  const left = Math.max(viewportMargin, Math.min(trigger.left, viewport.width - viewportMargin - minWidth));
  const maxWidth = viewport.width - viewportMargin - left;
  if (opensAbove) {
    return { top: null, bottom: viewport.height - trigger.top + menuGap, left, minWidth, maxWidth, maxHeight: Math.min(menuMaxHeight, roomAbove) };
  }
  return { top: trigger.bottom + menuGap, bottom: null, left, minWidth, maxWidth, maxHeight: Math.min(menuMaxHeight, Math.max(roomBelow, 0)) };
}
