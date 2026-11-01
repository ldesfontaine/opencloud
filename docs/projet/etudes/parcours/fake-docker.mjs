// Un faux démon Docker, pour un banc sans Docker : il répond aux seules
// requêtes que l'agent lit, avec deux conteneurs d'un projet Compose
// « demo-shop » (web, db) et un conteneur isolé. Rien ne tourne vraiment.
// Usage : node fake-docker.mjs <socket> [nom-du-projet]
import http from "node:http";
import fs from "node:fs";
import crypto from "node:crypto";

const socket = process.argv[2];
const project = process.argv[3] ?? "demo-shop";
const now = Date.now();
const started = new Date(now - 3 * 3600_000).toISOString();

function compose(service, image, ports, health) {
  const id = crypto.createHash("sha256").update(socket + service).digest("hex");
  return {
    id, name: `${project}-${service}-1`, image, health, ports,
    labels: {
      "com.docker.compose.project": project,
      "com.docker.compose.service": service,
      "com.docker.compose.oneoff": "False",
      "com.docker.compose.project.working_dir": `/srv/workspace/${project}`,
      "com.docker.compose.project.config_files": `/srv/workspace/${project}/compose.yml`,
      ...(service === "web" ? { "com.docker.compose.depends_on": "db:service_started:false" } : {}),
    },
  };
}
const all = [
  compose("web", "nginx:1.27", [{ IP: "0.0.0.0", PrivatePort: 80, PublicPort: 18481, Type: "tcp" }], "healthy"),
  compose("db", "postgres:16", [], undefined),
  { id: crypto.createHash("sha256").update(socket + "grafana").digest("hex"), name: "grafana-solo", image: "grafana/grafana:11.2.0", labels: {}, ports: [{ IP: "0.0.0.0", PrivatePort: 3000, PublicPort: 3000, Type: "tcp" }] },
];

const summary = (c) => ({ Id: c.id, Names: ["/" + c.name], Image: c.image, ImageID: "sha256:" + "c".repeat(64), State: "running", Created: Math.floor(now / 1000) - 10800, Labels: c.labels, Ports: c.ports });
const inspect = (c) => ({
  Id: c.id, Name: "/" + c.name, Image: "sha256:" + "c".repeat(64), Created: started, RestartCount: 0,
  State: { Status: "running", ExitCode: 0, OOMKilled: false, StartedAt: started, FinishedAt: "0001-01-01T00:00:00Z", ...(c.health ? { Health: { Status: c.health } } : {}) },
  Config: { Image: c.image, Tty: false, Labels: c.labels, ...(c.health ? { Healthcheck: { Test: ["CMD", "true"] } } : {}) },
  HostConfig: { NetworkMode: `${project}_default`, Privileged: false },
  NetworkSettings: { Ports: {}, Networks: { [`${project}_default`]: { NetworkID: "d".repeat(64), IPAddress: "172.30.0.2", Aliases: [c.name] } } },
  Mounts: [],
});

const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://docker");
  const json = (body) => { res.writeHead(200, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
  if (url.pathname.endsWith("/_ping")) { res.writeHead(200, { Server: "Docker/27.3.1 (linux)", "Api-Version": "1.47" }); return res.end("OK"); }
  if (url.pathname.endsWith("/containers/json")) return json(all.map(summary));
  let m = url.pathname.match(/\/containers\/([^/]+)\/json$/);
  if (m) { const c = all.find((x) => x.id === m[1] || x.name === m[1]); return c ? json(inspect(c)) : (res.writeHead(404), res.end("{}")); }
  m = url.pathname.match(/\/containers\/([^/]+)\/stats$/);
  if (m) {
    const t = Date.now();
    return json({ read: new Date(t).toISOString(), cpu_stats: { cpu_usage: { total_usage: (t - now) * 200_000 }, system_cpu_usage: (t - now) * 4_000_000, online_cpus: 4 }, memory_stats: { usage: 120_000_000, limit: 4_000_000_000, stats: { inactive_file: 10_000_000 } } });
  }
  if (url.pathname.endsWith("/events")) { res.writeHead(200, { "Content-Type": "application/json" }); res.write("\n"); return; }
  if (url.pathname.endsWith("/volumes")) return json({ Volumes: [] });
  if (url.pathname.endsWith("/networks")) return json([{ Id: "d".repeat(64), Name: `${project}_default`, Driver: "bridge", Containers: Object.fromEntries(all.filter((c) => c.labels["com.docker.compose.project"]).map((c) => [c.id, { Name: c.name }])) }]);
  if (url.pathname.includes("/images/")) return json({ Id: "sha256:" + "c".repeat(64), RepoTags: ["x"], RepoDigests: [] });
  res.writeHead(404, { "Content-Type": "application/json" }); res.end('{"message":"not faked"}');
});
try { fs.unlinkSync(socket); } catch {}
server.listen(socket, () => console.log("fake docker on", socket));
