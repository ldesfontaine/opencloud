# Registre des décisions

**Confronté au terrain le 5 septembre 2026 (`19-cas-d-usage.md`).** Huit
décisions contredites, toutes arbitrées ; les précisions exigées sont écrites
dans les documents concernés.

Trois listes : **acquis**, **à trancher**, **écarté ou reporté**. Chaque ligne
renvoie au document qui la détaille. Les questions ouvertes ne vivent qu'ici.

## 1. Acquis

### Périmètre et principes

| Décision | Détail |
|---|---|
| openCloud pilote **une seule infrastructure** et vit à l'intérieur | `01-perimetre.md` |
| **Les cinq principes de your-cloud, en plus souple** : rien en silence, la machine fait foi, refus nommé, observer ≠ agir, le pilotage hors du chemin des services | `01-perimetre.md` |
| **Il n'est pas indispensable** : tout ce qu'il fait se fait aussi à la main, au même endroit, de la même façon | `01-perimetre.md` |
| **Il n'est pas propriétaire de la machine** : impératif, pas de réconciliation | `01-perimetre.md` |
| **L'origine ne compte pas** : tout ce qui suit la norme est un service, avec les actions génériques. Il n'adopte pas ce qui ne suit pas la norme | `01-perimetre.md` |
| Ordre de marche : **publier, certifier, sauvegarder, observer** — déploiement d'applications après | `01-perimetre.md` |
| **Détection de dérive** : *Diagnostiquer* tourne périodiquement, son résultat est comparé au dernier état connu, l'écart est **signalé — jamais corrigé** | `01-perimetre.md`, `09`, `15` |
| Licence **Apache 2.0** | `LICENSE` |

### Technique et exécution

| Décision | Détail |
|---|---|
| **Go, interface rendue côté serveur (HTML + HTMX), un seul binaire** dans `/opt`, unité systemd. **Pas de conteneur** | `13-pile-technique.md`, `10-cycle-de-vie.md` |
| **Développement en local, tests dans la CI GitHub** (tests, sécurité, bonnes pratiques) | `12-methode.md` |
| **Installation par paquet `.deb`** : un binaire qui embarque tout, unité durcie, `/etc/opencloud`, `/var/lib/opencloud` préservés par dpkg. Pas de `curl \| sh` | `20-installation-et-mise-a-jour.md` |
| **Mise à jour en place** — `apt install` ou `opencloud self-update` : somme et attestation vérifiées, pas de saut de version mineure, ancien binaire gardé, **sauvegarde de la base avant migration sinon pas de migration**, jamais automatique, jamais par la file | `20-installation-et-mise-a-jour.md` |
| **Désinstallation** : `apt remove` garde l'état, `apt purge` l'enlève ; ni l'un ni l'autre ne touche `/srv`, Traefik, Docker ni les machines gérées — celles-ci se retirent une par une par l'action *Retirer une machine*, avant | `20-installation-et-mise-a-jour.md` |
| **Architecture en composants** sous `internal/`, interfaces + constructeurs, aucune globale ; `web` ne lance rien, `runner` ne compose aucune commande | `16-architecture.md` |
| **Identifiants en anglais, commentaires courts en français, tout ce que voit l'opérateur en français** ; **lisible par un humain avant d'être court** — pas d'astuce, le nommage fait le travail ; refus ≠ erreur ; exécution sans shell ; écriture atomique ; fixtures figées | `17-conventions-code.md` |
| **Ordre du dev** : squelette → une action locale suivie en direct → SSH + enrôlement → publier → certifier → services → sauvegardes → observer | `16-architecture.md` |
| **Code repris de `your-cloud`** fichier par fichier, dans l'ordre des étapes — écriture atomique, quoting `params.env`, vecteur SSH, passage WireGuard, séquences d'enrôlement et de sauvegarde | `18-reprise-code-your-cloud.md` |
| **Debian et Ubuntu** | `03-modele.md` |
| **Scripts shell versionnés, lancés par SSH.** Ansible seulement si le catalogue révèle trop de répétition — et alors il remplace | `05-execution.md` |
| **Utilisateur `opencloud` par machine, jamais root direct ; son `sudo` n'autorise qu'un lanceur root-owned `oc-launch <id>`** qui revalide l'identifiant et lance `systemd-run` sans shell. Un filet contre l'erreur et les escapes, pas un rempart contre une clé volée — dit tel quel | `08-securite-et-secrets.md`, `15` §1, `annexes/lecture-sudoers.md` |
| **Pas d'agent maison** : SSH + outils standard déployés comme des services | `02-roles.md` |
| **Une file par machine**, en série ; action de portée infrastructure décomposée en lot | `05-execution.md` |
| **Le lot dit, par machine, `réussi` / `échoué` / `non tenté`** — le troisième dit ce qu'il reste à rejouer | `05-execution.md`, `16` |
| **L'action ne vit pas dans la connexion** : déposée, `systemd-run`, relue via journald ; écrit au fil de l'eau | `05-execution.md` |
| **Journal de transaction par action** (préparée → appliquée / échouée), relu au démarrage | `05-execution.md` |
| **Machine openCloud sur elle-même : même script, même transport** — SSH vers `localhost`, l'unité reste fermée ; amorçage par `sudo opencloud enroll-local` (tranché le 6 septembre 2026, ticket #20) | `05-execution.md` |
| Toute action porte **portée, lieu, réversibilité, interruption**, montrés avant — **plus le diff des fichiers qui vont être posés et le résultat de la validation à blanc** (`make config`, `sshd -t`, `visudo -c`) | `05-execution.md`, `15` §1 |
| **Idempotence testée** : chaque `run.sh` rejoué deux fois sort `inchangé`, vérifié en CI | `15` §1, `17-conventions-code.md` |
| **Aucune valeur saisie ne passe par une ligne de commande** : `params.env` lu par systemd, fichiers rendus par Go, vecteur de lancement fixe | `15-catalogue-actions.md` |
| **Ce qui doit survivre à l'extinction de la machine openCloud tourne sur la machine**, par minuteries systemd | `02-roles.md` |
| **La machine openCloud est mutualisée** ; **les composants centraux** (CrowdSec, métriques, journaux) y tournent, **sous plancher de ressources vérifié au préflight** — ordre de grandeur 2 Go libres. En dessous : refus d'installer Loki ou le collecteur central, et **proposition d'une autre machine** | `02-roles.md`, `09`, `15` §6 |
| **Statut de machine à quatre états** : joignable · SSH en échec · action en cours · dernière remontée datée. *Tester l'accès* tourne **périodiquement, en silence** | `02-roles.md`, `09`, `15` |

