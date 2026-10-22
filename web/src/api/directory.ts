import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import type { ContainerState } from "./containers";
import type { ActionRun } from "./actions";
import type { DeletionPlan } from "./deletion";

// L'annuaire : ce que l'opérateur déclare (services, projets), l'écart avec
// ce que Docker montre, et ce qu'il choisit d'ignorer. Sujet du direct :
// « directory » ; l'écart suit aussi « containers » (topicsOf).

// Les listes sont fermées côté serveur ; le front les traduit par leur mot.
export type Nature = "application" | "infra";
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

export interface Project {
  id: string;
  name: string;
  title: string;
  service_ids: string[];
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
type IgnoreKind = "compose_project" | "container_name";

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
  // Le service déclaré du même projet Compose : là où « Adopter » ajoute une unité.
  service_id: string;
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
type ServiceUnitRequest = Omit<ServiceUnit, "kind">;

export interface ServiceRequest {
  machine_id: string;
  name: string;
  title: string;
  nature: Nature;
  origin: Origin;
  compose_project: string;
  public_url: string;
  provides: Capability[];
  units: ServiceUnitRequest[];
  project_ids: string[];
}

// Une unité que la proposition ajoute n'a pas encore de politique.
type ProposedUnit = Omit<ServiceUnitRequest, "update_policy"> & { update_policy?: UpdatePolicy };

// Ce que « Adopter » pré-remplit : le corps de la création, à ses unités
// près. Avec service_id, c'est la modification de ce service.
export interface ProposalResponse {
  service_id: string;
  definition: Omit<ServiceRequest, "units"> & { units: ProposedUnit[] };
}

interface ProjectRequest {
  name: string;
  title: string;
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
const ignoredPath = `${directoryPath}/ignored`;

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
export function unignorePath(key: IgnoreKey): string {
  const query = new URLSearchParams({ machine: key.machine_id, kind: key.kind, value: key.value });
  return `${ignoredPath}?${query.toString()}`;
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

// Ce que « Adopter » propose pour ce conteneur ; containerID nul : rien à lire.
export function useProposal(containerID: string | null): Resource<ProposalResponse> {
  return useResource<ProposalResponse>(containerID === null ? null : () => `${directoryPath}/proposal?container=${segment(containerID)}`);
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
  return id === undefined ? api.post<DeclaredService>(servicesPath, body) : api.put<DeclaredService>(servicePath(id), body);
}

// Les projets restent, seul le lien s'efface. Un service lié est refusé
// en 409, avec le plan qui dit ce qui le lie.
export async function deleteDeclaredService(id: string): Promise<void> {
  return api.delete(servicePath(id));
}

export async function fetchDeclaredServiceDeletePlan(id: string): Promise<DeletionPlan> {
  return api.get<DeletionPlan>(`${servicePath(id)}/delete-plan`);
}

// Sans id, une création ; avec, le remplacement de ce projet.
export async function saveProject(id: string | undefined, body: ProjectRequest): Promise<Project> {
  return id === undefined ? api.post<Project>(projectsPath, body) : api.put<Project>(projectPath(id), body);
}

// Les services du projet gardent leur déclaration.
export async function deleteProject(id: string): Promise<void> {
  return api.delete(projectPath(id));
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
  return api.post<ActionRun>(`${servicePath(id)}/actions/${redeploy ? "redeploy" : "deploy"}`, { env });
}

// Change la version déclarée de l'unité, puis redéploie son service : un
// geste. La version est celle que l'agent propose, rien d'autre.
export async function upgradeUnit(unitID: string, version: string): Promise<ActionRun> {
  return api.post<ActionRun>(`${directoryPath}/units/${segment(unitID)}/actions/upgrade`, { version });
}

export async function ignoreObservation(key: IgnoreKey): Promise<void> {
  return api.post(ignoredPath, key);
}

export async function unignoreObservation(key: IgnoreKey): Promise<void> {
  return api.delete(unignorePath(key));
}

// La politique vit sur l'unité de l'annuaire, pas sur le conteneur : elle
// survit à sa recréation.
export async function saveUpdatePolicy(unitID: string, policy: UpdatePolicy): Promise<void> {
  return api.put(`${directoryPath}/units/${segment(unitID)}/update-policy`, { policy });
}
