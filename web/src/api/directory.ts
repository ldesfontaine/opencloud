import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import { route } from "./routes";
import type { ContainerState } from "./containers";
import type { ActionRun } from "./actions";
import type { DeletionPlan } from "./deletion";

// L'annuaire : ce que l'opérateur déclare (services, projets), l'écart avec
// ce que Docker montre, et ce qu'il choisit d'ignorer. Sujet du direct :
// « directory » ; l'écart suit aussi « containers » (topicsOf).

// Les listes sont fermées côté serveur ; le front les traduit par leur mot.
export type Nature = "application" | "infra";
// Ce qu'est le service ; vide dans une requête : openCloud la déduit de l'image.
export type Category = "web" | "sso" | "database" | "security" | "monitoring" | "application";
export type Origin = "compose" | "container" | "binary";
export type Capability = "sso" | "proxy" | "tls" | "backup-target";
type DeclaredBy = "operator";

// Ce que l'opérateur veut des mises à jour d'une unité : les suivre (watch),
// les taire (hold : vérifiées, jamais montrées), ne jamais interroger le
// registre (ignore).
export type UpdatePolicy = "watch" | "hold" | "ignore";

// Une pièce d'un service déclaré. Pour un service Compose, son nom est celui
// du service dans le projet.
export interface ServiceUnit {
  id: string;
  name: string;
  kind: "container" | "binary";
  image: string;
  version: string;
  digest: string;
  container_name: string;
  systemd_unit: string;
  ports: string[];
  // Le dossier du conteneur monté depuis data/<unité> au déploiement ; vide : aucun.
  data_path: string;
  // « watch » pour un binaire : la politique ne vaut que pour un conteneur.
  update_policy: UpdatePolicy;
  // Ce que le relevé des unités systemd dit d'une unité binaire ; nul pour un conteneur.
  observed: ObservedUnit | null;
}

// found : l'unité est sur la machine, avec ses états ; missing : systemd est lu
// et ne la connaît pas ; unknown : la machine n'a rien relevé (états vides).
type Presence = "found" | "missing" | "unknown";

export interface ObservedUnit {
  presence: Presence;
  load: string;
  active: string;
  sub: string;
  reported_at: string | null;
}

// Ce qui vise un service déclaré : tant que cette liste n'est pas vide, il
// ne se supprime pas.
export interface LinkedObject {
  id: string;
  name: string;
}

export interface ServiceLinks {
  probes: LinkedObject[];
  components: LinkedObject[];
}

export interface DeclaredService {
  id: string;
  machine_id: string;
  machine_name: string;
  name: string;
  title: string;
  nature: Nature;
  // La correction de l'opérateur, vide si le type se déduit ; category_deduced
  // est ce que l'image en dit, toujours posée.
  category: Category | "";
  category_deduced: Category;
  origin: Origin;
  compose_project: string;
  public_url: string;
  declared_by: DeclaredBy;
  provides: Capability[];
  units: ServiceUnit[];
  project_ids: string[];
  created_at: string;
  updated_at: string;
}

// La page publique d'un projet, à /statut/<slug>. La langue est résolue par
// le serveur : vide en base, c'est celle de l'interface.
interface ProjectPublication {
  slug: string;
  announcement: string;
  language: string;
  public_url: string;
  published_at: string;
}

// Un projet regroupe des services ; publication nul : il n'a pas de page
// publique. Le titre de la page est le sien.
export interface Project {
  id: string;
  name: string;
  title: string;
  service_ids: string[];
  publication: ProjectPublication | null;
  created_at: string;
  updated_at: string;
}

// La fiche d'un service déclaré dit en plus ce qui le lie.
export interface DeclaredServiceDetail extends DeclaredService {
  links: ServiceLinks;
}

export interface DeclaredServicesResponse {
  services: DeclaredService[];
}

export interface ProjectsResponse {
  projects: Project[];
}