### Machines, fichiers, services

| Décision | Détail |
|---|---|
| **La norme `/srv` (`workspace` / `data`) est celle de toutes les machines**, avec ou sans openCloud. **Elle prime toujours** : aucune racine variable, aucun repli | `04-implantation.md` |
| **Rien d'openCloud sous `/srv`** : son état vit dans `/var/lib/opencloud`, sauvegardé par une règle nommée **qui écrit sur une autre machine** ; l'interface dit **où et quand** | `04-implantation.md`, `07`, `10` |
| **L'environnement est dans le chemin** : `/srv/workspace/<env>/<service>/` | `03-modele.md` |
| **Un service = un environnement**, par défaut, sans interdire l'inverse | `03-modele.md` |
| Deux environnements d'une machine **partagent Traefik**, ont **chacun leur réseau et leur utilisateur** | `03-modele.md` |
| **Dossier de service = `compose` + `Makefile` aux cibles standard** (`up down restart status logs config pull update build shell backup restore clean`) ; openCloud et l'opérateur appellent les mêmes ; `down` et `clean` ne détruisent jamais de données ; `update` est une action approuvée | `03-modele.md` |
| **Deux formes de service** : conteneur, et classique avec plusieurs PHP-FPM | `03-modele.md` |
| **Déclaration par formulaire** ; mode expert « `compose` fourni » avec ce que ça perd dit à l'écran | `03-modele.md` |
| **Une base de données est un service comme un autre**, déployé par openCloud | `03-modele.md` |
| **Un `.env` par service**, sous `data/` ; **`.gitignore` forcé sur tout `.env` généré** quand le dossier de service est un dépôt git | `08-securite-et-secrets.md`, `03` |
| La suppression d'un service **n'emporte jamais ses données** | `03-modele.md` |
| **Journaux des services dans `journald`**, rien sous `/srv` | `04-implantation.md` |
| **HTTP seulement** : un port brut reste hors du périmètre — **question rouverte**, voir partie 2 | `03-modele.md`, `06` |
| Sur une machine déjà utilisée, openCloud **crée ses répertoires et rien de plus** ; port 80/443 pris = échec explicite. **Le refus nomme ce qu'il a trouvé** : gestionnaire présent (Plesk, cPanel), `certbot` actif, ou le processus qui tient le port | `04-implantation.md`, `15` §5 |

