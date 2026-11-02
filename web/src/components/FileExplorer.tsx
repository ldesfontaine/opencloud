import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";

import { failureOf, type ApiError } from "../api/client";
import { useI18n } from "../i18n/context";
import { childPath, crumbsOf, moveCursor, parentPath, toggleName } from "../lib/fileExplorer";
import { counted } from "../lib/counted";
import { formatWhen } from "../lib/time";
import { formatBytes } from "../lib/units";
import { Button } from "./Button";
import { Note } from "./Card";
import { Failure, type RightGesture } from "./Failure";
import { Icon } from "./Icon";
import "./FileExplorer.scss";

// Le patron de l'explorateur : il ne sait RIEN d'où viennent les fichiers. Une
// source répond à deux questions (quelles vues ? que contient ce dossier ?), les
// sorties disent ce qu'on peut faire de la sélection. Aujourd'hui la source est
// un instantané restic (page Sauvegardes) ; demain, le système de fichiers
// vivant d'une machine, pour le module Fichiers. Rien ici n'écrit un fichier.

export interface FileEntry {
  name: string;
  kind: "dir" | "file" | "link" | "other";
  size: number;
  // ISO, vide si inconnue.
  modified: string;
}

interface FileListing {
  entries: FileEntry[];
  // Le dossier avait plus de fils que la source n'en rend.
  truncated: boolean;
}

// Une vue : un instantané, ou rien du tout pour un système de fichiers vivant.
export interface FileView {
  id: string;
  label: string;
  hint: string;
}

export interface FileSource {
  // Sans vue (liste vide), l'explorateur n'a pas de colonne de gauche et lit la vue "".
  loadViews: () => Promise<FileView[]>;
  list: (viewID: string, folder: string) => Promise<FileListing>;
}

// Ce qui est choisi : la vue, le dossier ouvert, et les noms cochés dedans.
interface FileSelection {
  view: FileView | null;
  folder: string;
  names: string[];
}

// Une sortie autorisée : un nom, et ce qu'elle montre pour la sélection.
export interface FileOutput {
  id: string;
  label: string;
  render: (selection: FileSelection) => ReactNode;
}

interface Props {
  source: FileSource;
  outputs: readonly FileOutput[];
  // Le nom de la racine dans le fil d'Ariane (la sauvegarde, la machine).
  rootLabel: string;
  viewsLabel: string;
  // Un refus de droit y porte son geste (« Ré-enrôler avec ce droit »).
  gesture?: RightGesture | undefined;
}

interface Loaded {
  key: string;
  listing: FileListing | null;
  error: ApiError | null;
}

