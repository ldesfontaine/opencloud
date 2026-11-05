import { useState } from "react";

import type { Current } from "../api/resources";
import { clearMachineThreshold, saveMachineThreshold, useMachineThresholds, type ThresholdRule } from "../api/thresholds";
import { useAction } from "../hooks/useAction";
import { useT } from "../i18n/context";
import { resourceOrder, ruleSentence } from "../lib/thresholds";
import { meterTone, percentOf } from "../lib/units";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, Meter, Note } from "./Card";
import { Failure } from "./Failure";
import { Pill } from "./Pill";
import { ThresholdForm } from "./ThresholdForm";
import "./MachineThresholds.scss";

// Les seuils des alertes d'une machine, sur sa fiche : une tuile par
// ressource, qui dit la règle en vigueur et d'où elle vient (le réglage
// global, ou les seuils propres à la machine). « Plus de tolérance pour cette
// machine » pose une surcharge ; « Suivre le réglage global » la retire.
export function MachineThresholds({ machineID, current }: { machineID: string; current: Current | null }) {
  const t = useT();
  const rules = useMachineThresholds(machineID);
  const sample = current?.available ? current.sample : null;
  const percents = {
    cpu: sample ? Math.round(sample.cpu_percent) : null,
    memory: sample ? percentOf(sample.mem_used, sample.mem_total) : null,
    disk: sample ? percentOf(sample.disk_used, sample.disk_total) : null,
  };
  return (
    <Card>
      <CardHeader title={t("thresholds.machine_title")} />
      <CardBody gap={12}>
        {rules.error !== null && rules.data === null ? (
          <Failure error={rules.error} />
        ) : rules.data === null ? null : (
          <>
            {rules.data.rules.every((rule) => rule.origin !== "machine") && <Note tone="neutral">{t("thresholds.machine_all_follow")}</Note>}
            <div className="machine-thresholds">
              {resourceOrder.map((resource) => {
                const rule = rules.data?.rules.find((candidate) => candidate.resource === resource);
                return rule === undefined ? null : <ThresholdTile key={resource} machineID={machineID} rule={rule} percent={percents[resource]} reload={rules.reload} />;
              })}
            </div>
          </>
        )}
      </CardBody>
    </Card>
  );
}

function ThresholdTile({ machineID, rule, percent, reload }: { machineID: string; rule: ThresholdRule; percent: number | null; reload: () => void }) {
  const t = useT();
  const [isEditing, setEditing] = useState(false);
  const clear = useAction();
  const hasOwn = rule.origin === "machine";
  return (
    <section className={isEditing ? "threshold-tile threshold-tile-editing" : "threshold-tile"} aria-label={t(`thresholds.resource_${rule.resource}`)}>
      <div className="threshold-tile-head">
        <span className="card-t">{t(`thresholds.resource_${rule.resource}`)}</span>
        <Pill tone={hasOwn ? "accent" : "neutral"}>{t(hasOwn ? "thresholds.origin_own" : "thresholds.origin_global")}</Pill>
      </div>
      <p className="threshold-sentence">{ruleSentence(t, rule)}</p>
      {percent !== null && <Meter percent={percent} tone={meterTone(percent, { attention: rule.attention, danger: rule.danger })} />}
      {hasOwn && rule.global !== undefined && <p className="secondary">{t("thresholds.global_is", ruleSentence(t, rule.global))}</p>}
      {isEditing ? (
        <ThresholdForm
          rule={rule}
          idPrefix={`machine-threshold-${rule.resource}`}
          save={(request) => saveMachineThreshold(machineID, rule.resource, request)}
          saveLabelKey="thresholds.save_machine"
          onCancel={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            reload();
          }}
        />
      ) : (
        <div className="threshold-tile-actions">
          <Button small icon="settings" onClick={() => setEditing(true)}>
            {t(hasOwn ? "thresholds.edit_machine" : "thresholds.more_tolerance")}
          </Button>
          {hasOwn && (
            <Button
              small
              disabled={clear.busy}
              onClick={() => clear.run(() => clearMachineThreshold(machineID, rule.resource), { onSettled: reload })}
            >
              {t("thresholds.follow_global")}
            </Button>
          )}
        </div>
      )}
      {clear.error !== null && <Failure error={clear.error} />}
    </section>
  );
}
