# Cas d'usage — le terrain contre le registre

Ce document confronte **les 53 décisions acquises** de `11-decisions.md` à onze
rapports de terrain sourcés : confirmé, à compléter, ou contredit. Il ne
tranche rien.

## 1. Les personnes

| Qui | Machines | Ce qu'il fait | Ce qui le fait souffrir | Source |
|---|---|---|---|---|
| **Freelance web** | 1 VPS | héberge les sites de ses clients, veut quitter Plesk | licence payante ; partager le serveur = donner root/SSH à tout | coolify#399 ; coolify#5685 |
| **Admin d'une douzaine de VPS** | 12 (2 Pi + 2 serveurs + 8 VPS, ~40 $/mois) | a jeté Proxmox, K8s, Ansible pour des scripts bash + un watchdog | Webmin trop lourd ; maintenance 1-2 h/semaine avant simplification | HN 48746086 ; lowendtalk 203473 |
| **Solo, plusieurs VPS chez plusieurs hébergeurs** | ~5 | laisse cron faire les mises à jour système | une maj auto de PowerDNS a réécrit une config et cassé le DNS en prod | lowendtalk 158866 |
| **Bricoleur d'observation** | 2-3 | Portainer + btop + Uptime Kuma ouverts en permanence | trois interfaces, et toujours SSH pour agir — il a fini par coder son outil | HN 47648922 ; HN 23937193 |
| **Homelab derrière CGNAT** | 2 (une machine à la maison + un VPS relais) | changement de FAI ou 4G : plus d'adresse publique du jour au lendemain | tunnels SSH inversés maison, un port par machine ; ou Tailscale | jeffgeerling.com 2022 ; donaldsimpson.co.uk 2016 ; HN 33680000 |

## 2. Les moments

### Chaque jour

| Moment | Ce qu'il fait | Ce qui casse | Ce qu'il attend | Source |
|---|---|---|---|---|
| Regarder si tout tourne | ouvre trois interfaces | rien ne se répare depuis l'écran, il repasse en SSH | un seul outil qui montre **et** agit | HN 47648922 |
| Déployer | `git push` ou `compose pull && up -d` | le build échoue, l'outil retente à l'infini sans prévenir | une alerte quand un déploiement échoue | caprover#727 |
| Lire les journaux | `tail` local | monter Graylog « pour une douzaine de lignes par jour » est disproportionné | rien à monter | HN 23937193 |

### Chaque semaine

| Moment | Ce qu'il fait | Ce qui casse | Ce qu'il attend | Source |
|---|---|---|---|---|
| Ajouter un site | route + certificat | une typo dans un label Traefik fait disparaître la route sans erreur | des erreurs explicites | budgethomelab.com/articles/npm-vs-caddy-vs-traefik |
| Toucher au proxy | édite la config à la main | config obscure, peur de casser | une validation type `nginx -t` avant d'appliquer | caprover#660 |
| Mettre à jour des conteneurs | `pull` + `up -d`, note l'ID d'image d'avant | ni migration de base ni retour arrière ; 502/503 pendant 18-31 s | une bascule sans coupure, un retour arrière réel | kkovacs.eu/docker-compose-rollback ; coolify#8627 |

### Chaque mois

| Moment | Ce qu'il fait | Ce qui casse | Ce qu'il attend | Source |
|---|---|---|---|---|
| Laisser passer les maj système | `unattended-upgrades` ou cron | le paquet ajoute des fichiers de config en conflit, sans le documenter | des maj qui ne cassent pas en silence | lowendtalk 158866 |
| Redémarrer après patch | `reboot` | le reverse proxy ne repart plus, tous les sites tombent | un redémarrage qui ne casse pas le routage | coolify#7193 |
| Vérifier les sauvegardes | lit un journal « réussi » | « réussi » ≠ restaurable : un bloc référencé n'existe plus | une restauration testée | forum.duplicati.com/t/…/11978 |
| Laisser renouveler les certificats | rien | échec depuis des jours, découvert par un visiteur | une alerte avant expiration | nginx-proxy-manager#3979 ; coolify#4280 |
| Vérifier que la sauvegarde nocturne a tourné | rien : il attend une erreur | le backup a cessé depuis trois semaines, découvert à la restauration | une alerte sur l'**absence** de signal | blog.elest.io self-host-healthchecks |

### Quand ça casse

