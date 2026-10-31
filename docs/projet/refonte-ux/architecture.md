# Refonte UX : cadrage d'architecture

> Statut : **proposition** du 31/10/2026 (#387), à valider par Lucas. Elle
> traduit en architecture les décisions du 31/10 (#386). Aucune ligne de code
> de production ne change ici. L'apparence (jetons, polices, tuiles) reste
> celle de la [direction artistique](../21-direction-artistique.md) ; l'anatomie
> d'une page de module reste celle de la
> [ligne directrice des modules](../25-ligne-directrice-modules.md). Ce
> document dit **comment le code y arrive**.

Mesures faites sur `origin/dev` à `095ff98`. Maquette d'agencement de la vue
Machines (esquisse, pas la cible pixel) : [`maquettes/machines-v2.html`](maquettes/machines-v2.html),
[liste](maquettes/machines-v2-liste.png), [fiche](maquettes/machines-v2-fiche.png).

## Les règles qui structurent ce document

| Règle (Lucas) | Ce qu'elle impose ici |
| --- | --- |
| Modulaire, générique, propre ; chaque patron justifié | Chaque point de la cible (§3) nomme son patron, la raison tirée du code actuel, l'alternative écartée |
| Urbanisation | Chaque chantier (§4) liste ce qu'il possède, ce qu'il supprime, ses tests ; aucun nouveau chemin à côté de l'ancien |
| Parité UI / MCP | Une action d'écran = une route d'API ; un test le tient (§3.7) |
| Le thème reste | Aucun jeton de `web/src/styles/_tokens.scss` ne change ; on change l'agencement |
| `{product}` | Le mot-symbole du front passe par la marque (§2.3, chantier C1) |
| Générique | Les modules se dérivent de faits observés, jamais d'une liste de machines connues (§3.1) |

---

## 1. Relevé de l'existant

### 1.1 Le front

| Élément | Fait | Preuve |
| --- | --- | --- |
| Routeur | Une route par écran, adresses en français ; 56 `<Route>` (`grep -c "<Route" web/src/App.tsx`) | `web/src/App.tsx:69-139` |
| Première configuration | Hors du routeur : `FirstSetupGate` enveloppe `App` | `web/src/main.tsx:19` |
| Menu | Deux listes en dur : `viewEntries` (7, section « Vue ») et `settingsEntries` (5, section « Administration ») ; compteurs par `if` sur la clé | `web/src/components/Layout.tsx:147-164`, `:235-264` |
| Marque | `Brand()` écrit `open` + `Cloud` en dur | `web/src/components/Layout.tsx:129-130` ; idem `web/src/statut/StatusPage.tsx:107-108` |
| Onglets de la fiche machine | Liste fixe de 6 + `hyperviseur` si `proxmox.declared` (`visibleMachineTabs(hostsProxmox: boolean)`) | `web/src/lib/machineTabs.ts:8-30` ; `web/src/pages/Machine.tsx:78` |
| Pages | 64 composants `.tsx` sous `web/src/pages`, tests exclus (sous-dossiers `alerts`, `backups`, `settings`, `status`) | `ls web/src/pages` |
| Hooks | `useResource` (lecture relue sur le direct), `useAction` (une écriture, `confirm` natif), `useForm` (valeurs, remplissage unique), `useLive` (un `EventSource`) | `web/src/hooks/useResource.ts:37`, `useAction.ts:36,49`, `useForm.ts:44`, `useLive.ts` |
| Appels d'API | Chemins écrits en chaîne dans `web/src/api/*.ts`, un `call(method, path, body)` | `web/src/api/client.ts:14-30` ; ex. `web/src/api/proxmox.ts:111-113` |
| i18n | Catalogues `internal/lang/{fr,en}.toml`, 2566 clés chacun ; servis par `GET /api/i18n/{code}`, marqueurs (`{product}`…) remplacés au chargement | `internal/lang/lang.go:79-133` ; `web/src/i18n/context.tsx:34-72` |
| Garde des clés | Toute clé lue existe dans les deux langues ; toute clé a un lecteur | `internal/server/frontend_keys_test.go:73` ; `internal/server/catalog_readers_test.go:105` |
| Contrat d'API | Les clés des goldens Go = les clés des types TS | `web/src/api/contract.test.ts:1067-1117` |

### 1.2 Les formulaires

Tous les écrans de saisie. **Aucun ne garde la saisie** : pas de `useBlocker`,
`beforeunload` ni brouillon dans `web/src` (grep), `useForm` vit dans la page.

| Formulaire | Route | Conteneur | Étapes | Écrit par |
| --- | --- | --- | --- | --- |
| `MachineNew` | `machines/nouvelle` | `FormPage` + `FormCard` | 1 | `POST /api/machines/tokens` |
| `MachineToken` | `machines/jeton` | `FormPage` (affichage) | — | — |
| `MachineRights` | `machines/:id/droits` | `FormPage` + `FormCard` | 1 | `POST /api/machines/{id}/actions/reenroll` |
| `MachineProxmox` | `machines/:id/proxmox` | `FormPage` + `FormCard` | 1 | `PUT /api/machines/{id}/proxmox` |
| `JobForm` | `taches/nouvelle`, `taches/:id/modifier` | `FormPage` + `FormCard` | 1 | `POST/PUT /api/jobs` |
| `ProbeForm` | `disponibilite/nouvelle`, `…/:id/modifier` | `FormPage` + `FormCard` | 1 | `POST/PUT /api/probes` |
| `DeclaredServiceForm` (369 lignes) | `services/annuaire/nouveau`, `…/:id/modifier` | `FormPage` + `FormCard` | 1 | `POST/PUT /api/directory/services` |
| `ProjectForm` | `services/projets/nouveau`, `…/:id` | `FormPage` + `FormCard` | 1 | `POST/PUT /api/directory/projects` |
| `FirewallForm` | `pare-feu/nouveau`, `…/:id/modifier` | `FormPage` + **`Wizard`** | 3 + récapitulatif | `POST/PUT /api/firewalls` |
| `StatusPageForm` | `page-statut/pages/nouvelle`, `…/:id` | `FormPage` + `FormCard` | 1 | `POST/PUT /api/status/pages` |
| `StatusComponentForm` | `page-statut/composants/nouveau`, `…/:id` | `FormPage` + `FormCard` | 1 | `POST/PUT /api/status/components` |
| `StatusIncidentNew` | `page-statut/incidents/nouveau` | `FormPage` + `FormCard` | 1 | `POST /api/status/incidents` |
| `StatusIncident` `UpdateForm`, `EditForm` | `page-statut/incidents/:id` | `FormCard` dans la fiche | 1 | `POST …/updates`, `PUT /api/status/incidents/{id}` |
| `AlertChannelForm` | `alertes/canaux/nouveau`, `…/:id` | `FormPage` + `FormCard` | 1 | `POST/PUT /api/alerts/channels` |
| `AlertMuteNew` | `alertes/silences/nouveau` | `FormPage` + `FormCard` | 1 | `POST /api/alerts/mutes` |
| `Backups` `KeyringCard`, `DestinationsCard` ; `backups/PlanForm` | `sauvegardes` | `FormCard` dans la page | 1 | `POST /api/backups/keyring`, `…/destinations`, `PUT …/plans/{subject}` |
| Paramètres : mot de passe, seuils disque, e-mails, jetons d'API | `parametres#…` | `<form>` brut + `SettingRow` | 1 | `PUT /api/auth/password`, `/api/settings/disk`, `/api/settings/mail` ; `POST /api/settings/api-tokens` |
| `FirstSetup` (+ `AccessCredentialsForm`, `AccessForwardForm`) | hors routeur | `Card` + `FormBody` | 2 écrans | `POST /api/auth/actions/enable`, `POST /api/machines/local-enrollment` |
| `DeployCard` `DeployDialog` | fiche service | `<dialog>` | 1 | `POST /api/directory/services/{id}/actions/deploy` |

Sources : routes `web/src/App.tsx:72-127` ; conteneurs `web/src/components/FormPage.tsx:9`,
`FormCard.tsx:13,41,71` ; détail par fichier dans le relevé (`grep -rn "useForm\|<form" web/src`).

| Brique de saisie | Fait | Preuve |
| --- | --- | --- |
| `Wizard` | Précédent / Suivant, validation par étape, récapitulatif avec « Modifier », valeurs chez l'appelant ; rendu **en page**. Un seul usage réel : `FirewallForm` | `web/src/components/Wizard.tsx:33-45,136-155` ; règles pures `web/src/lib/wizard.ts:19-79` (testées, `wizard.test.ts`) |
| Modales | Deux `<dialog>` natifs copiés l'un sur l'autre, sans composant commun ; aucun `role="dialog"` ailleurs | `web/src/components/DeletePlanDialog.tsx:40-53` ; `DeployCard.tsx:107-119` |
| Confirmations | Trois mécanismes : `window.confirm` par `useAction` (9 appels), `ConfirmInline`, `DeletePlanDialog` | `web/src/hooks/useAction.ts:49` ; `ConfirmInline.tsx:15` ; ex. `Machine.tsx:310`, `alerts/MutesTab.tsx:69` |
| Annulation | Écrite deux fois | `FormCard.tsx:100-109` ; `Wizard.tsx:146-155` |

### 1.3 Les paquets Go concernés

| Paquet | Rôle pour la refonte | Preuve |
| --- | --- | --- |
| `machine` | Machines, agents, jetons d'enrôlement, droits (`CapabilityNames()`, 10 noms), retrait (`Remove`), révocation (`Revoke`) | `internal/machine/capability.go:18-47` ; `service.go:189-385` |
| `role` | Seul rôle détecté : `proxmox_ve` (paquet `pve-manager` + montage `/etc/pve`) | `internal/role/detect.go:13-44` ; `role.go:13` |
| `hostinfo` | Ce que l'agent dit à l'enrôlement : nom, OS, arch, adresse, empreinte. **Ni virtualisation, ni MAC** | `internal/hostinfo/hostinfo.go:30-71` |
| `proxmox` | Hôte déclaré, invités, stockages ; lien invité → machine **par nom d'hôte** | `internal/proxmox/reading.go:104-130` ; `client.go:179,205,282` |
| `directory` | Services déclarés, unités, projets, écart, propositions d'adoption | `internal/directory/*.go` |
| `status` | Pages, composants, incidents, état dérivé | `internal/status/*.go` |
| `alert` | Alertes, canaux, silences, livraisons, acquittement | `internal/alert/*.go` |
| `verb` | Registre des verbes ouverts aux jetons (11) | `internal/verb/{default,agent,proxmox}.go` |
| `deletion` | Forme d'un plan de suppression ; la règle reste au domaine | `internal/deletion/plan.go` |
| `server` | Routes (177 lignes figées), admission des jetons, goldens | `internal/server/routes.go:207-266` ; `testdata/routes.txt` |
| `brand` | Le nom, une seule fois ; test des littéraux **Go seulement** | `internal/brand/brand.go:9` ; `literals_test.go:35-60` |

### 1.4 Ce qu'une machine sait faire aujourd'hui

Il n'y a **pas** de notion unique : chaque écran déduit sa condition à sa façon.

| Fait | Décidé par | Stocké | Ce qu'il conditionne à l'écran |
| --- | --- | --- | --- |
| Droits de l'agent (`observe`, `probe.lan`, `agent.update`, `docker.*`, `backup.*`, `files.write`, `proxmox.*`) | L'opérateur à l'émission du jeton ; l'agent les redit | `agent_capabilities` (`migrations/001_schema.sql:51-58`) | Boutons grisés avec raison (gestes Proxmox, mise à jour d'agent) — `web/src/lib/proxmox.ts:144-175` |
| Rôle `proxmox_ve` | L'agent, toutes les 10 min | `machine_roles` (`014_machine_roles.sql:4-11`) | Cases `proxmox.*` proposées ; carte « Proxmox reconnu » |
| Hôte Proxmox déclaré | L'opérateur (URL + jeton d'API Proxmox) | `machine_proxmox` (`013_proxmox.sql:7-27`) | **Seul** fait qui change les onglets (`hyperviseur`) |
| Invité d'un hôte | Le serveur, par nom d'hôte | `proxmox_guests.linked_machine_id` (`013:29-51`) | Carte `HostedCard` du résumé |
| Docker | L'agent (présent / raison) | `machine_engines` (`001:145-153`) | Rien : l'onglet Conteneurs reste, vide avec une note (`Machine.tsx:208-234`) |
| Clés SSH | L'agent (relevé sous `observe`) | `ssh_reports`, `ssh_accounts`, `ssh_keys` (`011_ssh_keys.sql`) | Onglet `ssh` toujours affiché |
| Pare-feu | L'opérateur | `firewalls` **sans `machine_id`** (`015_firewalls.sql:3`) | Rien sur la machine |
| Sauvegardes | Objets qui pointent vers une machine | `backup_destinations`, `backup_plans` (`006`, `012`) | Bloquent le retrait ; onglet machine non livré (`machineTabs.ts:35`) |

Graine de la cible déjà présente : `visibleMachineTabs(hostsProxmox)`
(`web/src/lib/machineTabs.ts:24`). Elle ne passe pas à l'échelle : un booléen
par module, transmis à la main.

### 1.5 Annuaire et adoption

| Fait | Preuve |
| --- | --- |
| Un service naît toujours par `CreateService`, `declared_by = operator` (seule valeur) | `internal/directory/service.go:134-163` ; `directory.go:64-72` |
| La colonne `declared_by` est sans `CHECK`, prévue pour « la découverte automatique, le jour où elle viendra » | `001_schema.sql:198` ; `migrations/003_needs_review.sql:1-6` |
| « Adopter » = formulaire pré-rempli par `GET /api/directory/proposal?container=` ; rien n'est écrit avant validation | `internal/directory/propose.go:7-56` ; `web/src/pages/DeclaredServiceForm.tsx:47-81` ; `web/src/lib/gaps.ts:41-56` |
| `ProposeAll` : un service proposé par groupe adoptable, servi dans l'export YAML | `internal/directory/propose.go:108-137` ; `internal/manifest/export.go:64-93` |
| « Ignorer » : `ignored_keys` (projet Compose ou nom de conteneur, par machine) | `internal/directory/ignored.go:12-43` ; `001:248-255` |
| L'**écart** : calculé à chaque lecture (5 états, 7 motifs), jamais stocké ; affiché par `GapPill` (annuaire, fiche service) ; ni alerte ni compteur | `internal/directory/gap.go:13-54,119-236` ; `web/src/components/GapPill.tsx:10-24` |
| Le manifeste (YAML) ne porte pas `declared_by` | `internal/manifest/file.go:55-73` |

### 1.6 Projets et pages de statut

| Fait | Preuve |
| --- | --- |
| Projet = `name`, `title`, lien N-N avec les services ; rien d'autre | `001:178-186,237-246` ; `internal/directory/directory.go:185-192` |
| Page = `slug`, `title`, `announcement`, `language`, `project_id` **UNIQUE nullable** | `001:533-545` |
| `project_id` ne sert qu'à l'unicité, au blocage de suppression du projet (`project.has_status_page`) et à l'audit ; la page ne reprend **pas** les services du projet | `internal/store/status_pages.go:72-90` ; `internal/store/deletion.go:104-122` ; `internal/directory/directory.go:263-267` |
| Composant : appartient à une page (1-N, `CASCADE`), membres N-N parmi machine, service, tâche, sonde (jamais un conteneur) | `001:547-579` ; `internal/status/status.go:118-171` |
| Incident : vise des objets ; ses pages se calculent à la lecture | `migrations/005_incident_targets.sql:4-16` ; `internal/status/targets.go:22-96` |
| Routes publiques `/statut/{slug}…` sans authentification ; bundle public séparé | `internal/server/public/handler.go:92-99` ; `web/statut.html`, `web/src/statut/*` |
| Menu : « Disponibilité » (sondes) et « Pages de statut » sont deux entrées | `web/src/components/Layout.tsx:152-154` |

### 1.7 Alertes

| Fait | Preuve |
| --- | --- |
| 12 genres, 2 gravités (`CHECK (severity IN ('attention','danger'))`), 2 statuts | `internal/alert/alert.go:13-82` ; `001:626-657` |
| Seule notion « vue » : l'**acquittement**, « l'opérateur a vu » ; une aggravation le ré-arme | `internal/alert/acknowledge.go:7-8` ; `engine.go:223-226` |
| 5 onglets : ouvertes, acquittées, résolues, canaux, silences | `web/src/pages/Alerts.tsx:14-20` |
| Compteur du menu : ouvertes, rouge si une n'est pas acquittée | `web/src/components/Layout.tsx:260-262` |
| Canaux (6 formats) et silences (`mutes`) : routes sous `/api/alerts/` | `internal/server/routes.go:151-159` |
| Paramètres : 5 sections ancrées ; l'ancre `alertes` porte les **seuils disque** ; aucune section « Notifications » | `web/src/lib/settingsSections.ts:3-9` |
| Le texte d'une alerte est écrit deux fois : Go pour les canaux, TS pour l'écran (assumé) | `internal/alert/text.go:10-12` ; `web/src/lib/alerts.ts:30` |

### 1.8 La fiche machine

| Élément | Fait | Preuve |
| --- | --- | --- |
| Onglets | Résumé, Conteneurs (`services`), Disponibilité, Réseau, SSH, Journaux, + Hyperviseur | `web/src/lib/machineTabs.ts:8-30` |
| Résumé | `ResourceStats` (4 tuiles), `ResourceHistory`, `DiskVolumesCard`, `SystemSupport`, `HostedCard`, `ProxmoxCard`, `AgentCard`, `ActionsCard` | `web/src/pages/Machine.tsx:81-91` |
| En-tête | Trois boutons : Ré-enrôler, Révoquer l'agent, Retirer ; pas de `MoreMenu` | `web/src/pages/Machine.tsx:296-340` |
| Liste | Triée par nom côté serveur, sans jauge ni hiérarchie ; hôte, invités, rôles sont dans la réponse mais inutilisés | `internal/store/machines.go:27` ; `web/src/pages/Machines.tsx:38-74` |
| Attente de l'agent | `MachineToken` n'écoute pas l'arrivée de l'agent | `web/src/pages/MachineToken.tsx:83-139` |
| Stockage | Volumes montés par l'agent ; ni SMART, ni pool, ni LVM ; volumes Docker non affichés sur la fiche | `internal/sampler/mounts.go:13-60` ; `021_machine_volumes.sql` |

**Révoquer vs Retirer** (`internal/machine/service.go:263-385`, `internal/store/machines.go:75-92,333-355`) :

| | Révoquer l'agent | Retirer la machine |
| --- | --- | --- |
| Route | `POST /api/machines/{id}/actions/revoke` | `DELETE /api/machines/{id}` (+ `GET …/delete-plan`) |
| Machine, historique, conteneurs, clés SSH, invités | gardés | supprimés (cascade) |
| Services, sondes, tâches, composants, plans de sauvegarde | gardés | **bloquent** (409 avec le plan) |
| Alertes | gardées | gardées, détachées |
| Retour arrière | ré-enrôler (même id, même historique) | aucun |
| Confirmation | `window.confirm` | `DeletePlanDialog` |
| Audit | `agent.revoke` | `machine.delete` |

**Changer les droits vs Ré-enrôler** : la même route
`POST /api/machines/{id}/actions/reenroll`, avec ou sans `capabilities`
(`internal/server/api_machines.go:563-575`). Deux boutons, un geste.

---

## 2. Code mort mesuré

Rapports bruts : [`code-mort/`](code-mort/).

| Outil | Commande | Rapport |
| --- | --- | --- |
| deadcode | `go install golang.org/x/tools/cmd/deadcode@latest` ; `deadcode -test ./...` | [`deadcode.txt`](code-mort/deadcode.txt) (1 résultat) |
| deadcode, sans les tests | `deadcode ./...` : ce que **seuls les tests** appellent | [`deadcode-sans-tests.txt`](code-mort/deadcode-sans-tests.txt) (80) |
| knip 5.88.1 | `npx knip --reporter compact` dans une copie de `web/` | [`knip.txt`](code-mort/knip.txt) |
| knip, entrées déclarées | même, avec `entry: src/main.tsx, src/statut/main.tsx, public/theme.js` (fichier de config hors dépôt) | [`knip-entrees.txt`](code-mort/knip-entrees.txt) |

### 2.1 Go

| Résultat | Classe | Raison |
| --- | --- | --- |
| `agentwire.ObservationKinds` (`internal/agentwire/command.go:69`) | **mort** | Aucun appel, même en test (`grep -rn ObservationKinds`) |
| 37 listes fermées (`States`, `Kinds`, `Reasons`, `Statuses`, `Provenances`, `GapStates`…) | faux positif | Lues par `internal/server/composed_keys_test.go` pour vérifier les clés composées du catalogue ; la liste fermée est la règle |
| Paquets `storetest`, `servertest`, `smtptest`, `opnsensetest`, `releasetest` (36 fonctions) | faux positif | Bancs de test ; « seuls des `_test.go` les importent » (`docs/projet/17-conventions-code.md`, table des imports) |
| `release.PrivateKey.Format` | faux positif | Appelée par `fmt` à travers l'interface `fmt.Formatter` (masque la clé) |
| `verb.Registry.Check` | faux positif | Le test `Default.Check()` tient la règle « chaque inverse existe » (`internal/verb/verb_test.go:65`) |
| `container.NeedsAttention` | faux positif | Oracle Go du SQL `containerNeedsAttention` ; « une règle écrite en Go et en SQL a son test de parité » (17-conventions) |
| `agent.allowAll`, `alert.Verify`, `alert.Describe` | à confirmer | Aides de test placées dans un fichier de production ; `Describe` est l'enveloppe sans échappement de `describe` (`internal/alert/text.go:21`). À déplacer dans un `_test.go` ou à garder : un choix de style |

### 2.2 Front

| Résultat | Classe | Raison |
| --- | --- | --- |
| `src/statut/main.tsx`, `StatusPage.tsx`, `statut.scss` | faux positif | Seconde entrée Vite (`web/vite.config.ts:22-24`, `web/statut.html:13`) ; disparaît avec les entrées déclarées |
| `public/theme.js` | faux positif | Chargé par `<script src="/theme.js">` (`web/index.html:9`) |
| `lib/proxmox.ts` `hostsProxmox` (`:78`) | **mort** | Jamais appelée : la capacité est relue en ligne (`current.proxmox.declared`, `Machine.tsx:78`) — c'est le registre de §3.1 qui la remplace |
| `lib/manifest.ts` `applyStates` (`:7`) | **mort** | Déclarée, jamais lue |
| `components/Field.tsx` `Select`, `EnumSelect` | **doublon** | 22 fichiers importent `components/Select` ; seul `FirewallForm.tsx:4` prend le `<select>` natif de `Field`. Le test `Select.test.tsx:23` (« aucun select natif ») ne le voit pas |
| 15 autres exports (`actionsPath`, `agentUpdatePath`, `noActorFilter`, `countedTopics`, `AccountState`, `muteTargetOf`, `minPassphrase`, `databaseSubject`, `isCleanExit`, `transitionTitle`, `unitFormOf`, `hypervisorTab`, `workspaceRoot`, `minScale`/`maxScale`/`overviewFrameGap`, `attentionProbeStatuses`, `searchThreshold`, `incidentStatuses`, `maintenanceStatuses`) | export inutile | Utilisés dans leur seul fichier. La règle (17-conventions, « Le front ») : « une brique de son seul fichier ne s'exporte pas ». On retire `export`, pas le code |
| 38 types exportés non importés | export inutile | Même règle |

### 2.3 Doublons fonctionnels

| Doublon | Où | Sort dans la cible |
| --- | --- | --- |
| Révoquer / Retirer | `Machine.tsx:296-340` ; deux routes | Un geste « Supprimer la machine », option « garder l'historique » (§3.2, C5) |
| « Changer les droits » / « Ré-enrôler » | même route `…/actions/reenroll` | Un geste « Ré-enrôler » qui ouvre l'assistant avec les droits pré-cochés (C4) |
| Onglet Conteneurs de Services / onglet Conteneurs de la fiche | même handler `writeContainers`, même `ContainerTable` (`internal/server/api_containers.go:165-188`) | L'onglet de Services disparaît : les conteneurs se déplient sous leur service. La fiche garde le sien (C7) |
| Compteur « Services » = conteneurs | `Layout.tsx:251-253` | Le compteur se dérive du module (§3.1) |
| `FormCard` / `Wizard` / `FormBody` / `<form>` brut | §1.2 | Un composant `Modal` + `Wizard` ; Paramètres gardent `SettingRow` (§3.2) |
| `DeletePlanDialog` / `DeployDialog` | deux `<dialog>` copiés | Construits sur `Modal` (C2) |
| `window.confirm` / `ConfirmInline` / `DeletePlanDialog` | §1.2 | `ConfirmInline` pour ce qui se défait, `Modal` pour ce qui ne se défait pas (C2) |
| `Disclosure` (1 usage) / `Fold` (3) | `components/Disclosure.tsx` | « Avancé » des assistants = `Fold` ; `Disclosure` à confirmer au chantier |
| `Select` de `Field.tsx` / `Select.tsx` | §2.2 | `Field.Select`, `Field.EnumSelect` supprimés (C1) |
| Mot-symbole `open`+`Cloud` en dur | `Layout.tsx:129`, `statut/StatusPage.tsx:107` | Lu dans le catalogue (`app.name`), comme le titre (C1) |

---

## 3. Architecture cible

Chaque point : le patron, la raison tirée du code, l'alternative écartée.

### 3.1 Registre de modules à capacités

**Le mot.** « Capacité » nomme déjà les droits d'un agent
(`internal/machine/capability.go`, `agent_capabilities`). Pour ne pas les
confondre, le code dira **module** ; une machine ou l'installation **porte**
des modules. À l'écran, « capacités » reste libre.

| | Cible |
| --- | --- |
| Patron | **Registre déclaratif à liste fermée** + **dérivation des faits** (spécification par prédicat) |
| Raison | Aujourd'hui chaque écran déduit sa condition à la main (`hostsProxmox` passé en booléen, `Machine.tsx:78` ; menu en dur, `Layout.tsx:147-164` ; onglets en dur, `machineTabs.ts:8`). La liste fermée rendue par une fonction est l'idiome du dépôt (`machine.CapabilityNames()`, `alert.Kinds()`, testées par `composed_keys_test.go`). Le registre de verbes (`internal/verb`) a déjà prouvé la forme : un identifiant stable, des clés de catalogue, un test de cohérence |
| Écartée : un système de greffons (chargement dynamique) | Réflexion et cadre opaque, interdits (17-conventions, « Ce qu'on ne fait pas ») |
| Écartée : des drapeaux dans `config.toml` | Le module se **constate** (Docker présent, hôte déclaré) ; un drapeau demanderait à l'opérateur de dire ce qu'openCloud sait déjà, et mentirait dès que la machine change |
| Écartée : garder des `if` par écran | C'est l'état actuel ; chaque module ajouté (OPNsense, Traefik…) ajouterait un booléen dans trois fichiers |

**Côté serveur.**

| Pièce | Rôle | Où |
| --- | --- | --- |
| `module.ID`, `module.Scope` (`installation`, `machine`) | Liste fermée, `module.All()` ; libellé et phrase en clés de catalogue `module.<id>`, `module.<id>_text` | nouveau paquet outil `internal/module` (aucune règle d'openCloud : la liste et sa forme) |
| Règle de présence d'un module **de machine** | Une requête SQL qui rend, par machine, les modules portés, à partir des faits de §1.4 | `internal/store/modules.go` (« compter en SQL ») ; lu par `reader.Reader` |
| Règle de présence d'un module **d'installation** | « configuré » = au moins un objet du module existe (un pare-feu, un trousseau de sauvegarde, une sonde…) | idem, une requête |
| Exposition | `GET /api/machines` et `/{id}` gagnent `modules: [...]` ; `GET /api/session` gagne `modules: [{id, configured}]` | goldens `machines`, `machine`, `session` |
| Garde | Un test : chaque `module.ID` a ses deux clés de catalogue, chaque module de machine a sa règle SQL | `internal/server/composed_keys_test.go` (préfixe `module.`) |

Modules de départ, tous constatés par des faits qui existent déjà :

| Module | Portée | Présent quand | Remplace |
| --- | --- | --- | --- |
| `containers` | machine | `machine_engines.present` ou au moins un conteneur | onglet `services` toujours affiché |
| `hypervisor` | machine | `machine_proxmox` existe | `visibleMachineTabs(hostsProxmox)`, `hostsProxmox` (mort) |
| `guest` | machine | `proxmox_guests.linked_machine_id` = la machine | `proxmox.hosted !== null` |
| `ssh_keys` | machine | `ssh_reports` existe | onglet `ssh` toujours affiché ; entrée de menu Clés SSH |
| `probes` | machine + installation | au moins une sonde | onglet `disponibilite` toujours affiché |
| `firewall` | installation | au moins un pare-feu | entrée « Administration » |
| `backups` | installation | trousseau ou plan | entrée « Administration » |
| `jobs` | installation | au moins une tâche | entrée de menu fixe |
| `status` | installation | au moins un projet publié (§3.4) | entrée « Pages de statut » |

**Côté front.**

| Pièce | Rôle |
| --- | --- |
| `web/src/lib/modules.ts` | Une table : `id`, clé de libellé, adresse, icône, onglet de fiche (`tabSlug`), compteur (fonction pure de `Counts`), rang. **Seul** lieu qui sait qu'un module a une entrée de menu ou un onglet |
| `Layout` | Le menu = les modules d'installation configurés, puis les entrées fixes (Vue d'ensemble, Machines, Services, Alertes) ; **plus de section « Administration »** ; Paramètres en bas, au-dessus de langue et thème |
| Fiche machine | Les onglets = onglets fixes (Résumé, Réseau, Journaux, Agent) + onglets des modules portés |
| Module non configuré | N'apparaît pas. Son premier geste (« Déclarer un pare-feu ») vit dans Paramètres › Modules, et sur la vue d'ensemble tant que rien n'est configuré (question Q3) |

Agencement du menu demandé par #386, dérivé du registre :

| Rang | Entrée | Source |
| --- | --- | --- |
| 1 | Vue d'ensemble | fixe |
| 2 | Machines | fixe |
| 3 | Pare-feu | module `firewall` (même niveau que Machines) |
| 4 | Sauvegardes | module `backups` (même niveau que Machines) |
| 5 | Services | fixe |
| 6 | Supervision web (sondes + pages publiques, §3.4) | module `probes` / `status` ; nom à trancher (Q6) |
| 7 | Tâches planifiées | module `jobs` (renommé) |
| 8 | Alertes | fixe |
| 9 | Journal | fixe |
| bas | Paramètres, puis langue et thème | fixe |

Clés SSH sort du menu : onglet `ssh` de la fiche, avec un état vide
explicatif. La place de la matrice globale (`/cles-ssh`) est la question Q4.

**Extensions prévues du registre** (citées, pas conçues) : plan d'adressage
IP, métriques OPNsense par API (le module `firewall` gagne une portée
machine), Traefik puis Authelia gérés depuis {product}, optimisation des
jetons du MCP (lecture des modules par mcp-maison pour ne proposer que les
outils utiles).

### 3.2 Modale + assistant, et un bouton d'action

| | Cible |
| --- | --- |
| Patron | **Composition** : `Modal` (cadre) ∘ `Wizard` (étapes, déjà écrit) ∘ `useForm` (valeurs). **Route modale** : la modale s'ouvre par l'adresse |
| Raison | `Wizard` et `lib/wizard.ts` font déjà Précédent / Suivant / récapitulatif, testés, mais en page (`Wizard.tsx:45`) ; deux `<dialog>` existent sans socle commun (§1.2). Composer réutilise les trois sans les réécrire. La route modale garde les liens profonds en usage : adopter depuis un conteneur (`lib/gaps.ts:41-56`), silence pré-rempli depuis une alerte (`lib/alerts.ts:307-312`), sonde depuis un conteneur (`Container.tsx:67`) |
| Écartée : un état local `open` | Perd ces liens profonds ; un rechargement perd la modale |
| Écartée : une bibliothèque de modales | « Toute dépendance nouvelle se décide en revue » (17-conventions) ; `<dialog>` natif suffit, il fait déjà le focus et Échap |

**`Modal`** (`web/src/components/Modal.tsx`, nouveau) :

| Règle | Comment |
| --- | --- |
| Flou léger sur la vue | `::backdrop` = `--scrim` + `backdrop-filter: blur(…)` ; jeton nouveau `--scrim-blur` dans `_tokens.scss` (seul ajout au thème) |
| Saisie jamais perdue sur un clic dehors | Le clic sur le voile **ne ferme pas**. Échap et « Annuler » ferment ; si la saisie a changé, `ConfirmInline` « Abandonner la saisie ? » d'abord |
| Accessibilité | `<dialog>` + `showModal()` : focus piégé, `aria-labelledby` sur le titre d'étape (repris de `Wizard.tsx` qui y pose déjà le focus) |
| Fermeture | Retour à l'adresse d'arrière-plan (la liste) ; un succès y mène aussi, avec la ligne neuve en évidence |

**`ActionButton`** : le bouton principal unique de l'en-tête (règle « une seule
action principale », doc 25). Les autres gestes vont dans `MoreMenu`, grisés
avec leur raison. Il prend la **route** qu'il appelle (§3.7) et en déduit la
puce `MCP`.

**Formulaires qui migrent** (et ce qui disparaît) :

| Formulaire | Devient | Étapes |
| --- | --- | --- |
| `MachineNew` + `MachineToken` | Assistant « Ajouter une machine » | 1. nom + rôle préréglé (droits sous « Avancé ») ; 2. commande à coller ; 3. attente de l'agent en direct (sujet `machines` de `useLive`) |
| `MachineRights` | Assistant « Ré-enrôler » (mêmes étapes 2-3, droits pré-cochés) | 3 |
| `MachineProxmox` | Assistant « Déclarer l'hôte Proxmox » | 2 (accès ; certificat) + récapitulatif |
| `DeclaredServiceForm` | Assistant « Déclarer un service » ; champs techniques sous « Avancé » | 3 (identité ; unités ; projets) |
| `ProbeForm` | Assistant « Ajouter une sonde » ; HTTP sous « Avancé » | 2 |
| `JobForm` | Modale une étape + récapitulatif | 1 |
| `ProjectForm` | Fusionné avec `StatusPageForm` (§3.4) | 2 (projet ; publication) |
| `StatusComponentForm` | Modale une étape | 1 |
| `StatusIncidentNew`, `EditForm` | Modale une étape ; `UpdateForm` reste en ligne dans la fiche (fil de l'incident) | 1 |
| `FirewallForm` | Son `Wizard` passe dans `Modal` | 3, inchangées |
| `AlertChannelForm`, `AlertMuteNew` | Modales, ouvertes depuis Paramètres › Notifications (§3.6) | 1-2 |
| `KeyringCard`, `DestinationsCard`, `PlanForm` | Modales depuis la page Sauvegardes | 1-2 |
| `DeployDialog`, `DeletePlanDialog` | Construits sur `Modal` | — |
| Paramètres (mot de passe, disque, e-mails, jetons) | **Restent en ligne** (`SettingRow`) : on règle une valeur en place, rien ne s'ajoute | — |
| `FirstSetup` | Reste plein écran : il n'y a pas de vue derrière | — |

Supprimés à la fin de la migration : `FormPage.tsx` (+ `.scss`), `FormCard`
sauf `FormBody` (gardé par `FirstSetup`), `width="wizard"`, les 9
`window.confirm`, les routes `…/nouveau`, `…/modifier` **comme pages** (elles
deviennent des routes modales sur leur liste).

### 3.3 Modèle hôte > invités

| | Cible |
| --- | --- |
| Patron | **Composite** côté front (un nœud de l'arbre est une machine, un invité sans agent ou un cluster) ; côté serveur, **rapprochement par clés ordonnées** (la plus forte gagne) |
| Raison | Le lien d'aujourd'hui se fait par nom d'hôte seul (`internal/proxmox/reading.go:104-130`) : deux VM au même nom, ou un nom changé, cassent le lien. L'API Proxmox et l'agent savent tous deux donner l'adresse MAC ; aucun des deux ne la lit aujourd'hui (`internal/proxmox/client.go` : `/version`, `/cluster/resources`, stockages seulement) |
| Écartée : une route `/api/machines/tree` | Un second chemin de lecture des mêmes lignes ; l'arbre se construit en fonction pure côté front (`lib/machineTree.ts`, testée) à partir de `GET /api/machines` enrichi |
| Écartée : le VMID lu depuis l'invité (`/sys/class/dmi/id/product_uuid` = `smbios1`) | Lisible par root seulement ; l'agent tourne sous un compte dédié (`010_agent_account.sql`) |

Clés du rapprochement agent ↔ invité, dans l'ordre :

| Rang | Clé | Source |
| --- | --- | --- |
| 1 | Correction manuelle de l'opérateur | colonne `machines.host_override` |
| 2 | MAC d'une interface de l'agent = MAC d'un `netN` (qemu) ou `hwaddr` (lxc) | agent : `/sys/class/net/*/address` (lisible sans root) ; Proxmox : `/nodes/{node}/{qemu,lxc}/{vmid}/config` |
| 3 | Nom d'hôte (règle actuelle) | `reading.go:104-130`, gardée en repli |

Type de machine :

| Type | Déduit de | Correction |
| --- | --- | --- |
| `physical` | `systemd-detect-virt` → `none` | `machines.kind_override` |
| `vm` | `kvm`, `qemu`, `vmware`, `microsoft`… **et** un hôte connu | idem |
| `lxc` | `lxc` | idem |
| `vps` | virtualisé **sans** hôte connu (on ne peut pas savoir mieux) | idem ; c'est le cas le plus souvent corrigé |
| `unknown` | agent ancien, ou commande absente | idem |

Cluster dès le modèle : `hypervisor_clusters(id, name, kind)` ;
`machine_proxmox.cluster_id` (un hôte déclaré lit tout le cluster par
`/cluster/resources`, déjà le cas) ; `proxmox_guests.node` existe et rattache
chaque invité à son nœud. L'arbre affiche le niveau cluster seulement à partir
de deux nœuds.

Protocole de l'agent : `hostinfo` gagne `virt` et `interfaces[{name, mac}]`.
Un agent ancien ne les envoie pas, le serveur les tient pour absents : pas de
changement de `ProtocolVersion` (règle de `agentwire`, 17-conventions). Les MAC
sont redites au signal (une interface change).

Liste (maquette) : une ligne par hôte, ses invités en retrait (machines enrôlées
**et** invités sans agent, grisés « arrêté · sans agent »), badge de type,
point d'état, mini-jauges CPU / RAM / stockage, IP ; bandeau de santé
(verdict de la doc 25) ; anomalies en tête.

Fiche (maquette) : deux colonnes ; à droite, l'identité fixe (type, hôte, IP,
zone, OS, agent, allocation par l'hôte) ; à gauche les onglets. Résumé = tuiles
et historique **inchangés** (`ResourceStats`, `ResourceHistory`) + bloc « À
faire » (une liste de faits qui demandent un geste : mise à jour d'agent,
droits non exerçables, fin de support) avec un bouton chacun. Onglet « Agent »
= `AgentCard` + `ActionsCard` (ordres). Stockage : disques physiques puis
pools/volumes ; volumes d'invités rattachés à leur invité (SMART : Q8).

### 3.4 Fusion Projet + Page de statut

| | Cible |
| --- | --- |
| Patron | **Agrégat** : le projet est la racine ; sa publication est une partie optionnelle de lui, pas une entité voisine |
| Raison | Aujourd'hui la page ne sert au projet qu'à bloquer sa suppression (§1.6) ; le lien 0..1 ↔ 0..1 est une entité coupée en deux. Le visiteur voit des composants saisis à la main, sans lien avec les services du projet (`StatusComponentForm.tsx:122`) |
| Écartée : garder deux entités et synchroniser | Deux écritures pour un fait, et le chemin d'avant reste |
| Écartée : supprimer les composants | Ils portent le nom public et le regroupement que le visiteur lit (« le seul mot d'{product} qui sort en public », `001:551`) ; on les garde, rattachés au projet |

Modèle :

| Avant | Après |
| --- | --- |
| `projects(id, name, title)` | `projects(id, name, title, slug UNIQUE NULL, announcement, language, published_at NULL)` — publié ⇔ `slug` non nul |
| `status_pages(id, project_id UNIQUE NULL, slug, title, announcement, language)` | **supprimée** |
| `status_components.page_id → status_pages` | `status_components.project_id → projects ON DELETE CASCADE` |
| Page sans projet | Devient un projet (sans service) |
| Projet sans page | Projet non publié |
| Page sans composant | Composants dérivés par défaut : un par service du projet (règle de lecture, rien d'écrit) — Q5 |

Migration `026_project_publication.sql` (esquisse, écrite et testée au
chantier C9 avec `TestSchema_*`) :

```sql
-- 1. La publication entre dans le projet.
ALTER TABLE projects ADD COLUMN slug TEXT;
ALTER TABLE projects ADD COLUMN announcement TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN language TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN published_at INTEGER;
CREATE UNIQUE INDEX projects_slug ON projects (slug) WHERE slug IS NOT NULL;

-- 2. Une page sans projet devient un projet ; son nom est son adresse,
--    suffixé si un projet porte déjà ce nom.
INSERT INTO projects (id, name, title, created_at, updated_at)
SELECT 'prj_' || p.id,
       CASE WHEN EXISTS (SELECT 1 FROM projects WHERE name = p.slug) THEN p.slug || '-statut' ELSE p.slug END,
       p.title, p.created_at, p.updated_at
FROM status_pages p WHERE p.project_id IS NULL;
UPDATE status_pages SET project_id = 'prj_' || id WHERE project_id IS NULL;

-- 3. Chaque projet reprend sa page.
UPDATE projects SET (slug, announcement, language, published_at) =
  (SELECT slug, announcement, language, created_at FROM status_pages WHERE project_id = projects.id)
WHERE id IN (SELECT project_id FROM status_pages);

-- 4. Les composants changent de parent (SQLite ne modifie pas une clé
--    étrangère : on recrée la table), puis la page part.
CREATE TABLE status_components_new (… project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE …);
INSERT INTO status_components_new SELECT c.id, s.project_id, c.name, c.position, c.created_at
FROM status_components c JOIN status_pages s ON s.id = c.page_id;
DROP TABLE status_components; ALTER TABLE status_components_new RENAME TO status_components;
DROP TABLE status_pages;
```

L'adresse publique `/statut/{slug}` ne change pas. Le titre public = le titre
du projet (avant : celui de la page ; l'étape 3 garde le titre du projet,
Q5).

Ce qui disparaît :

| Couche | Supprimé |
| --- | --- |
| Routes | `GET/POST /api/status/pages`, `GET/PUT/DELETE /api/status/pages/{id}`, `GET …/delete-plan` (6) ; la publication s'écrit par `PUT /api/directory/projects/{id}` |
| Go | `status.Page`, `PageDefinition`, `internal/status/page.go`, `internal/store/status_pages.go` (partie pages), refus `statuspage.project_invalid`, `statuspage.project_taken`, `project.has_status_page`, genre de plan `status_page` |
| Front | `StatusPageForm.tsx`, `status/PagesTab.tsx`, `StatusPagePill.tsx` (à confirmer), routes `page-statut/pages/*`, entrée de menu « Pages de statut » |
| Catalogue | `statuspage.*` repris sous `project.publish_*` ; les autres retirés des deux langues |
| Goldens | `status_pages`, `status_page` remplacés par `projects` / `project` enrichis |
| Écriture | La publication s'écrit par **`directory`** (le propriétaire du projet), `status` la lit par `reader` — règle « écrire toujours par son composant » |

Impact mcp-maison : `opencloud_status` lit `/api/status/pages` et `/pages/{id}`
(`mcp-maison:src/tools/opencloud/tools.ts:167-190`) ; à porter sur
`/api/directory/projects` (à ouvrir aux jetons en lecture, absente de
`apiTokenRoutes` aujourd'hui), avec `test/fakes.ts` et `test/opencloud.test.ts`.

Disponibilité et Pages de statut fusionnent dans **un module simple** (le
menu : une entrée) : les sondes, leur disponibilité, les incidents, et la
liste des projets publiés. Le projet lui-même vit dans Services.

### 3.5 Annuaire à déclaration automatique

| | Cible |
| --- | --- |
| Patron | **Observateur** (le suivi des conteneurs prévient l'annuaire, déjà câblé par `Set*`) + **Commande annulable** (la déclaration automatique porte son inverse) |
| Raison | Le socle existe : `ProposeAll` fait déjà un service par groupe adoptable (`propose.go:108-137`) ; `declared_by` attend sa seconde valeur (`003_needs_review.sql:1-6`) ; « Ignorer » est l'inverse naturel (`ignored_keys`) |
| Écartée : garder « Adopter » à la main à côté | Deux chemins pour le même fait ; c'est ce que l'urbanisation interdit |
| Écartée : déclarer sans prévenir | L'opérateur découvrirait un service qu'il n'a pas voulu ; l'annulation doit être sous la main |

| Pièce | Règle |
| --- | --- |
| Déclenchement | `directory` reçoit les changements du suivi des conteneurs par une interface déclarée chez lui, branchée au câblage (`containers.SetUnits(declared)` existe déjà, `cmd/opencloud/components.go:91` ; `internal/container/tracker.go:133`) |
| Geste | Pour un groupe adoptable, non ignoré, non déclaré : `CreateService` avec `declared_by = discovery`, la proposition de `Propose` telle quelle ; audit `service.create`, acteur `rule` (acteur existant, `internal/audit/actor.go`) |
| Notification | Une ligne « nouveau service détecté » dans la boîte de réception (§3.6), avec « Annuler » |
| Annuler | Supprime le service **et** ajoute sa clé à `ignored_keys` : il ne revient pas ; un seul geste, inverse exact |
| Garder | Acquitter la notification ; `declared_by` passe à `operator` à la première modification |
| Réglage | Paramètres : déclaration automatique activée ou non (Q7) |

Ce que devient l'écart :

| Partie | Sort |
| --- | --- |
| Verdict déclaré ↔ observé (`conform`, `diverged`, `absent`…) | **Reste**, inchangé : c'est la dérive d'un service déclaré |
| Liste « non déclarés » (`UndeclaredList`) | Se vide d'elle-même ; reste pour ce qui n'est pas adoptable (label orphelin) et pour les ignorés (« Ne plus ignorer ») |
| Bouton « Adopter », `DeclaredServiceForm?container=`, `adoptPath` | **Supprimés** |
| `GET /api/directory/proposal` | Supprimée si plus aucun écran ne l'appelle (à confirmer au chantier : l'export `?propose` utilise `ProposeAll`, pas la route) |
| Manifeste | Un `apply --prune` supprimerait un service découvert absent du fichier. Règle : le fichier exporte les services `discovery` comme les autres (ils sont déclarés) — Q7 |

### 3.6 Alertes en boîte de réception

| | Cible |
| --- | --- |
| Patron | **Boîte de réception** : une liste, un état « vue », des filtres ; **Façade** de Paramètres › Notifications sur les routes existantes |
| Raison | L'acquittement *est* déjà « l'opérateur a vu » (`acknowledge.go:7-8`), ré-armé par une aggravation : c'est exactement « non vue ». Les 3 onglets ouverts / acquittées / résolues sont trois filtres d'une même liste (`Alerts.tsx:54-56`) |
| Écartée : une colonne `seen_at` à côté de l'acquittement | Deux notions pour un fait ; une migration de plus |
| Écartée : déplacer les routes `/api/alerts/channels`, `/mutes` | Rien ne l'exige ; mcp-maison lit `/api/alerts/channels` ; seul l'écran change de place |

| Élément | Cible |
| --- | --- |
| Liste | Non vues en tête, vues ensuite, résolues dessous (historique, `?status=resolved`) |
| Geste | « Marquer comme vue » = `POST /api/alerts/{id}/actions/acknowledge` (et le lot) ; libellé seul change |
| Filtres | Gravité, genre, machine, service ; côté client sur la liste ouverte (bornée), paramètre de requête sur l'historique |
| Compteur | Non vues (`alerts.unacknowledged`), déjà servi par `GET /api/counts` |
| Notification « nouveau service détecté » | Genre d'alerte `service_discovered`, gravité nouvelle `info` (Q2) : un canal à `min_severity = attention` ne la reçoit pas |
| Paramètres › Notifications | Canaux, Silences, **et** les seuils disque (aujourd'hui sous l'ancre `alertes`) ; `#alertes` renvoie à `#notifications` |
| Routes front | `alertes/canaux*`, `alertes/silences*`, `alertes/:tab` → routes modales sous `parametres` ; anciens liens redirigés (patron `ProbesRedirect`, `App.tsx:56`) |
| Plus tard | Routage par machine / type (cité, non conçu) |

Coût franc : la gravité `info` touche le `CHECK` de `alerts.severity`
(`001_schema.sql:628`), donc une reconstruction de table (`025`), les goldens
d'alertes, le texte des canaux, et le contrat de mcp-maison
(`opencloud_alerts`). L'alternative sans migration : une table de
notifications à part, mais alors deux listes à l'écran (Q2).

### 3.7 Parité UI / MCP

| | Cible |
| --- | --- |
| Patron | **Table de correspondance déclarative** (le front déclare la route de chaque écriture) + **test de contrat** (comme `contract.test.ts` pour les réponses) |
| Raison | La parité d'exécution existe déjà : « une route, un seul code », le jeton passe par le handler de l'opérateur (`22-fonctionnement.md:3852-3858`). Ce qui manque est la **preuve** : les chemins du front sont des chaînes (`client.ts`), aucun lien entre un bouton et un verbe (grep `verb` dans `web/src`), la puce `MCP` de la doc 25 n'existe pas |
| Écartée : générer un client TS depuis `routes.go` | Un générateur à maintenir, un cadre opaque |
| Écartée : OpenAPI | Une dépendance et un second contrat à côté des goldens |

| Pièce | Rôle |
| --- | --- |
| `web/src/api/routes.ts` | Chaque écriture du front déclare son motif, tel que dans `routes.txt` : `"POST /api/probes/{id}/actions/pause"` ; `api/*.ts` appellent par motif + paramètres (`segment()` garde ses contrôles) |
| `verbs.golden.json` | Le registre (`id`, `route`, `reverse`) écrit par le banc Go existant (`compareGolden`, `internal/server/api_test.go:252`) |
| Test front | Chaque motif déclaré existe dans `internal/server/testdata/routes.txt` ; **chaque verbe** du golden a un geste d'écran (son motif est déclaré) |
| Test Go | Chaque route d'écriture de `routes.txt` est appelée par le front **ou** listée « sans écran » avec sa raison (agent, ping, statut public) : une route sans appelant est du code mort |
| Puce `MCP` | `ActionButton` / `MoreMenu` reçoivent le motif ; puce si le motif est la route d'un verbe. Le registre arrive par `GET /api/session` (`verbs: [{id, route}]`), lu au chargement |

Point à trancher (Q1) : #386 dit « tout ce que l'UI fait passe par les mêmes
verbes que le MCP » ; la doc 25 réserve à l'interface ce qui détruit
(supprimer, révoquer). La cible tient les deux : **toute action d'écran est une
route d'API** (le MCP peut l'atteindre si un verbe l'ouvre), et **tout verbe a
son geste d'écran** ; un geste destructeur reste sans verbe, sans puce.

---

## 4. Chantiers

Chaque chantier : une branche `feat/…` depuis `dev`, une PR relisible, un
commit par fonctionnalité, la doc `22-fonctionnement.md` mise à jour.

### 4.1 Ce qui est déjà en vol

| Branche | Fichiers qui comptent ici | Commande |
| --- | --- | --- |
| `fix/redemarrage-demande` | `internal/container/*`, `internal/dockerwatch/*`, `internal/alert/source_container.go`, `internal/store/containers.go`, `internal/server/api_containers.go`, `web/src/pages/Container.tsx`, `web/src/lib/containers.ts`, `internal/lang/*.toml`, `docs/projet/22-fonctionnement.md`, **`migrations/022_container_stop_requested.sql`** | `git diff --stat origin/dev...origin/fix/redemarrage-demande` |
| `feat/jeton-portee-machines` | `internal/apitoken/*`, **`internal/verb/*`**, **`internal/server/routes.go`**, **`testdata/routes.txt`**, `internal/server/api_tokens.go`, `web/src/pages/settings/*`, `web/src/api/apiTokens.ts`, `internal/lang/*.toml`, `docs/projet/22-fonctionnement.md`, **`migrations/022_api_token_reach.sql`** | `git diff --stat origin/dev...origin/feat/jeton-portee-machines` |
| `orca/sauvegardes-inventaire` | — : déjà fusionnée dans `dev` (`git merge-base --is-ancestor`) | |

**Les deux branches réservent `022`.** Les noms diffèrent : git ne verra pas
de conflit. Le store lit le numéro (`internal/store/store.go`, `migrationVersion`) :
sur une base neuve, la seconde `INSERT INTO schema_migrations` échoue ; sur une
base qui a joué l'une, l'autre est **sautée sans erreur** (déduit du code, non
exécuté). La seconde fusionnée doit passer en `023`.

Presque tout chantier touche `routes.go`, `routes.txt`, les catalogues et
`22-fonctionnement.md`, que ces deux branches modifient. **Aucun chantier ne
démarre avant leur fusion** (dépendance D0). C1 et C2 n'ont pas cette
dépendance : ils ne touchent ni routes ni verbes ni catalogue de ces domaines
(à revérifier au démarrage).

### 4.2 Numéros de migration réservés

| N° | Chantier | Objet |
| --- | --- | --- |
| 022, 023 | branches en vol | voir §4.1 |
| 024 | C6 | `machine_placement` : `machines.kind`, `kind_override`, `host_override`, `machine_interfaces(machine_id, name, mac)`, `proxmox_guests.macs`, `hypervisor_clusters`, `machine_proxmox.cluster_id` |
| 025 | C8 | `alerts.severity` admet `info` (reconstruction de table) — seulement si Q2 = oui |
| 026 | C9 | `project_publication` (§3.4) |
| 027 | C5 | réservé : seulement si « garder l'historique » demande une marque d'archive distincte de `agents.revoked_at` |

Un test d'unicité des numéros manque (`internal/store/schema_test.go` ne le
vérifie pas) : il entre en C1.

### 4.3 La liste

| # | Chantier | Fichiers possédés | Supprimé | Tests | Migr. | mcp-maison | Dépend de |
| --- | --- | --- | --- | --- | --- | --- | --- |
| C1 | Garde-fous et ménage | `web/src/api/routes.ts` (nouveau), `web/src/api/*.ts` (appels par motif), `web/src/api/parity.test.ts`, `internal/server/parity_test.go`, `testdata/verbs.golden.json`, `Layout.tsx` + `statut/StatusPage.tsx` (mot-symbole), `internal/store/schema_test.go` (unicité des numéros), `internal/server/api_test.go` (message `-update` faux, `./web` → `./internal/server`) | `ObservationKinds`, `hostsProxmox`, `applyStates`, `Field.Select`/`Field.EnumSelect` (`FirewallForm` passe à `components/Select`), les `export` inutiles (§2.2) | parité front (motifs ⊂ routes, verbes ⊂ motifs), route sans appelant, numéros uniques, `TestNameOnlyInBrand` étendu aux `.tsx` | — | aucun | — |
| C2 | `Modal`, `ActionButton`, confirmations | `components/Modal.tsx` + `.scss`, `ActionButton.tsx`, `DeletePlanDialog.tsx`, `DeployCard.tsx`, `useAction.ts`, `_tokens.scss` (`--scrim-blur`), `VisualSystem.tsx` | `confirm` de `useAction`, 9 `window.confirm`, styles de dialogue copiés | `lib/modal.ts` (règle « fermer si propre, demander si saisie »), démo dans `VisualSystem` | — | aucun | — |
| C3 | Registre de modules et menu | `internal/module/*` (nouveau), `internal/store/modules.go`, `internal/server/api_machines.go`, `api_session.go`, `web/src/lib/modules.ts`, `Layout.tsx`, `lib/machineTabs.ts`, `MachineHead.tsx`, `fr/en.toml` (`module.*`, `nav.*`) | `viewEntries`, `settingsEntries`, `counter()` en `if`, `visibleMachineTabs`, `hypervisorTab`, section « Administration » (`nav.section_settings`), entrée Clés SSH | `composed_keys` (`module.`), store (présence par module), golden `machines`, `machine`, `session`, `lib/modules.test.ts`, `machineTabs.test.ts` réécrit | — | `opencloud_machines` voit `modules` (champ en plus, sans casse) | D0, C1 |
| C4 | Assistants Machines | `pages/MachineNew.tsx` → `machines/AddMachine.tsx`, `MachineToken.tsx`, `MachineRights.tsx`, `MachineProxmox.tsx`, `App.tsx`, `RightsFields.tsx`, `lib/machines.ts` (rôles préréglés) | pages `machines/nouvelle`, `jeton`, `:id/droits`, `:id/proxmox` comme pages ; bouton « Changer les droits » | `lib/machines.test.ts` (rôles → droits), attente de l'agent (`lib/enrollWait.ts`) | — | aucun | C2, C3 |
| C5 | Supprimer la machine | `internal/machine/service.go` (`Remove` avec `KeepHistory`), `internal/server/api_machines.go`, `routes.go`, `routes.txt`, `pages/Machine.tsx`, `DeletePlanDialog.tsx` | route `POST /api/machines/{id}/actions/revoke`, `revokeAgent` (front), boutons Révoquer et Retirer, clés `machine.revoke*`, `machine.remove*` | `machine/deletion_test.go`, `api_machines_test.go` (les deux branches de l'option), audit (`agent.revoke` gardé pour l'option historique) | 027 si besoin | aucun (ni revoke ni delete ne sont des verbes) | C2, D0 |
| C6 | Hôte > invités, type, cluster | `internal/hostinfo` (`virt`, interfaces), `internal/agentwire` (champs optionnels), `internal/proxmox/{client,reading}.go` (config des invités, MAC), `internal/store/{machines,proxmox}.go`, `migrations/024_machine_placement.sql`, `web/src/lib/machineTree.ts`, `pages/Machines.tsx`, `pages/Machine.tsx` (deux colonnes, À faire, onglet Agent), `Resources.tsx` | `link` par nom seul (devient repli), `HostedCard`, `ProxmoxCard` (absorbés par l'identité), `ActionsCard` du résumé (vers l'onglet Agent) | `proxmox/reading_test.go` (ordre des clés), `hostinfo` (détection, sans root), `lib/machineTree.test.ts`, goldens `machines`, `machine`, `machine_proxmox`, `schema_test` | 024 | `opencloud_machines`, `opencloud_proxmox` voient des champs en plus | C3 ; à couper en deux PR (serveur ; écrans) |
| C7 | Assistants Services, conteneurs dépliés | `DeclaredServiceForm.tsx`, `ProbeForm.tsx`, `JobForm.tsx`, `Services.tsx`, `ServicesTabs.tsx`, `DirectoryTab.tsx`, `ContainerTable.tsx`, `DeclaredServiceTable.tsx`, `App.tsx` | onglet `services/conteneurs` (redirigé), `ContainersTab`, `FormPage`/`FormCard` pour ces trois formulaires | `lib/directory.test.ts`, `lib/containers.test.ts` (groupage par service), golden inchangé | — | aucun (`/api/containers` reste) | C2, C3 ; après fusion de `fix/redemarrage-demande` (`ContainerTable`, `lib/containers.ts`) |
| C8 | Déclaration automatique + boîte de réception | `internal/directory/{service,discover}.go`, `internal/alert` (genre `service_discovered`, gravité `info`), `migrations/025_*`, `pages/Alerts.tsx`, `pages/alerts/*`, `UndeclaredList.tsx`, `DirectoryCard.tsx`, `lib/gaps.ts` | onglets `acquittees`/`resolues`/`canaux`/`silences` d'Alertes, bouton « Adopter », `adoptPath`, `useProposal` et `GET /api/directory/proposal` (si sans appelant) | `directory/discover_test.go` (déclare, annule = ignore, pas de doublon), `alert` (`info` jamais livrée sous `attention`), goldens `alerts`, `directory_gaps` | 025 | `opencloud_alerts`, `opencloud_directory` : champs et valeurs nouvelles (`declared_by`, `severity`) | C7 |
| C9 | Projet + publication, module « Supervision web » | `internal/directory/projects.go`, `internal/status/*`, `internal/store/{directory_projects,status_*}.go`, `migrations/026_project_publication.sql`, `internal/server/api_{directory,status}.go`, `routes.go`, `routes.txt`, `ProjectForm.tsx`, `StatusAdmin.tsx`, `pages/status/*`, `Probes.tsx`, `lib/modules.ts` | §3.4 « Ce qui disparaît » | migration (pages sans projet, avec projet, noms en collision), `status` (titre, composants dérivés), `public/*` (adresse inchangée), goldens | 026 | **`opencloud_status` à réécrire** ; `GET /api/directory/projects` ouverte aux jetons | C3, C7 |
| C10 | Paramètres › Notifications | `pages/Settings.tsx`, `pages/settings/NotificationsSection.tsx`, `AlertChannelForm.tsx`, `AlertMuteNew.tsx`, `DiskSection.tsx`, `lib/settingsSections.ts`, `App.tsx`, `lib/alerts.ts` (`mutePathOf`) | routes `alertes/canaux*`, `alertes/silences*` (redirigées), ancre `alertes` | `settingsSections.test.ts`, `alerts.test.ts` | — | aucun (routes API inchangées) | C2, C8 |
| C11 | Derniers formulaires | `FirewallForm.tsx`, `Backups.tsx`, `backups/PlanForm.tsx`, `StatusIncident*.tsx`, `StatusComponentForm.tsx` | `FormPage.tsx`, `FormPage.scss`, `FormCard` (hors `FormBody`), `width="wizard"` | `StatusComponentForm.test.tsx` adapté ; route sans page (`parity_test.go`) | — | aucun | C2 ; Sauvegardes après #372/#373 (doc 25, « Ordre de reprise ») |

Ordre : C1 ∥ C2 → (D0) → C3 → C4 ∥ C5 → C6 → C7 → C8 → C9 → C10 → C11.
C1, C2 peuvent partir tout de suite.

---

## 5. Risques et questions pour Lucas

### Risques

| Risque | Ma recommandation |
| --- | --- |
| Double migration `022` dans deux branches en vol : perte silencieuse sur une base existante | Renuméroter la seconde en `023` avant sa fusion ; test d'unicité en C1 |
| La refonte touche les mêmes fichiers que les branches en vol | Fusionner les deux branches avant C3 (D0) |
| `opencloud_status` de mcp-maison casse à la fusion projet/page | Livrer C9 et la PR mcp-maison ensemble |
| Les pages sans test (`Machine.tsx`, `Services.tsx`, `Alerts.tsx`… aucun `*.test.tsx`) | Chaque chantier sort sa logique dans `lib/` et la teste ; l'écran se vérifie au navigateur (17-conventions) |
| `apply --prune` supprime un service découvert absent du fichier | Q7 |
| Détection du type faussée sur un VPS (KVM sans hôte) | Correction manuelle visible dans l'identité ; `vps` par défaut sans hôte |
| Écart entre la doc et le code : « 32 lectures » (`22-fonctionnement.md:31,102,3790,4776`) contre 35 dans `apiTokenRoutes` | Corriger la doc dans le premier chantier qui touche les jetons (hors périmètre ici) |

### Questions

| # | Question | Ma recommandation |
| --- | --- | --- |
| Q1 | Parité : un geste destructeur (supprimer une machine) reste-t-il sans verbe, comme le dit la doc 25 ? | Oui : toute action d'écran est une route ; tout verbe a son écran ; détruire reste à l'interface |
| Q2 | « Nouveau service détecté » : une alerte de gravité `info` (une liste, migration 025) ou une table à part (deux listes) ? | Gravité `info` : une seule boîte de réception |
| Q3 | Où se configure un module absent du menu (premier pare-feu, premier trousseau) ? | Paramètres › Modules, plus une carte sur la vue d'ensemble tant que rien n'est configuré |
| Q4 | La matrice globale des clés SSH (`/cles-ssh`) : garder une vue globale, et où ? | Un onglet « Clés SSH » de la page Machines (vue de l'ensemble), la fiche gardant le sien |
| Q5 | Une page publiée sans composant : montrer un composant par service du projet ? Et le titre public : celui du projet ? | Oui aux deux ; les composants à la main restent possibles |
| Q6 | Nom du module « sondes + pages publiques » : « Supervision web » (maquette), « Disponibilité » ? | « Disponibilité » : le mot est déjà connu des opérateurs et des adresses (`/disponibilite`) |
| Q7 | Déclaration automatique : activée par défaut ? Et un service découvert est-il exporté et soumis à `--prune` comme les autres ? | Activée par défaut, réglable ; exporté comme les autres (il est déclaré) |
| Q8 | Disques physiques et SMART : `smartctl` demande root, l'agent n'en a pas. Un droit nouveau (`disks.smart`, sudo borné) ou les disques sans SMART d'abord ? | Disques et pools sans SMART en C6 ; SMART comme droit à part, plus tard |
| Q9 | « Garder l'historique » à la suppression = la révocation actuelle (machine gardée, agent coupé) ? | Oui, sans migration ; la machine s'affiche « archivée » dans la liste |
| Q10 | Les routes de formulaire (`…/nouveau`) restent-elles des adresses (routes modales) ? | Oui : les liens profonds existants en dépendent (§3.2) |

---

## Non vérifié

| Point | Pourquoi |
| --- | --- |
| Le comportement réel de la double `022` (échec ou saut) | Déduit de `internal/store/store.go`, aucune fusion simulée |
| La lecture de la MAC d'un invité par `/nodes/{node}/{qemu,lxc}/{vmid}/config` avec un jeton `PVEAuditor` | Non essayé contre un Proxmox ; à valider au début de C6 |
| `/sys/class/dmi/id/product_uuid` illisible sans root | Connu des distributions courantes, non vérifié sur une machine de test |
| `systemd-detect-virt` présent partout | Absent hors systemd (Alpine, conteneurs) : alors `unknown` |
| La migration `026` | Esquisse non exécutée ; forme des identifiants (page : `ident.New()`, `internal/status/page.go:20` ; projet : préfixe `prj_`) et collision de noms à vérifier contre `internal/status` et `TestSchema_*` |
| Le sort de `GET /api/directory/proposal` | Dépend des appelants restants après C8 |
| Les numéros de ligne marqués d'une plage | Relevés par lecture, ±5 lignes possibles pour quelques fichiers longs |
| Aucun test lancé | `go test`, `make front-check` non exécutés : le document ne change pas de code |
| Les 8 autres branches distantes | Seules les trois branches nommées ont été comparées à `dev` |
| knip sans config | Lancé sur une copie de `web/` hors dépôt (`npm ci`), version 5.88.1 ; les exports « inutiles » ont été vérifiés un à un par `grep`, les 38 types non |
