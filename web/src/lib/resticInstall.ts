// Ce qu'il faut installer quand restic ou rest-server manque, en commandes
// prêtes à coller : rien à remplacer, l'architecture et le système viennent
// de l'enrôlement de la machine. L'agent ne télécharge jamais un binaire :
// l'opérateur le pose, puis redémarre l'agent pour qu'il le voie.

// La version de rest-server que l'écran propose, et d'où elle vient.
export const restServerVersion = "0.14.0";
const restServerDownloads = `https://github.com/restic/rest-server/releases/download/v${restServerVersion}/`;
export const restServerReleasePage = `https://github.com/restic/rest-server/releases/tag/v${restServerVersion}`;
export const resticReleasePage = "https://github.com/restic/restic/releases/latest";

// Les architectures dont rest-server publie une archive Linux et que l'écran
// sait nommer ; une autre mène à la page des versions, jamais à une commande
// inventée.
const restServerArchitectures = ["amd64", "arm64"];

// Les systèmes dont les paquets portent restic sous ce nom.
const aptSystems = /^(debian|ubuntu|raspbian)\b/i;

export type MissingSoftware = "restic" | "rest-server";

// Une étape : son titre (clé du catalogue) et la commande à copier.
interface InstallStep {
  title: string;
  command: string;
}

// Des étapes à copier, ou la page où prendre le binaire à la main.
type InstallPlan = { steps: InstallStep[]; page: null } | { steps: []; page: string };

export function restServerInstall(arch: string): InstallPlan {
  if (!restServerArchitectures.includes(arch)) {
    return { steps: [], page: restServerReleasePage };
  }
  const archive = `rest-server_${restServerVersion}_linux_${arch}`;
  return {
    page: null,
    steps: [
      { title: "backup.install_download", command: `curl -fsSLO ${restServerDownloads}${archive}.tar.gz` },
      { title: "backup.install_sums", command: `curl -fsSLO ${restServerDownloads}SHA256SUMS` },
      { title: "backup.install_verify", command: "sha256sum -c --ignore-missing SHA256SUMS" },
      { title: "backup.install_extract", command: `tar -xzf ${archive}.tar.gz` },
      { title: "backup.install_place", command: `sudo install -m 0755 ${archive}/rest-server /usr/local/bin/rest-server` },
    ],
  };
}

export function resticInstall(os: string): InstallPlan {
  if (!aptSystems.test(os.trim())) {
    return { steps: [], page: resticReleasePage };
  }
  return { page: null, steps: [{ title: "backup.install_apt", command: "sudo apt install restic" }] };
}

// Le logiciel qu'un refus ou une raison d'échec dit manquer : la clé du
// refus du serveur, ou le mot fermé de l'agent ; null pour tout le reste.
export function missingSoftware(reason: string): MissingSoftware | null {
  if (reason === "restic.restic_missing" || reason === "restic_missing") {
    return "restic";
  }
  if (reason === "restic.rest_server_missing" || reason === "rest_server_missing") {
    return "rest-server";
  }
  return null;
}

export function installPlan(software: MissingSoftware, machine: { os: string; arch: string }): InstallPlan {
  return software === "rest-server" ? restServerInstall(machine.arch) : resticInstall(machine.os);
}