// Les noms d'un état : les vues, puis le dossier, chacun lu quand sa clé change.
export function FileExplorer({ source, outputs, rootLabel, viewsLabel, gesture }: Props) {
  const { t, language } = useI18n();
  const listID = useId();
  const [views, setViews] = useState<{ list: FileView[]; error: ApiError | null } | null>(null);
  const [viewID, setViewID] = useState("");
  const [folder, setFolder] = useState("/");
  const [selected, setSelected] = useState<string[]>([]);
  const [cursor, setCursor] = useState(0);
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const list = useRef<HTMLUListElement>(null);

  useEffect(() => {
    let cancelled = false;
    source
      .loadViews()
      .then((found) => {
        if (!cancelled) {
          setViews({ list: found, error: null });
          setViewID(found[0]?.id ?? "");
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setViews({ list: [], error: failureOf(error) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [source]);

  const ready = views !== null && views.error === null && (views.list.length === 0 || viewID !== "");
  const wanted = `${viewID}\u0000${folder}`;
  useEffect(() => {
    if (!ready) {
      return;
    }
    let cancelled = false;
    source
      .list(viewID, folder)
      .then((listing) => {
        if (!cancelled) {
          setLoaded({ key: wanted, listing, error: null });
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setLoaded({ key: wanted, listing: null, error: failureOf(error) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [source, ready, viewID, folder, wanted]);

  const current = loaded !== null && loaded.key === wanted ? loaded : null;
  const entries = current?.listing?.entries ?? [];
  const activeID = entries.length === 0 ? undefined : `${listID}-${cursor}`;

  const go = (next: string) => {
    setFolder(next);
    setSelected([]);
    setCursor(0);
  };
  const open = (entry: FileEntry) => {
    if (entry.kind === "dir") {
      go(childPath(folder, entry.name));
    }
  };
  const chooseView = (id: string) => {
    setViewID(id);
    go("/");
  };

  const onKeyDown = (event: KeyboardEvent<HTMLUListElement>) => {
    const entry = entries[cursor];
    switch (event.key) {
      case "ArrowDown":
        setCursor(moveCursor(cursor, 1, entries.length));
        break;
      case "ArrowUp":
        setCursor(moveCursor(cursor, -1, entries.length));
        break;
      case "Enter":
        if (entry !== undefined) {
          open(entry);
        }
        break;
      case " ":
        if (entry !== undefined) {
          setSelected(toggleName(selected, entry.name));
        }
        break;
      case "Backspace":
        go(parentPath(folder));
        break;
      default:
        return;
    }
    event.preventDefault();
  };

  const view = views?.list.find((candidate) => candidate.id === viewID) ?? null;
  const selection: FileSelection = { view, folder, names: selected };
  const hasViews = views !== null && views.list.length > 0;

  // Les vues n'ont pas pu être lues (refus de droit, cible hors ligne) : rien à parcourir, le refus le dit.
  if (views?.error != null) {
    return (
      <div className="explorer explorer-failed">
        <Failure error={views.error} gesture={gesture} />
      </div>
    );
  }
  return (
    <div className={hasViews ? "explorer explorer-with-views" : "explorer"}>
      {hasViews && (
        <div className="explorer-views" role="listbox" aria-label={viewsLabel}>
          <span className="explorer-label">{viewsLabel}</span>
          {views.list.map((candidate) => (
            <button key={candidate.id} type="button" role="option" aria-selected={candidate.id === viewID} className="explorer-view" onClick={() => chooseView(candidate.id)}>
              <b>{candidate.label}</b>
              <span>{candidate.hint}</span>
            </button>
          ))}
        </div>
      )}
      <div className="explorer-main">
        <div className="explorer-bar">
          <Button small variant="ghost" disabled={folder === "/"} onClick={() => go(parentPath(folder))}>
            {t("explorer.up")}
          </Button>
          <nav className="explorer-crumbs" aria-label={t("explorer.path")}>
            <button type="button" className="explorer-crumb" onClick={() => go("/")}>
              {rootLabel}
            </button>
            {crumbsOf(folder).map((crumb) => (
              <span key={crumb.path} className="explorer-crumb-item">
                <span className="explorer-sep" aria-hidden="true">
                  ›
                </span>
                <button type="button" className="explorer-crumb" onClick={() => go(crumb.path)}>
                  {crumb.name}
                </button>
              </span>
            ))}
          </nav>
        </div>
        {current?.error != null && (
          <div className="explorer-note">
            <Failure error={current.error} gesture={gesture} />
          </div>
        )}
        {ready && current === null && <p className="explorer-empty secondary">{t("explorer.loading")}</p>}
        {current?.listing != null && entries.length === 0 && <p className="explorer-empty secondary">{t("explorer.empty")}</p>}
        {entries.length > 0 && (
          <ul ref={list} className="explorer-list" role="listbox" aria-multiselectable="true" aria-label={t("explorer.list")} aria-activedescendant={activeID} tabIndex={0} onKeyDown={onKeyDown}>
            {entries.map((entry, index) => (
              <li
                key={entry.name}
                id={`${listID}-${index}`}
                role="option"
                aria-selected={selected.includes(entry.name)}
                className={index === cursor ? "explorer-row explorer-row-cursor" : "explorer-row"}
                onClick={() => setCursor(index)}
                onDoubleClick={() => open(entry)}
              >
                <input type="checkbox" tabIndex={-1} checked={selected.includes(entry.name)} aria-label={t("explorer.choose", entry.name)} onChange={() => setSelected(toggleName(selected, entry.name))} />
                <Icon name={entry.kind === "dir" ? "folder" : "file"} />
                {entry.kind === "dir" ? (
                  <button type="button" className="explorer-name" tabIndex={-1} onClick={() => open(entry)}>
                    {entry.name}
                  </button>
                ) : (
                  <span className="explorer-name">{entry.name}</span>
                )}
                <span className="explorer-size mono">{entry.kind === "file" ? formatBytes(t, language, entry.size) : "—"}</span>
                <span className="explorer-date secondary">{entry.modified === "" ? "" : formatWhen(entry.modified, Date.now())}</span>
              </li>
            ))}
          </ul>
        )}
        {current?.listing?.truncated === true && (
          <div className="explorer-note">
            <Note tone="warn">{t("explorer.truncated")}</Note>
          </div>
        )}
        {view !== null || !hasViews ? (
          <div className="explorer-outputs">
            <span className="explorer-label">
              {t("explorer.outputs")} · {counted(t, "explorer.chosen", selected.length)}
            </span>
            {outputs.map((output) => (
              <div key={output.id} className="explorer-output">
                <b>{output.label}</b>
                {output.render(selection)}
              </div>
            ))}
          </div>
        ) : null}
        <p className="explorer-hint secondary">{t("explorer.hint")}</p>
      </div>
    </div>
  );
}
