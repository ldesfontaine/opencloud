// Galerie du système visuel : une page d'atelier, pas un écran de
// l'opérateur. Ses exemples (« Debian 12 », « nextcloud ») sont des
// valeurs de démonstration en dur, l'exception assumée à la règle du
// catalogue.
import { useState } from "react";

import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, EmptyLine, KeyValue, Meter, Stat } from "../components/Card";
import { ConfirmInline } from "../components/ConfirmInline";
import { CardGrid, EntityCard } from "../components/EntityCard";
import { Field, Input, Select } from "../components/Field";
import { Icon } from "../components/Icon";
import type { MenuAction } from "../components/MoreMenu";
import { PageHead } from "../components/PageHead";
import { Dot, Pill } from "../components/Pill";
import { SectionHead } from "../components/SectionHead";
import { Select } from "../components/Select";
import { DangerZone, SettingFoot, SettingRow } from "../components/SettingRow";
import { List, RowLink, RowText, RowWhen, Table } from "../components/Table";
import { ViewToggle } from "../components/ViewToggle";
import { useViewMode } from "../hooks/useViewMode";
import { useI18n, type Translate } from "../i18n/context";
import "./VisualSystem.scss";

const swatches = [
  ["bg", "#f6f6f8 · #0d0e12"],
  ["surface", "#ffffff · #15171c"],
  ["surface-2", "#f1f2f5 · #1c1f26"],
  ["line", "#e5e7ec · #262a33"],
  ["ink", "#101216 · #eef0f4"],
  ["ink-2", "#4b5160 · #b3b8c4"],
  ["muted", "#7a8194 · #7d8494"],
  ["accent", "#5b5be6 · #7c7cf0"],
  ["ok", "#1f9d57 · #3ac776"],
  ["warn", "#d78d0c · #f0a92a"],
  ["danger", "#de4350 · #f06470"],
  ["primary", "#101216 · #eef0f4"],
] as const;

// Douze machines : au-delà de huit, le menu ouvert porte sa recherche.
const demoMachines = [
  "vps-paris-1",
  "vps-lyon-2",
  "vps-lille-1",
  "nas-maison",
  "pve-atelier",
  "pve-garage",
  "rpi-salon",
  "rpi-bureau",
  "vps-roubaix-3",
  "vps-gravelines-1",
  "backup-froid",
  "lab-test",
];

function machineOptions(names: readonly string[]) {
  return names.map((name) => ({ value: name, label: name }));
}

