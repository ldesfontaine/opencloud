# openCloud

[Français](README.md) · Contributing: see [AGENTS.md](AGENTS.md) and [docs/projet/](docs/projet/) (in French)

[![ci](https://github.com/ldesfontaine/opencloud/actions/workflows/ci.yml/badge.svg?branch=dev)](https://github.com/ldesfontaine/opencloud/actions/workflows/ci.yml)

**openCloud is self-hosted monitoring**: your machines, their Docker
containers (read-only), HTTP/TCP probes and their certificates, scheduled jobs
by ping, alerts through signed webhooks, a public status page, detected image
updates, and an MCP server for AI agents. One Go binary, with SQLite and the
web interface embedded.

> **Maturity.** Version `v0.0.1`, pre-release. No binary is published: you
> build it. The interface is **open by default**: enable authentication
> before exposing it beyond the machine.

![openCloud overview with example machines](docs/images/vue-ensemble.png)

## What it does, what it does not

| Does | Does not |
| --- | --- |
| Samples each machine every 10 s: CPU, memory, disks, network | Start, stop or restart anything on a machine |
| Follows Docker containers: state, health, restarts, logs, networks | Write to Docker: the socket is read, no write verb exists |
| Probes URLs and ports, checks certificates and their expiry | Renew a certificate |
| Watches a cron job or a backup through a ping URL | Run the job |
| Compares each image to its registry and builds the update command | Apply the update: you run it |
| Sends alerts as signed webhooks (Discord, Slack, Mattermost, ntfy, JSON) | E-mail, SMS |
| Publishes a status page under `/statut` | Multiple accounts, second factor |
| Serves MCP: 16 read tools, 9 writes the interface already has | Any action on a machine through MCP, any secret returned |

openCloud **observes; it never acts on a machine**. The interface is
available in English and French (FR/EN switch at the bottom of the sidebar).
The documentation is in French.

## Quick start

Needs Go 1.26.9+, Node.js 20.19+, `make` and `git`. Linux only.

```sh
git clone https://github.com/ldesfontaine/opencloud.git
cd opencloud
make run          # builds bin/opencloud, writes dev/config.toml, starts on 127.0.0.1:8080
```

Open <http://127.0.0.1:8080>, then **Settings → Interface access** to enable
authentication. To add a machine: **Machines → Add a machine**, then paste
the displayed command on it as root
(`sudo opencloud agent -server https://… -token oc_…`). Do not enroll an
agent on the openCloud machine itself: it is already monitored, and an agent
there would duplicate its services and alerts. To try it, use another machine
or a VM.

Server installation with Debian, systemd and Traefik, updates, backups:
[docs/installation.md](docs/installation.md) (French). How it works in
detail: [docs/projet/22-fonctionnement.md](docs/projet/22-fonctionnement.md).

## License

[Apache 2.0](LICENSE).
