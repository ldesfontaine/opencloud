import type { NetworkMap } from "../../api/network";
import { useT } from "../../i18n/context";
import { useNow } from "../../hooks/useNow";
import { freshnessText, proofTone } from "../../lib/links";
import { serviceIndex } from "../../lib/netmap";
import { Disclosure } from "../Disclosure";
import { Pill } from "../Pill";
import { Table } from "../Table";

// Les mêmes liens que ceux du dessin, en liste : ce que le survol dit se lit
// ici sans souris, et sur un écran étroit où les traits se croisent.
export function LinkList({ map }: { map: NetworkMap }) {
  const t = useT();
  const now = useNow(60_000);
  const spots = serviceIndex(map);
  // À moins de 860 px les traits sont cachés : la liste s'ouvre d'elle-même.
  const narrow = window.matchMedia("(max-width: 860px)").matches;
  const name = (id: string) => {
    const spot = spots.get(id);
    return spot === undefined ? id : `${spot.service.label} · ${spot.box.name}`;
  };
  return (
    <Disclosure title={t("network.map_links_title", map.links.length)} initiallyOpen={narrow}>
      {map.links.length === 0 ? (
        <p className="secondary netmap-links-empty">{t("network.map_links_empty")}</p>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("network.map_col_from")}</th>
              <th>{t("network.map_col_to")}</th>
              <th>{t("link.col_proof")}</th>
              <th>{t("link.col_fresh")}</th>
            </tr>
          </thead>
          <tbody>
            {map.links.map((link) => (
              <tr key={link.id}>
                <td>{name(link.from)}</td>
                <td>
                  <span className="stack">
                    <span>{name(link.to)}</span>
                    <span className="secondary">
                      {t(`link.nature_${link.nature}`)}
                      {link.port > 0 && ` · ${t("link.port", link.port)}`}
                    </span>
                  </span>
                </td>
                <td>
                  <span className="stack">
                    <span className="cluster">
                      <Pill tone={proofTone(link.proof)}>{t(`link.proof_${link.proof}`)}</Pill>
                      {link.proposed && (
                        <Pill tone="warn" dot={false}>
                          {t("network.map_link_proposed")}
                        </Pill>
                      )}
                    </span>
                    <span className="secondary">{t(`link.signal_${link.signal}`)}</span>
                  </span>
                </td>
                <td>
                  <span className="cluster">
                    <span className="secondary">{link.proposed ? t("link.deduced_fresh") : freshnessText(t, { ...link, dismissed: false }, now)}</span>
                    {link.stale && (
                      <Pill tone="warn" dot={false}>
                        {t("link.stale")}
                      </Pill>
                    )}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Disclosure>
  );
}
