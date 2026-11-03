import type { ReactNode } from "react";

import { Icon } from "./Icon";
import "./Matrix.scss";

// La matrice : des lignes groupées, des colonnes, des cases cliquables, une
// légende. Elle ne sait rien de son contenu : la page Sauvegardes y met des
// sources et des cibles ; Clés SSH, droits et zones de pare-feu la réutiliseront.
// Une case absente (rien dans le tableau) est un croisement qui n'existe pas.

export type CellTone = "ok" | "warn" | "danger" | "observed" | "none";

export interface MatrixCell {
  tone: CellTone;
  label: string;
  // Une valeur discrète à droite du libellé (le nombre de copies).
  detail?: string;
}

export interface MatrixColumn {
  id: string;
  label: string;
  hint?: string;
}

interface MatrixRow {
  id: string;
  label: string;
  hint?: string;
  cells: Readonly<Record<string, MatrixCell | undefined>>;
}

export interface MatrixGroup {
  id: string;
  label: string;
  rows: MatrixRow[];
}

export interface MatrixPick {
  row: string;
  column: string;
}

interface Props {
  caption: string;
  rowHeader: string;
  columns: readonly MatrixColumn[];
  groups: readonly MatrixGroup[];
  legend: readonly { tone: CellTone; label: string }[];
  selected: MatrixPick | null;
  onSelect: (pick: MatrixPick) => void;
  // « Source, cible » : le nom complet d'une case pour le lecteur d'écran.
  cellName: (row: MatrixRow, column: MatrixColumn, cell: MatrixCell) => string;
}

export function Matrix({ caption, rowHeader, columns, groups, legend, selected, onSelect, cellName }: Props) {
  return (
    <div className="card matrix">
      <div className="matrix-scroll">
        <table>
          <caption className="matrix-caption">{caption}</caption>
          <thead>
            <tr>
              <th scope="col">{rowHeader}</th>
              {columns.map((column) => (
                <th key={column.id} scope="col">
                  <span className="matrix-col">{column.label}</span>
                  {column.hint !== undefined && <span className="matrix-col-hint">{column.hint}</span>}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {groups.map((group) => (
              <GroupRows key={group.id} group={group} columns={columns} selected={selected} onSelect={onSelect} cellName={cellName} />
            ))}
          </tbody>
        </table>
      </div>
      <ul className="matrix-legend">
        {legend.map((item) => (
          <li key={item.tone}>
            <i className={`matrix-dot matrix-dot-${item.tone}`} aria-hidden="true" />
            {item.label}
          </li>
        ))}
      </ul>
    </div>
  );
}

function GroupRows({ group, columns, selected, onSelect, cellName }: { group: MatrixGroup; columns: readonly MatrixColumn[]; selected: MatrixPick | null; onSelect: (pick: MatrixPick) => void; cellName: Props["cellName"] }): ReactNode {
  return (
    <>
      <tr className="matrix-group">
        <th colSpan={columns.length + 1} scope="colgroup">
          <Icon name="server" /> {group.label}
        </th>
      </tr>
      {group.rows.map((row) => (
        <tr key={row.id}>
          <th scope="row" className="matrix-row">
            {row.label}
            {row.hint !== undefined && <span>{row.hint}</span>}
          </th>
          {columns.map((column) => {
            const cell = row.cells[column.id];
            return (
              <td key={column.id}>
                {cell !== undefined && (
                  <button
                    type="button"
                    className="matrix-cell"
                    aria-pressed={selected?.row === row.id && selected.column === column.id}
                    aria-label={cellName(row, column, cell)}
                    onClick={() => onSelect({ row: row.id, column: column.id })}
                  >
                    <i className={`matrix-dot matrix-dot-${cell.tone}`} aria-hidden="true" />
                    {cell.label}
                    {cell.detail !== undefined && <small>{cell.detail}</small>}
                  </button>
                )}
              </td>
            );
          })}
        </tr>
      ))}
    </>
  );
}