// L'écart entre l'annuaire et ce que les machines montrent. Les états sont
// des faits : le front en fait une pastille et un libellé.
export type GapState = "conform" | "absent" | "diverged" | "unverifiable" | "unobserved";
type GapReason = "image_differs" | "digest_differs" | "tag_latest" | "version_unpinned" | "tag_differs" | "digest_unknown" | "image_unreadable";
type MatchLevel = "label" | "compose" | "name";
// Pourquoi les labels d'un conteneur ne le rattachent à rien ; le serveur
// dit vide quand il n'y a pas de label orphelin.
export type LabelProblem = "invalid" | "incomplete" | "service_unknown" | "unit_unknown" | "unit_binary";
type IgnoreKind = "compose_service" | "compose_project" | "container_name";

interface ContainerVerdict {
  // L'identifiant de la fiche du conteneur, pas celui d'un service déclaré.
  id: string;
  name: string;
  state: ContainerState;
  image: string;
  match_level: MatchLevel | "";
  verdict: GapState;
  reason: GapReason | "";
}

interface UnitGap {
  unit_id: string;
  name: string;
  kind: "container" | "binary";
  state: GapState;
  weak: boolean;
  containers: ContainerVerdict[];
}

export interface ServiceGap {
  service_id: string;
  state: GapState;
  // Faux : la machine du service ne signale plus, l'écart est celui d'avant
  // son silence.
  observed: boolean;
  weak: boolean;
  units: UnitGap[];
}

export interface UndeclaredContainer {
  id: string;
  name: string;
  state: ContainerState;
  image: string;
  compose_service: string;
  label_service: string;
  label_unit: string;
  label_problem: LabelProblem | "";
}

// Ce qui tourne sans être déclaré, regroupé par la clé qu'on peut ignorer.
export interface UndeclaredGroup {
  machine_id: string;
  machine_name: string;
  key_kind: IgnoreKind;
  key_value: string;
  ignored: boolean;
  // Le groupe d'exécution (projet Compose) et le service Compose du groupe ;
  // vides pour un conteneur seul.
  project: string;
  compose_service: string;
  // Au moins un conteneur du groupe n'a pas de label : un groupe d'orphelins
  // se corrige par le label, pas par l'adoption.
  adoptable: boolean;
  containers: UndeclaredContainer[];
}

export interface GapsResponse {
  services: ServiceGap[];
  undeclared: UndeclaredGroup[];
}

// Ce que la fiche d'un conteneur dit de l'annuaire.
export interface ContainerDirectory {
  declared: boolean;
  service_id: string;
  service_name: string;
  unit_id: string;
  unit_name: string;
  match_level: MatchLevel | "";
  weak: boolean;
  key_kind: IgnoreKind;
  key_value: string;
  ignored: boolean;
  // Les labels du conteneur, et pourquoi ils ne le rattachent à rien ;
  // label_machine_name nomme la machine qui déclare le service visé quand
  // c'en est une autre.
  label_service: string;
  label_unit: string;
  label_problem: LabelProblem | "";
  label_machine_name: string;
}

// Le corps de la création comme de l'édition d'un service ; lib/directory
// le construit depuis le formulaire. Une unité y est celle que le serveur
// rend, sans son genre, que le corps ne porte pas.
type ServiceUnitRequest = Omit<ServiceUnit, "kind" | "observed">;

export interface ServiceRequest {
  machine_id: string;
  name: string;
  title: string;
  nature: Nature;
  category: Category | "";
  origin: Origin;
  compose_project: string;
  public_url: string;
  provides: Capability[];
  units: ServiceUnitRequest[];
  project_ids: string[];
}

// Une unité que la proposition ajoute n'a pas encore de politique.
type ProposedUnit = Omit<ServiceUnitRequest, "update_policy"> & { update_policy?: UpdatePolicy };

// Ce que « Accepter » écrit et que « Modifier avant d'accepter » pré-remplit :
// le corps de la création, à ses unités près.
export interface ProposalResponse {
  definition: Omit<ServiceRequest, "units"> & { units: ProposedUnit[] };
}

// publication nul : le projet n'est pas publié.
export interface ProjectRequest {
  name: string;
  title: string;
  publication: { slug: string; announcement: string; language: string } | null;
}

// Ce que l'opérateur dit voulu : une clé d'observation, pas un id Docker.
export interface IgnoreKey {
  machine_id: string;
  kind: IgnoreKind;
  value: string;
}

const directoryPath = "/api/directory";
const servicesPath = `${directoryPath}/services`;
const projectsPath = `${directoryPath}/projects`;