### Réseau, TLS, enrôlement

| Décision | Détail |
|---|---|
| **Traefik sur chaque machine** ; pas de frontal tant que la topologie ne l'impose pas | `06-reseau-et-certificats.md` |
| **DNS Cloudflare par API, ACME DNS-01** ; **un jeton par zone Cloudflare**, tous sur la machine openCloud, **une seule copie chacun**. **La rotation d'un jeton est une action** du catalogue | `06-reseau-et-certificats.md`, `08`, `15` |
| **Deux écritures DNS nommées, pas une de plus** : le TXT du challenge, et — **sur demande, jamais automatique, toujours montrée** — l'enregistrement A/CNAME (action *Créer l'enregistrement DNS*). Le reste du DNS est constaté | `06-reseau-et-certificats.md`, `15` |
| **Préflight DNS + CAA + budget Let's Encrypt** avant toute demande et tout renouvellement ; le bouton « renouveler » **refuse pendant la fenêtre de blocage** et dit l'heure de déblocage | `06-reseau-et-certificats.md`, `15` §6 |
| **Le TLS termine sur chaque machine** | `06-reseau-et-certificats.md` |
| **Enrôlement par une commande générée, jouée sur la machine** ; option « derrière NAT » qui monte WireGuard, **aussi indolore que Tailscale** : une commande, zéro réglage. *Enrôler* **ne touche ni au proxy ni aux certificats des autres machines** et **vérifie après coup** que leurs hôtes virtuels répondent | `10-cycle-de-vie.md`, `15` |
| **WireGuard**, pas IPsec | `06-reseau-et-certificats.md` |
| **Journaux centralisés** (Loki + Alloy) pour le trafic par site ; **le WAF lit en local**, il n'en dépend pas | `09-observation-et-interface.md` |
| **CrowdSec** pour le WAF ; **vérification périodique que le bouncer bloque réellement**, et **liste des bannissements avec un geste « débannir »** | `08-securite-et-secrets.md`, `09`, `15` |
| **Netdata ou Beszel + Loki/Alloy** — le choix entre les deux collecteurs reste en partie 2 | `09-observation-et-interface.md` |
| **Un seul écran qui montre et qui agit** : la vue Machine porte santé et actions ensemble ; les outils tiers restent des détails, pas des interfaces obligatoires | `09-observation-et-interface.md` |

### Sécurité, sauvegardes, mises à jour

| Décision | Détail |
|---|---|
| **Interface jamais exposée sans authentification** ; un opérateur, mot de passe changé à la première connexion ; modèle ouvert à plusieurs comptes. **2FA prévue dans le modèle de données** dès maintenant, non codée ; **aucune politique de mot de passe imposée sans porte de sortie** sur réseau de confiance | `08-securite-et-secrets.md` |
| **Secrets d'openCloud en fichiers à permissions strictes**, jamais réaffichés | `08-securite-et-secrets.md` |
| **Sauvegarde locale + copie froide manuelle** sur disque chiffré débranché ; date de la dernière copie affichée | `07-donnees-et-sauvegardes.md` |
| **Clé de chiffrement dans un gestionnaire de mots de passe**, jamais stockée par openCloud ; **vérification périodique qu'elle déchiffre encore**, la clé fournie par l'opérateur et non conservée | `07-donnees-et-sauvegardes.md`, `15` |
| **Tester la restauration est une action du catalogue**, jouée automatiquement après chaque sauvegarde : dossier temporaire, vérification, suppression | `07-donnees-et-sauvegardes.md`, `15` |
| **Le slot de sauvegarde d'une base porte le nom réel de la base**, pas celui du service | `07-donnees-et-sauvegardes.md`, `15` |
| **Élagage : 10 % d'espace libre sur `/var` comme sur `/srv`** ; l'espace est **réellement libéré**, mesuré avec `df` et non `du` ; **une alerte se déclenche avant** le seuil d'élagage | `07-donnees-et-sauvegardes.md` |
| **Plancher de sauvegardes** : un nombre minimum conservé quoi qu'il arrive, qui prime sur le seuil d'espace | `07-donnees-et-sauvegardes.md` |
| **Alerte sur l'absence de signal** : toute tâche périodique — sauvegarde, renouvellement, collecte — qui cesse de se manifester déclenche une notification. La « copie froide trop ancienne » en est un cas | `07-donnees-et-sauvegardes.md`, `09` |
| **Mises à jour de sécurité automatiques, redémarrage proposé** ; services jamais seuls ; openCloud jamais par la file. **Tout redémarrage non demandé est signalé** ; après chaque mise à jour, **les fichiers de configuration en conflit sont listés** (`.dpkg-dist`, `.ucf-dist`) | `10-cycle-de-vie.md`, `15` |
| **Pare-feu vérifié depuis l'extérieur** par *Diagnostiquer*, qui **avertit sur tout port publié en `0.0.0.0`** ; après un redémarrage, il vérifie que **le proxy est reparti** | `08-securite-et-secrets.md`, `15` |

