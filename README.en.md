# openCloud

[Français](README.md) · Contributing: see [AGENTS.md](AGENTS.md) and [docs/projet/](docs/projet/) (in French)

[![ci](https://github.com/ldesfontaine/opencloud/actions/workflows/ci.yml/badge.svg?branch=dev)](https://github.com/ldesfontaine/opencloud/actions/workflows/ci.yml)

**openCloud is self-hosted monitoring** that **declares** what should be
running, **compares** it with what is, and performs a few named **actions**
from the interface (direction: [docs/projet/23-direction.md](docs/projet/23-direction.md), in French).
It follows your machines, their Docker containers, HTTP/TCP probes and their
certificates, scheduled jobs by ping, alerts through signed webhooks, a public
status page, detected image updates, and a read-only API token for an AI agent,
through mcp-maison. One Go binary, with SQLite and the web interface embedded.

> **Maturity.** Version `v0.0.1`, pre-release. Linux amd64 and arm64
> binaries are in the [v0.0.1 release](https://github.com/ldesfontaine/opencloud/releases/tag/v0.0.1),
> or you build them. A fresh install has **no account**: it serves only the
> first-setup screen, and whoever opens it first creates the account. Create it
> before exposing the port beyond the machine.

![openCloud overview with example machines](docs/images/vue-ensemble.png)

## What it does, what it does not

| Does | Does not |
| --- | --- |
| Samples each machine every 10 s: CPU, memory, disks, network | Act on a machine except through **signed orders**, one per action, which its agent accepts according to the **rights ticked when it was installed** (beyond `observe`, none by default). No shell, no free-form command, nothing through the API token |
| Follows Docker containers: state, health, restarts, logs, networks | Write to Docker, except to deploy a declared service (`docker compose up`, Compose file **generated** from the directory, never typed in) |
| Declares your services (interface, or YAML file: `opencloud export` / `opencloud apply`) and shows the gap with what runs | Declare anything on its own: an unknown container shows as "undeclared". The YAML file is applied by the command only |
| Deploys or redeploys a declared Compose service from its page; the `.env` goes once with the order, written on the machine, never kept by openCloud | Remove a deployment: deleting the service from the directory leaves containers and folder on the machine |
| Probes URLs and ports, checks certificates and their expiry | Renew a certificate |
| Watches a cron job or a backup through a ping URL | Run the job |
| Compares each image to its registry and builds the update command | Apply an update to a service it did not deploy: you run the command. For one it **deployed**: new version in the directory, then "Redeploy", with no automatic rollback |
| Backs up the database every night, once the Backups page is filled in: encrypted (`age`), sent to the agent of the machine you choose; `serve` keeps no copy | Back up a service's `data/` or `.env`: the database and its two keys only. Rule out the openCloud machine as a destination: choose another one. Lose the passphrase and no backup can be read. A failure shows on the page, with no alert |
| Keeps an audit log of every writing action and every order, also sent to the channels you tick | Keep the trace if you tick no channel: whoever wipes the database wipes it |
| Updates its agents from a signed version you import (`opencloud release import`) | Offer a signed version yet: none is published, and `v0.0.1` does not embed the key that verifies them. The first signed version is installed by hand, server and agents; the next ones go through this action |
| Sends alerts as signed webhooks (Discord, Slack, Mattermost, ntfy, JSON), by e-mail through your SMTP relay, or to a Matrix room | SMS |
| Publishes public status pages under `/statut/<address>` | Multiple accounts or a second factor of its own: behind an SSO (Authelia), `forward` mode delegates them to it |
| Gives an AI agent a read-only API token, for mcp-maison | Any write or action on a machine through that token, any secret returned |

openCloud observes, and acts only through named, signed, logged actions that
each machine allows when it is installed. The interface is available in
English and French (FR/EN switch at the bottom of the sidebar). The
documentation is in French.

## Quick start

Needs Go 1.26.10+, Node.js 20.19+, `make` and `git`. Linux only.

```sh
git clone https://github.com/ldesfontaine/opencloud.git
cd opencloud
make run          # builds bin/opencloud, writes dev/config.toml, starts on 127.0.0.1:8080
```

Open <http://127.0.0.1:8080> and **create the account** on the first-setup
screen (username, password of 12 characters or more); the interface then
opens. Lost password: `opencloud auth disable -config dev/config.toml` brings
back the first-setup screen. To add a machine: **Machines → Add a machine**, then paste
the displayed command on it as root
(`printf '%s\n' 'oc_…' | sudo opencloud agent -server https://…`). The
openCloud machine has an agent too, like any machine: `serve` observes nothing
of it, and without that agent it does not show. After you create the account,
the first-setup screen displays the line that installs it (observe right only);
paste it on that machine, as root. It shows once and lasts 1 h: after that,
"Add a machine", played on it, gives a new one (or Re-enroll, on its page).

Server installation with Debian, systemd and Traefik, updates, backups:
[docs/installation.md](docs/installation.md) (French). How it works in
detail: [docs/projet/22-fonctionnement.md](docs/projet/22-fonctionnement.md).

## License

[Apache 2.0](LICENSE).