function servicePath(id: string): string {
  return `${servicesPath}/${segment(id)}`;
}

function projectPath(id: string): string {
  return `${projectsPath}/${segment(id)}`;
}

// Le filtre passe dans le chemin : changer de machine ou de projet relit
// l'écart, que le serveur calcule pour ce filtre.
export function gapsPath(machineID: string, projectID: string): string {
  const filters: string[] = [];
  if (machineID !== "") {
    filters.push(`machine=${segment(machineID)}`);
  }
  if (projectID !== "") {
    filters.push(`project=${segment(projectID)}`);
  }
  const path = `${directoryPath}/gaps`;
  return filters.length === 0 ? path : `${path}?${filters.join("&")}`;
}

// La clé passe en paramètres de requête : sa valeur est un nom de projet ou
// de conteneur, pas un identifiant, URLSearchParams l'encode.
function unignoreQuery(key: IgnoreKey): URLSearchParams {
  return new URLSearchParams({ machine: key.machine_id, kind: key.kind, value: key.value });
}

// wanted faux : rien à lire (le sélecteur d'un silence, tant qu'un autre genre est choisi).
export function useDeclaredServices(wanted = true): Resource<DeclaredServicesResponse> {
  return useResource<DeclaredServicesResponse>(wanted ? servicesPath : null);
}

// Les services qu'un projet regroupe ; projectID nul : rien à lire.
export function useProjectServices(projectID: string | null): Resource<DeclaredServicesResponse> {
  return useResource<DeclaredServicesResponse>(projectID === null ? null : () => `${servicesPath}?project=${segment(projectID)}`);
}

// id nul : rien à lire (un formulaire de création).
export function useDeclaredService(id: string | null): Resource<DeclaredServiceDetail> {
  return useResource<DeclaredServiceDetail>(id === null ? null : () => servicePath(id));
}

export function useProjects(): Resource<ProjectsResponse> {
  return useResource<ProjectsResponse>(projectsPath);
}

// id nul : rien à lire (un formulaire de création).
export function useProject(id: string | null): Resource<Project> {
  return useResource<Project>(id === null ? null : () => projectPath(id));
}

// D'où part une proposition : un conteneur que la machine montre, ou une unité
// systemd qu'elle a relevée.
export type ProposalSource = { container: string } | { machine: string; unit: string };

function proposalPath(source: ProposalSource): string {
  if ("container" in source) {
    return `${directoryPath}/proposal?container=${segment(source.container)}`;
  }
  // Le nom d'une unité n'est pas un identifiant (il porte un point) : il
  // s'échappe comme un texte, la machine, elle, reste un identifiant.
  return `${directoryPath}/proposal?machine=${segment(source.machine)}&unit=${encodeURIComponent(source.unit)}`;
}

// Ce que le serveur propose pour cette source ; source nulle : rien à lire.
export function useProposal(source: ProposalSource | null): Resource<ProposalResponse> {
  const path = source === null ? null : proposalPath(source);
  return useResource<ProposalResponse>(path === null ? null : () => path);
}

// La proposition du moment, relue à l'instant d'accepter : si le conteneur est
// ignoré ou déclaré depuis, le serveur la refuse (409) et rien n'est écrit.
export async function fetchProposal(containerID: string): Promise<ProposalResponse> {
  return api.get<ProposalResponse>(proposalPath({ container: containerID }));
}

// L'écart pour une machine et un projet, vides pour tous ; filtre nul :
// rien à lire (la machine du service n'est pas encore connue).
interface GapsFilter {
  machineID: string;
  projectID: string;
}

export function useGaps(filter: GapsFilter | null): Resource<GapsResponse> {
  return useResource<GapsResponse>(filter === null ? null : () => gapsPath(filter.machineID, filter.projectID));
}

// Sans id, une création ; avec, le remplacement de ce service.
export async function saveDeclaredService(id: string | undefined, body: ServiceRequest): Promise<DeclaredService> {
  return id === undefined
    ? api.send<DeclaredService>(route("POST /api/directory/services"), body)
    : api.send<DeclaredService>(route("PUT /api/directory/services/{id}", { id }), body);
}

