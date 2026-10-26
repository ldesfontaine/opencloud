import type { ProxmoxGuest } from "../../api/proxmox";
import { Checkbox, Checks } from "../../components/Field";
import { Select } from "../../components/Select";
import { useT } from "../../i18n/context";
import { selectableGuests, selectorIds, toggleSelectorId } from "../../lib/backups";
import { kindLabel } from "../../lib/proxmox";
import "./GuestSelector.scss";

// Le choix de ce qu'un plan sauvegarde sur un hôte Proxmox connu : tous les
// invités, ou ceux qu'on coche (numéro, nom, type). Le sélecteur reste le
// texte du serveur : « all », ou les numéros séparés par des virgules. Un
// numéro du sélecteur que le relevé ne connaît plus reste coché, pour pouvoir
// le décocher.
export function GuestSelector({ id, guests, selector, onChange }: { id: string; guests: readonly ProxmoxGuest[]; selector: string; onChange: (selector: string) => void }) {
  const t = useT();
  const isAll = selector === "all";
  const listed = selectableGuests(guests);
  const unknown = selectorIds(selector).filter((vmid) => !listed.some((guest) => guest.vmid === vmid));
  return (
    <div className="guest-selector">
      <Select
        id={id}
        value={isAll ? "all" : "some"}
        options={[
          { value: "all", label: t("backup.plan_selector_all") },
          { value: "some", label: t("backup.plan_selector_some") },
        ]}
        onChange={(chosen) => onChange(chosen === "all" ? "all" : "")}
      />
      {!isAll && (
        <>
          <Checks>
            {listed.map((guest) => (
              <Checkbox
                key={guest.vmid}
                label={t("backup.plan_selector_guest", `${guest.vmid} ${guest.name}`, kindLabel(t, guest.kind))}
                checked={selectorIds(selector).includes(guest.vmid)}
                onChange={(checked) => onChange(toggleSelectorId(selector, guest.vmid, checked))}
              />
            ))}
            {unknown.map((vmid) => (
              <Checkbox key={vmid} label={String(vmid)} checked onChange={(checked) => onChange(toggleSelectorId(selector, vmid, checked))} />
            ))}
          </Checks>
          {selector === "" && <p className="secondary">{t("backup.plan_selector_none_checked")}</p>}
        </>
      )}
    </div>
  );
}