| Moment | Ce qu'il fait | Ce qui casse | Ce qu'il attend | Source |
|---|---|---|---|---|
| Disque plein | regarde `df` | fichiers supprimés mais tenus ouverts : `du` ≠ `df` | être prévenu avant la saturation | serverfault 275206 ; dev.to/higgs182092 disk-is-almost-full |
| Restaurer | lance la restauration | cinq mécanismes ont failli ensemble ; sauvé par un snapshot manuel pris par chance | savoir laquelle a été réellement testée | about.gitlab.com/blog/gitlab-dot-com-database-incident |
| Machine compromise | voit un pic sortant, l'hébergeur coupe | il panique, ou ne fait rien | quels fichiers ont changé, et depuis quand | serverfault 218005 |
| Pare-feu | applique une règle | plus aucune connexion, enfermé dehors | une simulation avant application | dev.to/mosesmorris/oops-i-locked-myself-out-with-ufw |
| Certificat qui refuse | relance le renouvellement | rate-limit Let's Encrypt, bloqué plusieurs jours | tentatives récentes, budget restant, heure de déblocage | traefik#11912 |
| La machine de pilotage tombe | rien de prévu | services « no server available », ne se relancent pas malgré `restart` | reconstruire par un script idempotent | coolify/discussions/7160 ; coolify/discussions/2074 |

### Quand ça grossit

| Moment | Ce qu'il fait | Ce qui casse | Ce qu'il attend | Source |
|---|---|---|---|---|
| Passer de 4 à 12 machines | `tmux` pour taper la même commande partout | ne marche que si elles sont identiques, et une seule fois | un état attendu, vérifié | lowendtalk 168411 |
| Appliquer sur N machines | lance sur tout | 3 injoignables, 2 bloquées à mi-chemin ; relancer reproduit la panne | ne rejouer que ce qui a échoué | deploy-goad#2 ; ansible#76915 |
| Rejouer un mois plus tard | relance le même playbook | casse : les dépendances de la cible ont changé | vérifier l'état avant d'agir | lowendtalk 158866 (akhfa) |
| Ajouter de l'observation | pose Netdata ou Uptime Kuma | ~20 % de RAM ou crash à 512 Mo ; OOM à ~50 moniteurs sur 2 Go | que ça tienne sur une petite machine | netdata#9289 ; uptime-kuma#235 ; uptime-kuma#5884 |
| Séparer pilotage et applicatif | tout sur une machine | une panne matérielle emporte tout | au moins deux machines | massivegrid.com/blog/coolify-multi-server-setup |
| Ajouter une machine | ajoute un deuxième serveur au panneau | Traefik casse, ACME répond 404, l'application du premier tombe | ajouter ne casse pas l'existant | coolify#2791 ; coolify#7384 |
| Voir tout le monde | SSH + `htop` + `df` sur chaque machine | le panneau ne montre que la machine principale | une vue unique CPU / RAM / disque | dokploy#4420 |
| Joindre les machines | tunnels SSH inversés, un port par machine sur un VPS relais | CGNAT après changement de FAI | un accès natif sans bricolage | HN 33680000 ; ssh-reverse-tunnel |
| Gérer les clés | recopie les clés publiques serveur par serveur | « pas tenable » même à moins de 20 utilisateurs ; une seule clé privée partout = « huge security threat » | une clé par machine, gérée par l'outil | HN 34325358 ; serverfault 824180 |

## 3. Ce qui fait fuir un outil

La liste consolidée des sept rapports. **openCloud ne doit jamais faire ça.**

| Ce qui fait fuir | Source |
|---|---|
| Un terminal web intégré | lowendtalk 203473 |
| Une bannière « passer en payant » dans un outil cru libre | portainer#8452 |
| Une politique de sécurité imposée sans option (mot de passe fort forcé sur réseau de confiance) | portainer#6904 |
| Des secrets d'environnement en clair dans la base de l'outil | dokploy#3821 |
| Une mise à jour auto qui réécrit une config sans le signaler | lowendtalk 158866 |
| Renommer quelque chose et casser en silence une config écrite à la main | caprover#660 |
| Partager le serveur = donner root complet | coolify#5685 |
| Prendre le contrôle d'un serveur déjà géré, sans cohabiter | coolify#10656 |
| « Zero-downtime » qui ne l'est pas : 18-31 s de 502 | coolify#8627 |
| Un message d'erreur vague qui cache la vraie cause | coolify#5750 |
| Un retour arrière « réussi » dans l'interface, échoué en dessous | dokploy#3987 |
| Une config illisible passé quelques dizaines de services (labels éclatés) | budgethomelab |
| Un jeton unique trop large : un seul point de fuite | lego#984 |
| Retenter à l'infini sans prévenir | caprover#727 |
| Confondre RAID et sauvegarde | serverfault, tag backup (131 votes) |
| Croire un journal de succès sans restauration vérifiée — « Amateurs backup. Professionals restore. » | HN 42365438 |
| Une option destructive (`--force`, `down -v`) sans conséquence dite | docker/compose#9535 ; reponotes.com |
| Un stockage distant opaque, sans visibilité sur ses pannes | restic 4555 |
| Une base locale fragile seule responsable de la fiabilité | forum.duplicati.com |
| Un disque froid débranché tenu pour une garantie éternelle | serverfault |
| Exposer ou monter le socket Docker sans proxy d'authentification | dockge#849 ; quarkslab |
| Publier un port sur `0.0.0.0` sans dire que ça contourne le pare-feu | chaifeng/ufw-docker ; jguillaumesio.com |
| Faire confiance à un bannissement automatique sans vérification humaine | crowdsec#4363 |
| Un outil qui pèse plus que ce qu'il surveille | netdata#9289 ; Tom's Hardware 3453919 |
| Un agent qui ment : CPU à 100 % sur une machine idle, état figé après redémarrage | coolify#11111 ; coolify#11539 |
| Un heartbeat vert avec la liaison morte | portainer#8689 |
| Un statut de machine ambigu : Online, Degraded, Offline, erreur SSH indiscernables | dokploy#4686 |
| Un agent qu'on ne peut pas désactiver et qui redémarre seul | coolify#6050 |
| Un seul agent hors ligne qui éteint toute l'interface | portainer#4911 ; portainer/agent#154 |
| Une seule clé privée déployée sur toutes les machines | serverfault 824180 |