// Les projets restent, seul le lien s'efface. Un service lié est refusé
// en 409, avec le plan qui dit ce qui le lie. Ses conteneurs qui tournent
// redeviennent une proposition ; avec ignore, ils sont dits voulus du même
// geste.
export async function deleteDeclaredService(id: string, ignore = false): Promise<void> {
  const query = ignore ? new URLSearchParams({ ignore: "true" }) : undefined;
  return api.send(route("DELETE /api/directory/services/{id}", { id }, query));
}

export async function fetchDeclaredServiceDeletePlan(id: string): Promise<DeletionPlan> {
  return api.get<DeletionPlan>(`${servicePath(id)}/delete-plan`);
}

// Sans id, une création ; avec, le remplacement de ce projet.
export async function saveProject(id: string | undefined, body: ProjectRequest): Promise<Project> {
  return id === undefined ? api.send<Project>(route("POST /api/directory/projects"), body) : api.send<Project>(route("PUT /api/directory/projects/{id}", { id }), body);
}

// Les services du projet gardent leur déclaration.
export async function deleteProject(id: string): Promise<void> {
  return api.send(route("DELETE /api/directory/projects/{id}", { id }));
}

export async function fetchProjectDeletePlan(id: string): Promise<DeletionPlan> {
  return api.get<DeletionPlan>(`${projectPath(id)}/delete-plan`);
}

// Le droit du prochain geste de déploiement ; « unknown » tant que l'agent
// n'a pas rapporté ses droits ; « unavailable » : le droit est coché mais le
// compte de l'agent ne peut pas l'exercer (right_reason dit pourquoi). L'ordre
// part quand même : c'est l'agent qui le refuse.
export type DeployRight = "ok" | "missing" | "unavailable" | "unknown";

// Ce que l'opérateur lit avant de déployer : où le service ira, ce qu'il y
// aura, et où il en est.
export interface DeployPlan {
  service_id: string;
  machine_id: string;
  machine_name: string;
  // Vide tant que l'agent n'a pas dit sa racine.
  root_dir: string;
  path: string;
  // Le dossier est écrit sur la machine : le geste est « redéployer ».
  deployed: boolean;
  right: DeployRight;
  // La raison de « unavailable » ; absente sinon.
  right_reason?: string;
  compose: string;
  // Les unités dont un dossier de données est monté.
  data_dirs: string[];
  // Les autres services déclarés qui partagent le projet Compose : tant qu'il
  // y en a, le serveur refuse de déployer (le fichier écrit les retirerait).
  shared_with: string[];
  last_run: ActionRun | null;
}

// id nul : rien à lire. Le plan suit l'annuaire, les ordres et les machines
// (topicsOf).
export function useDeployPlan(id: string | null): Resource<DeployPlan> {
  return useResource<DeployPlan>(id === null ? null : () => `${servicePath(id)}/deploy-plan`);
}

// Le .env part dans ce corps et nulle part ailleurs ; null : aucun. L'ordre
// rendu avance ensuite par le direct.
export async function deployService(id: string, redeploy: boolean, env: string | null): Promise<ActionRun> {
  return api.send<ActionRun>(
    redeploy ? route("POST /api/directory/services/{id}/actions/redeploy", { id }) : route("POST /api/directory/services/{id}/actions/deploy", { id }),
    { env },
  );
}

// Change la version déclarée de l'unité, puis redéploie son service : un
// geste. La version est celle que l'agent propose, rien d'autre.
export async function upgradeUnit(unitID: string, version: string): Promise<ActionRun> {
  return api.send<ActionRun>(route("POST /api/directory/units/{id}/actions/upgrade", { id: unitID }), { version });
}

export async function ignoreObservation(key: IgnoreKey): Promise<void> {
  return api.send(route("POST /api/directory/ignored"), key);
}

export async function unignoreObservation(key: IgnoreKey): Promise<void> {
  return api.send(route("DELETE /api/directory/ignored", unignoreQuery(key)));
}

// La politique vit sur l'unité de l'annuaire, pas sur le conteneur : elle
// survit à sa recréation.
export async function saveUpdatePolicy(unitID: string, policy: UpdatePolicy): Promise<void> {
  return api.send(route("PUT /api/directory/units/{id}/update-policy", { id: unitID }), { policy });
}