## 2. À trancher

| Question | Détail |
|---|---|
| **Comment le certificat arrive sur la machine** : obtenu par la machine openCloud puis déposé, ou Traefik local en DNS-01 avec jeton copié | `06-reseau-et-certificats.md` |
| **Le catalogue d'actions** est écrit ; à valider | `15-catalogue-actions.md` |
| **Traces d'action sur les machines** (script, sortie, code de retour) : `/var/lib/opencloud/actions/<id>` purgé, ou journald seul — Lucas veut d'abord comprendre l'enjeu | `05-execution.md` |
| **Outils de base à poser sur chaque machine** — `screen`, `ncdu`, `at` cités ; liste à compléter | `15-catalogue-actions.md` |
| **Netdata ou Beszel** — l'un des deux, à essayer en local. **Le terrain penche pour Beszel** (`19-cas-d-usage.md` §4, une mention, positive) ; Lucas veut d'abord le connaître | `09-observation-et-interface.md` |
| **Ports bruts (TCP non HTTP)** — hors du périmètre aujourd'hui ; **question rouverte**, à reprendre **plus tard** | `06-reseau-et-certificats.md` |
| Ce que font les machines quand **l'API CrowdSec est injoignable** | `02-roles.md` |
| **Fenêtre de maintenance** par environnement : utile ou non | `10-cycle-de-vie.md` |
| **Sauvegarde du stockage ACME** : restaurer plutôt que réémettre — à écrire | `06-reseau-et-certificats.md` |
| **Le saut de `0.x` à `1.0.0` dans `self-update`** : accepté aujourd'hui comme « majeure suivante en X.0 », en sautant les mineures entre les deux. Garder, et s'engager à ce que la 1.0 migre depuis la dernière 0.x ; ou retirer. Relevé à la revue de la PR #30 | `20-installation-et-mise-a-jour.md`, `internal/selfupdate/version.go` |

## 3. Écarté et reporté

### Écarté

- Plusieurs infrastructures par installation.
- Adoption de ce qui ne suit pas la norme.
- Réconciliation, remise en conformité, gestion de configuration désirée.
- Réplication d'état, consensus, bascule automatique.
- Répartition de charge, HAProxy, adresse flottante.
- Ports bruts hors HTTP — **question rouverte**, voir partie 2.
- **Action dédiée de retour arrière d'un déploiement** : redéployer une version
  antérieure suffit. L'écran de *Mettre à jour* dit que les données ne
  reviennent pas et propose `backup` avant (`15-catalogue-actions.md`).
- SysWarden comme WAF (lu pour son code seulement : `annexes/lecture-syswarden.md`).
- La méthode de développement et de preuve de `your-cloud`.

### Reporté

| Sujet | Détail |
|---|---|
| **Le flux des requêtes et l'adressage IP** — à comprendre avant de choisir | `06-reseau-et-certificats.md` |
| **Outil de sauvegarde** et cohérence des bases | `07-donnees-et-sauvegardes.md` |
| **Rétention en durée**, sauvegardes comme observation | `07`, `09` |
| **Déploiement d'applications** — le modèle est posé | `03-modele.md` |
| **Trois alertes avant les applications** — remonter certificat en échec, sauvegarde en échec et disque qui monte au rang de « certifier ». **L'ordre de marche reste** ; la proposition est notée | `19-cas-d-usage.md` §7 |
