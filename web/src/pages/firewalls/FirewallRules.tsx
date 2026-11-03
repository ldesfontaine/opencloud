import { useState } from "react";

import type { FirewallInterface, FirewallRule } from "../../api/firewalls";
import { Card, CardHeader } from "../../components/Card";
import { Choice } from "../../components/Choice";
import { Icon } from "../../components/Icon";
import { Input } from "../../components/Field";
import { Pill } from "../../components/Pill";
import { Table } from "../../components/Table";
import { Button } from "../../components/Button";
import { useT } from "../../i18n/context";
import { counted } from "../../lib/counted";
import { directionLabel, endpointLabel, filterRules, groupRulesByInterface, ruleActionLabel, ruleActionTone, ruleFilters, ruleTotals, type RuleFilter, type RuleGroup } from "../../lib/firewalls";

// Les règles groupées par interface, repliées, avec une recherche et trois
// filtres. Chercher ou filtrer ouvre les groupes qui ont une réponse et
// cache les autres ; sinon, c'est l'opérateur qui ouvre.
export function FirewallRules({
  rules,
  interfaces,
  opened,
  onToggle,
}: {
  rules: FirewallRule[];
  interfaces: FirewallInterface[];
  opened: ReadonlySet<string>;
  onToggle: (name: string, isOpen: boolean) => void;
}) {
  const t = useT();
  const [filter, setFilter] = useState<RuleFilter>("all");
  const [query, setQuery] = useState("");
  const totals = ruleTotals(rules);
  const isFiltering = filter !== "all" || query.trim() !== "";
  const shown = filterRules(rules, filter, query);
  const groups = groupRulesByInterface(shown, interfaces);
  const clear = () => {
    setFilter("all");
    setQuery("");
  };
  return (
    <div id="regles">
      <Card>
        <CardHeader title={t("firewall.rules_title")} aside={isFiltering ? t("firewall.rules_shown", shown.length, rules.length) : rules.length} />
        <div className="firewall-tools">
          <span className="firewall-search">
            <Icon name="search" />
            <Input
              type="search"
              value={query}
              maxLength={200}
              placeholder={t("firewall.rules_search")}
              aria-label={t("firewall.rules_search")}
              onChange={(event) => setQuery(event.target.value)}
            />
          </span>
          <Choice
            label={t("firewall.rules_kind")}
            name="firewall-rule-kind"
            choices={ruleFilters}
            value={filter}
            text={(choice) => t(`firewall.rules_filter_${choice}`)}
            count={(choice) => totals[choice]}
            onChange={setFilter}
          />
        </div>
        {groups.length === 0 && (
          <div className="firewall-no-match">
            <span className="secondary">{t("firewall.rules_no_match")}</span>
            <Button small onClick={clear}>
              {t("firewall.rules_clear")}
            </Button>
          </div>
        )}
        {groups.map((group) => (
          <RuleGroupView key={group.name} group={group} isOpen={isFiltering || opened.has(group.name)} onToggle={(isOpen) => onToggle(group.name, isOpen)} />
        ))}
      </Card>
    </div>
  );
}

function RuleGroupView({ group, isOpen, onToggle }: { group: RuleGroup; isOpen: boolean; onToggle: (isOpen: boolean) => void }) {
  const t = useT();
  const totals = ruleTotals(group.rules);
  const label = group.name === "" ? t("firewall.rules_floating") : group.label;
  return (
    <details id={`regles-${group.name}`} className="firewall-group" open={isOpen} onToggle={(event) => onToggle(event.currentTarget.open)}>
      <summary>
        <span className="fold-chevron">
          <Icon name="chevron-down" />
        </span>
        <b>{label}</b>
        {group.name !== "" && group.name !== label && <span className="mono secondary">{group.name}</span>}
        <span className="firewall-group-counts">
          <Pill tone="neutral" small dot={false}>
            {counted(t, "firewall.rules_count", totals.all)}
          </Pill>
          {totals.block > 0 && (
            <Pill tone="danger" small dot={false}>
              {counted(t, "firewall.rules_block", totals.block)}
            </Pill>
          )}
        </span>
      </summary>
      <div className="scroll-x">
        <Table>
          <thead>
            <tr>
              <th>{t("firewall.col_action")}</th>
              <th className="wide-only">{t("firewall.col_direction")}</th>
              <th className="wide-only">{t("firewall.col_protocol")}</th>
              <th>{t("firewall.col_source")}</th>
              <th>{t("firewall.col_destination")}</th>
              <th className="wide-only">{t("firewall.col_description")}</th>
            </tr>
          </thead>
          <tbody>
            {group.rules.map((rule, index) => (
              <tr key={index} className={rule.is_enabled ? undefined : "firewall-off"}>
                <td>
                  <Pill tone={ruleActionTone(rule)} small>
                    {ruleActionLabel(t, rule.action)}
                  </Pill>
                  {!rule.is_enabled && <span className="secondary firewall-disabled">{t("firewall.rule_disabled")}</span>}
                </td>
                <td className="wide-only">{directionLabel(t, rule.direction)}</td>
                <td className="num wide-only">{rule.protocol}</td>
                <td className="num">{endpointLabel(rule.source, rule.source_port)}</td>
                <td className="num">{endpointLabel(rule.destination, rule.destination_port)}</td>
                <td className="secondary wide-only">{rule.description}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      </div>
    </details>
  );
}