// La vitrine de la direction artistique : chaque composant, une fois, pour
// vérifier le clair, le sombre et les deux langues d'un coup d'œil.
export function VisualSystem() {
  const { t, version } = useI18n();
  const [machine, setMachine] = useState("vps-paris-1");
  const [manyMachine, setManyMachine] = useState("vps-lyon-2");
  return (
    <>
      <PageHead
        title={t("visual.title")}
        subtitle={t("visual.subtitle")}
        actions={
          <Pill tone="neutral" dot={false}>
            {version}
          </Pill>
        }
      />
      <div className="grid-2 gap-40">
        <div className="stack gap-24">
          <section>
            <h2 className="side-label section-label">{t("visual.colors.title")}</h2>
            <div className="grid-4 gap-14">
              {swatches.map(([name, values]) => (
                <div className="swatch-item" key={name}>
                  <div className={`swatch sw-${name}`} />
                  <b>{t(`visual.colors.${name.replace("-", "_")}`)}</b>
                  <span className="mono muted">--{name}</span>
                  <span className="mono muted">{values}</span>
                </div>
              ))}
            </div>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.type.title")}</h2>
            <Card>
              <CardBody gap={12}>
                <div className="kv">
                  <span className="type-h1">{t("visual.type.h1")}</span>
                  <span className="mono muted">22 / 600 / -0.02em</span>
                </div>
                <div className="kv">
                  <span className="card-t">{t("visual.type.card")}</span>
                  <span className="mono muted">14 / 600</span>
                </div>
                <div className="kv">
                  <span>{t("visual.type.body")}</span>
                  <span className="mono muted">13 / 400 / 1.45</span>
                </div>
                <div className="kv">
                  <span className="secondary">{t("visual.type.secondary")}</span>
                  <span className="mono muted">{t("visual.type.secondary_spec")}</span>
                </div>
                <div className="kv">
                  <span className="side-label inline-label">{t("visual.type.label")}</span>
                  <span className="mono muted">{t("visual.type.label_spec")}</span>
                </div>
                <div className="kv">
                  <span className="mono">51.15.20.114 · nextcloud:29.0.4</span>
                  <span className="mono muted">Geist Mono 12</span>
                </div>
                <div className="kv">
                  <span className="stat-value">
                    3,1<small>Go</small>
                  </span>
                  <span className="mono muted">{t("visual.type.stat")} 26 / 600 / -0.03em</span>
                </div>
              </CardBody>
            </Card>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.spacing.title")}</h2>
            <Card>
              <CardBody gap={10}>
                <KeyValue label={t("visual.spacing.card")} value={t("visual.spacing.card_value")} mono />
                <KeyValue label={t("visual.spacing.control")} value={t("visual.spacing.control_value")} mono />
                <KeyValue label={t("visual.spacing.pill")} value={t("visual.spacing.pill_value")} mono />
                <KeyValue label={t("visual.spacing.grid")} value="4 · 8 · 12 · 16 · 20 · 24 · 32" mono />
                <KeyValue label={t("visual.spacing.sidebar")} value={t("visual.spacing.sidebar_value")} mono />
                <KeyValue label={t("visual.spacing.icons")} value={t("visual.spacing.icons_value")} mono />
                <KeyValue label={t("visual.spacing.shadow")} value={t("visual.spacing.shadow_value")} mono />
              </CardBody>
            </Card>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.stats.title")}</h2>
            <div className="grid-2 gap-16">
              <Stat icon="cpu" label={t("visual.stats.cpu")} value="23" unit="%" meter={<Meter percent={23} />} foot={t("visual.stats.cpu_foot")} />
              <Stat
                icon="disk"
                label={t("visual.stats.disk")}
                value="69"
                unit={t("visual.stats.disk_unit")}
                meter={<Meter percent={86} tone="warn" />}
                foot={
                  <>
                    <b>86 %</b>
                    {t("visual.stats.disk_foot")}
                  </>
                }
              />
              <Stat compact icon="cpu" label={t("visual.stats.cpu")} value="23" unit="%" foot={t("visual.stats.cpu_foot")} />
              <Stat
                compact
                icon="disk"
                label={t("visual.stats.disk")}
                value="69"
                unit={t("visual.stats.disk_unit")}
                foot={
                  <>
                    <b>86 %</b>
                    {t("visual.stats.disk_foot")}
                  </>
                }
              />
              <Stat
                compact
                wideFoot
                icon="disk"
                label={t("visual.stats.disk")}
                value="69"
                unit={t("visual.stats.disk_unit")}
                foot={
                  <>
                    <b>86 %</b>
                    {t("visual.stats.disk_foot")}
                  </>
                }
              />
            </div>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.table.title")}</h2>
            <Card>
              <Table fit>
                <thead>
                  <tr>
                    <th>{t("visual.table.service")}</th>
                    <th>{t("visual.table.image")}</th>
                    <th>{t("visual.table.state")}</th>
                  </tr>
                </thead>
                <tbody>
                  <SampleRow name="nextcloud" image="nextcloud:29.0.4" tone="ok" state={t("state.active")} />
                  <SampleRow name="vaultwarden" image="vaultwarden/server:1.32" tone="warn" state={t("state.to_renew")} />
                  <SampleRow name="matomo" image="matomo:5.1" tone="neutral" state={t("state.stopped")} />
                </tbody>
              </Table>
            </Card>
          </section>

          <CardsSample />
        </div>

        <div className="stack gap-24">
          <section>
            <h2 className="side-label section-label">{t("visual.buttons.title")}</h2>
            <div className="cluster">
              <Button variant="primary" icon="plus">
                {t("visual.buttons.primary")}
              </Button>
              <Button icon="refresh">{t("visual.buttons.secondary")}</Button>
              <Button variant="ghost">{t("visual.buttons.ghost")}</Button>
              <Button variant="danger" icon="x">
                {t("action.delete")}
              </Button>
              <Button variant="accent">{t("visual.buttons.accent")}</Button>
              <Button small>{t("visual.buttons.small")}</Button>
              <Button icon="more" aria-label={t("action.more")} />
              <Button variant="primary" disabled>
                {t("visual.buttons.disabled")}
              </Button>
            </div>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.states.title")}</h2>
            <div className="cluster">
              <Pill tone="ok">{t("state.online")}</Pill>
              <Pill tone="ok">{t("state.active")}</Pill>
              <Pill tone="warn">{t("state.to_renew")}</Pill>
              <Pill tone="danger">{t("state.failed")}</Pill>
              <Pill tone="neutral">{t("state.stopped")}</Pill>
              <Pill tone="accent">{t("state.proxy")}</Pill>
              <Pill tone="neutral" dot={false}>
                {version}
              </Pill>
              <Pill tone="danger" dot={false}>
                {t("visual.states.open_count")}
              </Pill>
            </div>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.fields.title")}</h2>
            <div className="grid-2 gap-14">
              <Field label={t("visual.fields.service_name")} htmlFor="vs-name">
                <Input id="vs-name" type="text" placeholder="nextcloud" />
              </Field>
              <Field label={t("visual.fields.domain")} htmlFor="vs-domain">
                <Input id="vs-domain" mono type="text" defaultValue="cloud.exemple.fr" />
              </Field>
              <Field label={t("machine.select")} htmlFor="vs-machine">
                <Select id="vs-machine" icon="server" value={machine} options={machineOptions(demoMachines.slice(0, 2))} onChange={setMachine} />
              </Field>
              <Field label={t("visual.fields.many_machines")} htmlFor="vs-many-machines" help={t("visual.fields.many_machines_help")}>
                <Select id="vs-many-machines" icon="server" value={manyMachine} options={machineOptions(demoMachines)} onChange={setManyMachine} />
              </Field>
              <Field label={t("visual.fields.filter")}>
                <span className="seg" role="radiogroup" aria-label={t("visual.fields.filter")}>
                  <label>
                    <input type="radio" name="vs-filter" defaultChecked />
                    <span>{t("visual.fields.all")}</span>
                  </label>
                  <label>
                    <input type="radio" name="vs-filter" />
                    <span>{t("visual.fields.active")}</span>
                  </label>
                  <label>
                    <input type="radio" name="vs-filter" />
                    <span>{t("visual.fields.stopped")}</span>
                  </label>
                </span>
              </Field>
              <Field label={t("visual.fields.auto_renew")}>
                <div className="cluster control-row">
                  <input type="checkbox" className="toggle" role="switch" defaultChecked aria-label={t("visual.fields.enabled")} />
                  <input type="checkbox" className="toggle" role="switch" aria-label={t("visual.fields.disabled")} />
                </div>
              </Field>
              <Field label={t("visual.fields.tooltip")}>
                <div className="control-row">
                  <Button small data-tip={t("visual.fields.tooltip_text")}>
                    {t("action.hover")}
                  </Button>
                </div>
              </Field>
            </div>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.settings.title")}</h2>
            <SectionHead id="vs-settings-title" title={t("settings.section_alerts")} text={t("settings.disk_intro")} />
            <Card className="setting-stack">
              <SettingRow label={t("settings.disk_attention_label")} description={t("settings.disk_percent_help", 50, 99)} htmlFor="vs-attention" width="short">
                <Input id="vs-attention" type="number" defaultValue={85} />
              </SettingRow>
              <SettingRow label={t("settings.mail_host_label")} description={t("settings.mail_host_help")} htmlFor="vs-host" width="medium">
                <Input id="vs-host" mono type="text" defaultValue="smtp.exemple.fr" />
              </SettingRow>
              <SettingFoot note={t("settings.disk_applies")}>
                <Button variant="primary" icon="check" disabled>
                  {t("settings.disk_save")}
                </Button>
              </SettingFoot>
              <DangerZone>
                <SettingRow label={t("auth.title")}>
                  <ConfirmInline variant="danger" icon="x" question={t("auth.disable_confirm")} onConfirm={() => undefined}>
                    {t("auth.disable")}
                  </ConfirmInline>
                </SettingRow>
              </DangerZone>
            </Card>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.nav.title")}</h2>
            <div className="grid-nav-nodes">
              <nav className="nav">
                <span className="nav-item">
                  <Icon name="grid" />
                  <span>{t("visual.nav.rest")}</span>
                </span>
                <span className="nav-item active">
                  <Icon name="server" />
                  <span>{t("visual.nav.active")}</span>
                  <span className="count">3</span>
                </span>
                <span className="nav-item">
                  <Icon name="bell" />
                  <span>{t("visual.nav.alerting")}</span>
                  <span className="count hot">2</span>
                </span>
              </nav>
              <div className="stack gap-10">
                <div className="node">
                  <span className="ico">
                    <Icon name="box" />
                  </span>
                  <span className="t">
                    <b>nextcloud</b>
                    <span>cloud.exemple.fr</span>
                  </span>
                  <Dot tone="ok" />
                </div>
                <div className="node sel">
                  <span className="ico">
                    <Icon name="box" />
                  </span>
                  <span className="t">
                    <b>nextcloud</b>
                    <span>{t("visual.nav.selected")}</span>
                  </span>
                  <Dot tone="ok" />
                </div>
                <div className="node edge-node">
                  <span className="ico">
                    <Icon name="globe" />
                  </span>
                  <span className="t">
                    <b>{t("visual.nav.internet")}</b>
                    <span>{t("visual.nav.proxy_entry")}</span>
                  </span>
                </div>
              </div>
            </div>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.machine.title")}</h2>
            <Card>
              <CardBody gap={16}>
                <div className="machine-head">
                  <Select icon="server" aria-label={t("machine.select")} value={machine} options={machineOptions(demoMachines.slice(0, 2))} onChange={setMachine} />
                  <Pill tone="ok">{t("state.online")}</Pill>
                  <div className="meta">
                    <span className="mono">51.15.20.114</span>
                    <span className="sep" />
                    <span>Debian 12</span>
                    <span className="sep" />
                    <span>
                      {t("machine.agent")} <span className="mono">0.0.1</span>
                    </span>
                    <span className="sep" />
                    <span>{t("machine.seen_ago", "12 s")}</span>
                  </div>
                </div>
                <nav className="tabs">
                  <span className="tab active">{t("tab.summary")}</span>
                  <span className="tab">
                    {t("tab.services")}
                    <span className="n">6</span>
                  </span>
                  <span className="tab">
                    {t("tab.availability")}
                    <span className="n">5</span>
                  </span>
                  <span className="tab">{t("tab.network")}</span>
                  <span className="tab">{t("tab.backups")}</span>
                  <span className="tab">{t("tab.logs")}</span>
                  <span className="tab">{t("tab.settings")}</span>
                </nav>
              </CardBody>
            </Card>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.events.title")}</h2>
            <Card>
              <CardHeader title={t("visual.events.card_title")} aside={t("visual.events.card_sub")} />
              <List>
                <div className="row">
                  <Dot tone="ok" />
                  <RowText title={t("visual.events.e1")}>{t("visual.events.e1_sub")}</RowText>
                  <RowWhen>{t("visual.events.e1_when")}</RowWhen>
                </div>
                <div className="row">
                  <Dot tone="warn" />
                  <RowText title={t("visual.events.e2")}>{t("visual.events.e2_sub")}</RowText>
                  <RowWhen>{t("visual.events.e2_when")}</RowWhen>
                </div>
                <div className="row">
                  <Dot tone="neutral" />
                  <RowText title={t("visual.events.e3")}>{t("visual.events.e3_sub")}</RowText>
                  <RowWhen>{t("visual.events.e3_when")}</RowWhen>
                </div>
              </List>
            </Card>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.empty.title")}</h2>
            <Card>
              <Empty icon="archive" title={t("visual.empty.heading")} text={t("visual.empty.text")} />
            </Card>
          </section>

          <section>
            <h2 className="side-label section-label">{t("visual.tone.title")}</h2>
            <Card>
              <CardBody gap={8}>
                <div>
                  {t("visual.tone.intro")} <b className="medium">{t("visual.tone.verb_1")}</b>, <b className="medium">{t("visual.tone.verb_2")}</b>,{" "}
                  <b className="medium">{t("visual.tone.verb_3")}</b>.
                </div>
                <div className="muted">{t("visual.tone.rules")}</div>
              </CardBody>
            </Card>
          </section>
        </div>
      </div>
    </>
  );
}