## 4. La confrontation, décision par décision

### Périmètre et principes

| Décision | Verdict | Preuve (source) | Ce que ça change |
|---|---|---|---|
| Une seule infrastructure, openCloud vit à l'intérieur | **confirmé** | Cloudron : un seul serveur, pas de clustering, une instance par organisation (forum.cloudron.io/topic/6540) | Rien |
| Les cinq principes en plus souple | **confirmé** | Rien en silence : maj qui réécrit une config (lowendtalk 158866), retour arrière faussement réussi (dokploy#3987). La machine fait foi : IP « bannies » sans chaîne iptables (docker-fail2ban#25), « la conf firewall est une déclaration, pas un fait » (jguillaumesio.com) | Rien |
| Il n'est pas indispensable | **confirmé** | Après reboot, les apps redémarrent seules par `restart: always`, indépendamment de l'interface (coolify/discussions/2074) ; retour aux scripts + watchdog à 12 machines (HN 48746086) | Rien |
| Il n'est pas propriétaire : impératif, pas de réconciliation | **à compléter** | La dérive dans le temps est documentée : playbook qui casse un mois plus tard (lowendtalk 158866, akhfa), conflits de librairies à chaque upgrade (desperand) | Ne pas réconcilier n'est tenable que si openCloud **détecte** la dérive. Aujourd'hui « Diagnostiquer » est manuel (`15`) — le rendre périodique et comparer au dernier état connu |
| L'origine ne compte pas ; il n'adopte pas ce qui ne suit pas la norme | **à compléter** | La demande n°1 du freelance est justement de reprendre un serveur existant, pour quitter Plesk (coolify#399) ; installer sur un serveur déjà géré crée des conflits 80/443, iptables, double renouvellement LE (coolify#10656) | Le refus tient, mais il doit **nommer ce qu'il a trouvé** (gestionnaire présent, certbot déjà actif), pas juste échouer |
| Ordre : publier, certifier, sauvegarder, observer — applications après | **contredit** | Ce qui manque le plus au terrain est une alerte : sauvegarde en panne qui n'alerte personne (forum.cloudron.io/topic/5615), expiration silencieuse (coolify#4280), déploiement en échec muet (caprover#727) | Arbitrage à rendre : remonter **trois alertes** (certificat en échec, sauvegarde en échec, disque qui monte) au rang de « certifier », avant les applications |
| Licence permissive | **confirmé** | Preuve indirecte seulement : bannière « passer en payant » dans un outil cru libre = ce qui fait fuir (portainer#8452) | Rien |

### Technique et exécution

| Décision | Verdict | Preuve (source) | Ce que ça change |
|---|---|---|---|
| Go, HTML + HTMX, un binaire dans `/opt`, pas de conteneur | **confirmé** | Dockge monte `/var/run/docker.sock` dans son conteneur par défaut : une faille = root sur l'hôte (dockge#849) ; 23 354 sockets Docker visibles sur Shodan (quarkslab) | Rien. HTMX : pas de preuve terrain |
| Développement en local, tests dans la CI GitHub | — | **pas de preuve terrain** (hors du champ des rapports) | Rien |
| Architecture en composants sous `internal/` | — | **pas de preuve terrain** | Rien |
| Code en anglais, opérateur en français ; refus ≠ erreur ; écriture atomique | **confirmé** (partiellement) | Écriture atomique : coupure pendant le backup → index corrompu, archives perdues (mrtomlinux.org) ; format blob sans redondance → dépôt verrouillé (kopia#4305). Refus nommé : message vague qui cache la cause (coolify#5750) | Rien. La langue : pas de preuve |
| Ordre du dev : squelette → action → SSH → publier → certifier → services → sauvegardes → observer | **contredit** | Même preuve que l'ordre de marche ci-dessus (cloudron 5615, coolify#4280, caprover#727) | Si l'ordre de marche bouge, celui-ci bouge avec |
| Code repris de `your-cloud` | — | **pas de preuve terrain** | Rien |
| Debian et Ubuntu | **confirmé** | Tout le terrain sécurité y est : digitalocean initial-server-setup-with-ubuntu, danieltenner Hetzner, bugs.launchpad.net/bugs/1887655 | Rien |
| Scripts shell versionnés par SSH ; Ansible si trop de répétition | **à compléter** | L'idempotence est la vraie raison de quitter les scripts : « les scripts shell relancés dupliquent des lignes de config » (ansible-lint discussions/5017) ; « Ansible vérifie l'état d'abord » (matxsu). Mais Ansible n'immunise pas : un lot en échec arrête tout le playbook (ansible#76915) | L'étape 3 « Comparer » de `15` doit devenir une **propriété testée** : chaque `run.sh` rejoué deux fois sort `inchangé`, vérifié en CI |
| Utilisateur openCloud en `sudo` complet sans mot de passe | **contredit** | « sudoers en commandes listées : recommandé partout, appliqué rarement ; compte d'automatisation compromis = root » (cloudqubes.com/blog/7-bad-practices-of-sudo) ; débat nommé (geerlingguy/ansible-for-devops/discussions/431) | Arbitrage : le vecteur de lancement est **déjà fixe** (`15` §1) — une règle `sudo` limitée à `systemd-run --unit=oc-action-*` est écrivable dès le début, pour presque rien |
| Pas d'agent maison | **confirmé, à compléter** | Les agents sont eux-mêmes une source de panne : agents qui refusent de se reconnecter (portainer#2076, #4279), perdus après 24 h sur Pi (portainer/agent#154), agent qui ment (coolify#11111, #11539), heartbeat vert et liaison morte (portainer#8689) ; le créateur de Netdata reconnaît la surface d'attaque des agents (HN 37767747). **Mais le sans-agent a ses griefs** : pas de vue « live » fiable, statut ambigu (dokploy#4686) ; « shell over SSH, l'agent le plus fragile jamais déployé » (HN 15161292) ; les clés à gérer (HN 45397861) | Le choix tient, il ne règle pas tout : **l'état d'une machine doit distinguer** joignable / SSH en échec / action en cours / dernière remontée datée — et « Tester l'accès » doit tourner périodiquement, en silence |
| Une file par machine, en série ; portée infrastructure décomposée en lot | **confirmé** | Le mode de défaillance le plus documenté est l'exécution à moitié appliquée : 3 machines injoignables, 2 bloquées (deploy-goad#2) ; un lot en échec arrête tout le reste (ansible#76915) ; B échoue, C n'est jamais joué, flotte à moitié à jour (ansible#7019) | C'est la décision la mieux soutenue du registre. Une exigence d'affichage : le lot dit, par machine, **réussi / échoué / non tenté** |
| L'action ne vit pas dans la connexion : `systemd-run`, journald, écriture au fil de l'eau | **confirmé** | « relancer reproduit la même panne » sur un déploiement à moitié appliqué (deploy-goad#2) ; services qui ne se relancent pas après instabilité du contrôleur (coolify/discussions/7160) | Rien |
| Journal de transaction par action, relu au démarrage | **confirmé** | La première minute d'un incident demande la chronologie et qui a déclenché — « ran it on db1 instead of db2 » (GitLab) ; traçabilité de qui a changé quoi (cloudaware.com/blog/devsecops-compliance) | Rien |
| Machine openCloud sur elle-même : même script, transport local | — | **pas de preuve terrain** | Rien |
| Portée, lieu, réversibilité, interruption, montrés avant | **à compléter** | Le terrain nomme le **dry-run**, pas les attributs : « sans dry-run ni vérification d'état = casser quelque chose » (lowendtalk 217522, sanam08) ; simulation avant application d'une règle pare-feu (dev.to UFW) ; validation format avant redémarrage (serverfault 1103004) | Montrer aussi le **diff du fichier** qui va être posé et le résultat de `make config` / `sshd -t` / `visudo -c`, pas seulement quatre étiquettes |
| Aucune valeur saisie ne passe par une ligne de commande | **confirmé** (sans preuve directe) | **Pas de preuve terrain d'une injection.** Preuve voisine : secrets d'environnement en clair dans la base de l'outil, rejeté (dokploy#3821) | Rien |
| Ce qui survit à l'extinction tourne sur la machine, par minuteries systemd | **confirmé** | Les apps redémarrent seules après reboot, indépendamment de l'interface (coolify/discussions/2074) ; une box en panne de sauvegarde n'alerte personne — c'est le cron muet (forum.cloudron.io/topic/5615) | Rien |
| Machine openCloud mutualisée ; composants centraux (CrowdSec, métriques, journaux) dessus | **contredit** | « Coolify + apps sur la même machine = une panne matérielle emporte tout → séparer » (massivegrid) ; Netdata ~20 % de RAM ou crash à 512 Mo (netdata#9289, community.netdata.cloud), 170 à 500 Mo par petite machine (community.netdata.cloud 3342) ; Uptime Kuma sature à 1-2 Go (uptime-kuma#235, #5884) ; quand le point de supervision unique tombe, toute visibilité disparaît (uptime-kuma#5599) | Arbitrage : soit un **plancher de ressources nommé et vérifié au préflight** pour la machine openCloud, soit les composants centraux ailleurs par défaut |

### Machines, fichiers, services

| Décision | Verdict | Preuve (source) | Ce que ça change |
|---|---|---|---|
| La norme `/srv` prime toujours, aucune racine variable, aucun repli | **confirmé** | Un dossier par service, `.env` local jamais commité, `Makefile` par service — le schéma qui tient (github.com/EnigmaCurry/d.rymcg.tech) ; attente explicite : « schéma disque imposé » (06-deploiement) | Rien |
| Rien d'openCloud sous `/srv` ; `/var/lib/opencloud` sauvegardé par règle nommée | **à compléter** | Le piège est documenté : AWX écrit son backup **sur la machine de contrôle elle-même** (docs.redhat.com) ; la « backup » de Coolify sauvegarde Coolify, pas les données des apps (HN 43555996) ; rien à restaurer après panne (dokploy#709) | La règle nommée doit écrire **sur une autre machine**, et l'interface doit dire où et quand. C'est écrit dans `10` mais absent de la décision |
| L'environnement est dans le chemin `/srv/workspace/<env>/<service>/` | **confirmé** (faiblement) | La lisibilité à la main est confirmée (d.rymcg.tech). **Pas de preuve terrain sur `<env>` lui-même** | Rien |
| Un service = un environnement, par défaut | — | **pas de preuve terrain** | Rien |
| Deux environnements partagent Traefik, chacun son réseau et son utilisateur | **confirmé** (partiellement) | L'isolation par utilisateur système et pool par site est la solution standard (dchost.com/blog/en/one-server-many-phps) | L'isolation sur le disque ne répond pas à la demande de cloisonnement **dans l'interface** (coolify#5685) — voir §5 |
| Dossier de service = `compose` + `Makefile` ; `down` et `clean` ne détruisent jamais ; `update` approuvé | **confirmé** | `down -v` ou le mauvais dossier → base vide après un « reset » (docker/compose#9535) ; « jamais `-v` par réflexe » (reponotes.com) ; `Makefile` par service (d.rymcg.tech) ; `config` avant `up` répond à caprover#660 | Rien. Décision très bien soutenue |
| Deux formes de service : conteneur, et classique avec plusieurs PHP-FPM | **confirmé** | Un utilisateur + un pool PHP-FPM + un socket par site, dépôt Sury (dchost.com) | Les pièges nommés sont à traiter dans le script : socket `0660` mal aligné, `php -v` ≠ version FPM, OPcache partagé par version |
| Déclaration par formulaire ; mode expert `compose` fourni | **confirmé** | Labels Traefik éclatés dans chaque `compose` : une typo fait disparaître la route sans erreur (budgethomelab) ; un outil qui ne comprend pas ce qu'il déploie ne le sauvegarde pas (HN 43555996) | Rien |
| Une base de données est un service comme un autre | **à compléter** | Deux sauvegardes de bases différentes d'un même Postgres partagent le préfixe : la rétention de l'une efface l'autre (dokploy#4914) | Le slot de sauvegarde doit porter le **nom réel de la base**, pas celui du service |
| Un `.env` par service, sous `data/` | **à compléter** | `.env` poussé sur GitHub → exploité en minutes (dev.to/kashafabdullah) ; 3 938 secrets dans des dépôts, 768 encore actifs (1password/GitGuardian) | Ajouter le geste que le terrain impose : **`.gitignore` forcé** sur tout `.env` généré, quand le dossier de service est un dépôt git |
| La suppression d'un service n'emporte jamais ses données | **confirmé** | Options destructives sans conséquence dite = ce que les gens refusent (docker/compose#9535 ; reponotes.com) | Rien |
| Journaux des services dans `journald`, rien sous `/srv` | **à compléter** | Les logs remplissent `/var` et c'est découvert tard (dev.to disk-is-almost-full) ; fichiers supprimés tenus ouverts, `du` ≠ `df` (serverfault 275206) | Le seuil des 10 % doit couvrir **`/var` autant que `/srv`** — journald y déplace la saturation |
| HTTP seulement : un port brut est hors périmètre | **contredit** | Attente nommée : « exposer un port TCP simplement » (community.traefik.io/t/tcp-routing-to-database/2289) ; pas de SNI en clair → un port par base ; passthrough TLS incompatible Postgres/MSSQL, timeout sans log (traefik#11891) | Incohérence interne : `06` prévoit « un flux nommé, ouvert par une action », `11` dit « hors périmètre ». Arbitrage à rendre : l'un ou l'autre |
| Sur une machine déjà utilisée : ses répertoires et rien de plus ; 80/443 pris = échec explicite | **à compléter** | Conflits 80/443, iptables et **double renouvellement Let's Encrypt** avec Plesk/cPanel (coolify#10656) | Le refus doit détecter et nommer le gestionnaire présent et un certbot déjà actif, pas seulement le processus qui tient le port |

### Réseau, TLS, enrôlement

| Décision | Verdict | Preuve (source) | Ce que ça change |
|---|---|---|---|
| Traefik sur chaque machine ; pas de frontal | **confirmé** | Un frontal unique est un point de fuite : une mauvaise config nginx sur le central a bloqué le renouvellement de toutes les machines (community.letsencrypt.org 104484) ; Traefik pour l'auto-découverte Docker (budgethomelab) | À vérifier après redémarrage : le proxy qui ne repart pas fait tomber tous les sites (coolify#7193) |
| DNS Cloudflare par API, ACME DNS-01, **un seul jeton en une seule copie** | **contredit** | « Un jeton unique couvrant tout = rayon de fuite large » ; attente : « scoper par domaine, pas un jeton fourre-tout » (lego#984 ; acme.sh#2398) | Arbitrage : un **jeton par zone Cloudflare**, tous sur la machine openCloud. Le raisonnement de `06` (« un jeton par machine n'apporte rien ») reste vrai — le terrain parle de zones, pas de machines |
| Le TLS termine sur chaque machine | **confirmé** | Le contre-modèle est documenté : un seul client ACME émet puis distribue, clé privée pré-distribuée et fixe (community.letsencrypt.org 85495) ; le central bloqué bloque tout (104484) | La question ouverte « comment le certificat arrive » est exactement ce piège : le déposer depuis la machine openCloud reproduit 85495 |
| Enrôlement par commande générée ; option « derrière NAT » qui monte WireGuard | **confirmé, à compléter** | Rejouer un script idempotent est le remède retenu (coolify/discussions/2074) ; une règle appliquée sans vérifier l'accès courant enferme dehors (dev.to UFW). Le CGNAT est le déclencheur réel de l'option NAT (HN 33680000 ; jeffgeerling.com ; donaldsimpson.co.uk). Une clé par machine et `known_hosts` strict sont confirmés (serverfault 824180 ; HN 34322927). **Mais** : ajouter un deuxième serveur a cassé le proxy et l'ACME du premier chez Coolify (coolify#2791, #7384) | Garder `sshd -t` avant `reload` et les trois essais. Ajouter : *Enrôler* **ne touche pas** au proxy des autres machines et **vérifie après coup** que leurs hôtes virtuels répondent encore. Le changement d'adresse ou d'empreinte reste une action nommée (bitlaunch.io) |
| WireGuard, pas IPsec | — | **pas de preuve terrain** sur ce choix. Le terrain ne raconte pas « monter soi-même un mesh WireGuard » : il adopte du packagé — Tailscale (HN 33680000, 47066866), Nebula stable des années sans entretien (nebula#1119) | L'option « derrière NAT » doit être **aussi indolore que Tailscale** : une commande, zéro réglage, et ça tient sans entretien |
| La machine sans adresse publique est un cas de premier rang | **confirmé** | Trois récits identiques : changement de FAI ou 4G → CGNAT → tunnel inversé ou Tailscale (HN 33680000 ; jeffgeerling.com 2022 ; donaldsimpson.co.uk 2016) ; deux d'entre eux à exactement deux machines | Rien |
| Journaux centralisés (Loki + Alloy) pour le trafic par site ; le WAF lit en local | **à compléter** | « Monter un Graylog pour une douzaine de lignes par jour est disproportionné » (HN 23937193) ; Elasticsearch inadapté (HN 26321270) ; « ce qui compte c'est la colle » (HN 48265359) | L'incohérence interne relevée ici a été corrigée : `02`, `09` et `11` disent que l'agent CrowdSec lit en local. Loki ne sert qu'au trafic par site et peut attendre les alertes |
| CrowdSec pour le WAF | **à compléter** | ~45-50 % des alertes d'une règle = trafic légitime banni (crowdsec#4363) ; IP « bannies » des semaines alors que la chaîne iptables avait disparu (docker-fail2ban#25) | Deux ajouts nommés par le terrain : une **vérification périodique que le bouncer bloque vraiment**, et une liste des bannissements avec un geste « débannir » |
| Netdata ou Beszel + Loki/Alloy | **contredit** | Netdata : crash RAM à 512 Mo (netdata#9289), ~20 % de RAM au réglage minimal, le staff conseille de streamer ailleurs (community.netdata.cloud). Beszel n'apparaît qu'une fois, du bon côté, chez le témoin à 12 machines (HN 48746086) | La question ouverte du registre a une réponse de terrain : **Beszel**. Et poser un plancher de RAM avant d'installer quoi que ce soit |

### Sécurité, sauvegardes, mises à jour

| Décision | Verdict | Preuve (source) | Ce que ça change |
|---|---|---|---|
| Interface jamais exposée sans authentification ; un opérateur ; mot de passe changé à la première connexion | **à compléter** | 2FA réclamée dans trois outils (nginx-proxy-manager#313 ; caprover#493) ; accès cantonné à un projet pour un client (coolify#5685) ; mais une politique imposée sans option fait fuir (portainer#6904) | Prévoir la 2FA dans le modèle de données dès maintenant ; et ne pas **imposer** une politique de mot de passe sans porte de sortie sur réseau de confiance |
| Secrets d'openCloud en fichiers à permissions strictes, jamais réaffichés | **confirmé** | Secrets en clair dans la base de l'outil = ce qui fait fuir (dokploy#3821) ; 768 secrets encore actifs dans des dépôts publics (1password/GitGuardian) | Ajouter la **rotation du jeton DNS** comme action, puisqu'il ne sera jamais relu (lego#984) |
| Sauvegarde locale + copie froide manuelle sur disque chiffré ; date affichée | **à compléter** | OVH SBG2 : trois copies « isolées » sur 0,5 % des serveurs, dans le bâtiment détruit, 6 % irrécupérables (developpez 313910) ; GitLab : cinq mécanismes ont failli ensemble ; le seul montage qui tient ajoute une **restauration de test automatique 1 h après** (britter.dev) | Le cadrage nomme déjà le trou (plusieurs mois entre copies froides). Le terrain dit que la **copie croisée entre machines** cesse d'être une option, et que le test de restauration doit être une action, pas un principe |
| Clé de chiffrement dans un gestionnaire de mots de passe, jamais stockée par openCloud | **à compléter** | Fichier clé supprimé → dépôt illisible à jamais ; mot de passe oublié (forum.restic.net 1798, 2990 ; restic#923) | La décision tient. Ajouter une **vérification périodique que la clé déchiffre encore** : sinon la perte se découvre à la restauration |
| Élagage : 10 % d'espace libre ; notification système | **à compléter** | À 6 Mo restants, « plus aucune opération possible, même le verrou » — un outil bloqué à 100 % ne peut plus s'auto-purger (forum.restic.net disk full) ; « l'espace n'est pas libéré avant `borg compact` » (borgbackup prune.html) | Trois précisions : élaguer doit **libérer réellement** ; mesurer avec `df`, pas `du` (serverfault 275206) ; le plancher de sauvegardes, écrit dans `07`, doit figurer au registre |
| Mises à jour de sécurité automatiques, redémarrage proposé ; services jamais seuls ; openCloud jamais par la file | **à compléter** | `unattended-upgrades` redémarre malgré `Automatic-Reboot=false` — bug confirmé, non résolu depuis 2020 (bugs.launchpad.net/bugs/1887655) ; une maj auto a réécrit une config et cassé le DNS en prod (lowendtalk 158866) ; `reboot-required` rarement vérifié (dominikhofer.me) | « Redémarrage proposé » n'est pas garanti par l'outil : le vérifier et signaler tout redémarrage non demandé. Et lister les fichiers de config en conflit (`.dpkg-dist`, `.ucf-dist`) après chaque maj |

## 5. Ce que le terrain demande et que le cadrage n'a pas

| Besoin | Fréquence | Source | Où il irait | Effort |
|---|---|---|---|---|
| **Alerte quand une action ou un déploiement échoue** | 4 rapports | caprover#727 ; cloudron 5615 ; coolify#4280 ; nginx-proxy-manager#3979 | `09`, et le catalogue `15` | faible |
| **Test de restauration, comme action** | 3 rapports | britter.dev ; duplicati 11978 ; GitLab ; HN 42365438 | `15` (absent) — `07` n'en fait qu'un principe | moyen |
| **Prévenu avant que le disque sature**, pas quand l'élagage part | 3 rapports | dev.to disk-is-almost-full ; serverfault 275206 ; coolify#5685 | `07` et `09` | faible |
| **Vérification DNS + CAA avant la demande de certificat** | 2 rapports | coolify#5750 ; community.letsencrypt.org 228082 ; traefik#10684 | préflight de `15` §6 | faible |
| **Budget de tentatives Let's Encrypt visible** (tentatives, reste, déblocage) | 2 rapports | traefik#11912 ; letsencrypt.org/docs/rate-limits ; certbot#1569 | page certificats de `06` | faible |
| **Vérifier le pare-feu depuis l'extérieur** | 2 rapports | jguillaumesio.com ; chaifeng/ufw-docker | `08`, action « Diagnostiquer » | faible |
| **Retour à la version précédente d'un déploiement** (retag du digest d'avant) | 3 rapports | dokploy#319 ; dokploy#3987 ; kkovacs.eu | `15`, action « Mettre à jour » | moyen |
| **Créer ou éditer l'enregistrement DNS sans quitter l'outil** | 2 rapports | dokploy#4376 ; coolify#679 ; coolify#5750 | `06` — aujourd'hui « le DNS est constaté, jamais touché » | faible |
| **Un seul écran qui montre et qui agit** | 2 rapports | HN 47648922 ; Tom's Hardware 3453919 | `09` — l'observation est déléguée à deux outils tiers, donc deux interfaces de plus | moyen |
| **Accès cantonné à un projet pour un client ou un collègue** | 2 rapports | coolify#5685 ; coolify#399 | écarté par `01` — besoin nommé, décision assumée | fort |
| **Déplacer un service vers une autre machine** | 1 rapport | dokploy#812 | absent partout | fort |
| **Alerte sur l'absence de signal** — une tâche périodique qui cesse de se manifester | 2 rapports | blog.elest.io ; forum.cloudron.io 5615 | `09`, `07` — la notification « copie froide trop ancienne » en est un cas ; à généraliser | faible |
| **Le lot dit qui a réussi, échoué, n'a pas été tenté**, par machine | 2 rapports | ansible#7019 ; ansible#76915 | `05`, interface du lot | faible |
| **Un statut de machine honnête** : joignable / SSH en échec / action en cours / dernière remontée | 3 rapports | dokploy#4686 ; portainer#8689 ; coolify#11539 | `02`, `09` | faible |
| **Ajouter une machine sans casser le proxy et les certificats existants** | 1 rapport | coolify#2791 ; coolify#7384 | `15`, action *Enrôler* | faible |
| **Une deuxième source de vérité quand la machine openCloud tombe** | 1 rapport | uptime-kuma#5599 | `10` — la reconstruction par réinstallation ne couvre pas la visibilité pendant la panne | moyen |

## 6. Ce que le terrain n'a pas pu dire

- **Reddit inaccessible** dans les onze rapports ; **ServerFault** bloqué au
  fetch pour `04`, atteint via navigateur pour `03` ; **Stack Exchange**,
  forums DigitalOcean et Hetzner bloqués (`01`, `06`) ; **HN** inaccessible
  pour `02d`, **LowEndTalk** pour `02c`.
- **Aucun témoignage francophone.** Les rapports sont anglophones.
- **Les comparables ne sont pas openCloud** : Coolify, Dokploy, CapRover,
  Portainer, Cloudron sont des PaaS conteneurisés. Rien sur un binaire Go sans
  agent — les preuves « ce qui fait fuir » viennent d'une famille voisine.
- **Sujets sans aucune preuve** : HTMX et le rendu côté serveur ; la langue du
  code ; l'architecture interne ; WireGuard contre IPsec ; « un service = un
  environnement » ; l'environnement dans le chemin ; l'injection ; la licence.
- **La copie froide sur disque physique transporté n'a aucune preuve** : le
  terrain parle de dépôt distant chiffré, jamais de disque débranché.
- **Beszel n'a qu'une mention**, positive (HN 48746086). Ce n'est pas assez
  pour trancher seul contre Netdata, seulement pour peser.
- **Rien sur « monter soi-même un mesh WireGuard »** : les gens adoptent
  Tailscale ou Nebula plutôt que de le vivre.

## 7. Les cinq choses à changer avant de coder

1. **Remonter trois alertes** — certificat en échec, sauvegarde en échec,
   disque qui monte — au rang de « certifier », avant les applications
   (§4, ordre de marche ; §5).
2. **Décider où tournent les composants centraux, et poser un plancher de
   ressources** vérifié au préflight : Netdata + Loki + CrowdSec sur une
   machine mutualisée de 1-2 Go ne tient pas (§4, machine mutualisée ; §4,
   Netdata ou Beszel).
3. **Restreindre la règle `sudo` au vecteur de lancement** — il est déjà fixe,
   la liste blanche coûte une ligne (§4, `sudo` complet).
4. **Mettre le test de restauration au catalogue** : aujourd'hui c'est un
   principe sans action, et c'est le seul montage de sauvegarde qui tient au
   terrain (§4, sauvegarde froide ; §5).
5. **Un jeton Cloudflare par zone, et un préflight DNS + CAA + budget de
   tentatives** avant toute demande de certificat (§4, jeton unique ; §5).