// Les liens de la vitrine restent sur la vitrine, ses actions ne font rien.
const samplePath = "/systeme-visuel";

function sampleActions(t: Translate): MenuAction[] {
  const nothing = () => undefined;
  return [
    { label: t("visual.table.logs"), to: samplePath },
    { label: t("visual.table.restart"), onSelect: nothing },
    { label: t("action.delete"), danger: true, onSelect: nothing },
  ];
}

function SampleRow({ name, image, tone, state }: { name: string; image: string; tone: "ok" | "warn" | "neutral"; state: string }) {
  const { t } = useI18n();
  return (
    <tr>
      <td>
        <RowLink to={samplePath} icon="box" actions={sampleActions(t)}>
          {name}
        </RowLink>
      </td>
      <td className="num">{image}</td>
      <td>
        <Pill tone={tone}>{state}</Pill>
      </td>
    </tr>
  );
}

// Les cartes d'objet, leur bascule vers le tableau, et l'état vide compact.
function CardsSample() {
  const { t } = useI18n();
  const [mode, setMode] = useViewMode("visual", "cards");
  const actions = sampleActions(t);
  return (
    <section>
      <div className="vs-cards-head">
        <h2 className="side-label section-label">{t("visual.cards.title")}</h2>
        <ViewToggle mode={mode} onChange={setMode} />
      </div>
      <div className="stack gap-16">
        {mode === "cards" ? (
          <CardGrid>
            <EntityCard
              to={samplePath}
              icon="server"
              name="vps-paris-1"
              kind={t("visual.cards.kind_machine")}
              state={<Pill tone="ok">{t("state.online")}</Pill>}
              facts={[
                { label: t("visual.cards.address"), value: "51.15.20.4" },
                { label: t("visual.cards.os"), value: "Debian 12" },
                { label: t("visual.cards.agent"), value: "v0.0.1" },
                { label: t("visual.cards.seen"), value: t("visual.cards.seen_value"), mono: false },
              ]}
              meters={[
                { label: t("visual.cards.cpu"), value: "23 %", percent: 23 },
                { label: t("visual.cards.disk"), value: "71 %", percent: 71, tone: "warn" },
              ]}
              actions={actions}
            />
            <EntityCard
              to={samplePath}
              icon="server"
              name="vps-lyon-2"
              kind={t("visual.cards.kind_machine")}
              state={<Pill tone="danger">{t("state.offline")}</Pill>}
              facts={[
                { label: t("visual.cards.address"), value: "62.210.8.17" },
                { label: t("visual.cards.os"), value: "Ubuntu 24.04" },
                { label: t("visual.cards.agent"), value: "v0.0.1" },
                { label: t("visual.cards.seen"), value: t("visual.cards.seen_old"), mono: false },
              ]}
              actions={actions}
            />
            <EntityCard
              to={samplePath}
              icon="globe"
              name="statut.exemple.fr"
              kind={t("visual.cards.kind_page")}
              state={<Pill tone="ok">{t("state.online")}</Pill>}
              facts={[
                { label: t("visual.cards.probes"), value: "4" },
                { label: t("visual.cards.uptime"), value: "99,98 %" },
              ]}
            />
          </CardGrid>
        ) : (
          <Card>
            <Table fit>
              <thead>
                <tr>
                  <th>{t("visual.cards.kind_machine")}</th>
                  <th>{t("visual.cards.address")}</th>
                  <th>{t("visual.table.state")}</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>
                    <RowLink to={samplePath} icon="server" actions={actions}>
                      vps-paris-1
                    </RowLink>
                  </td>
                  <td className="num">51.15.20.4</td>
                  <td>
                    <Pill tone="ok">{t("state.online")}</Pill>
                  </td>
                </tr>
                <tr>
                  <td>
                    <RowLink to={samplePath} icon="server" actions={actions}>
                      vps-lyon-2
                    </RowLink>
                  </td>
                  <td className="num">62.210.8.17</td>
                  <td>
                    <Pill tone="danger">{t("state.offline")}</Pill>
                  </td>
                </tr>
              </tbody>
            </Table>
          </Card>
        )}
        <Card>
          <EmptyLine
            text={t("visual.cards.empty")}
            action={
              <Button variant="primary" small icon="plus">
                {t("visual.cards.empty_action")}
              </Button>
            }
          />
        </Card>
      </div>
    </section>
  );
}
