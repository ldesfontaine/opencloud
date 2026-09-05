# openCloud — le cadrage

**openCloud pilote une infrastructure, une seule, et il est installé à
l'intérieur de celle-ci**, sur l'une de ses machines. Ce n'est pas un outil
externe qui se connecte à des infrastructures depuis l'extérieur.

## En cinq minutes

- **Une installation = une infrastructure.** Deux infrastructures, ce sont deux
  installations indépendantes, sans vue commune.
- **Il fait d'abord quatre choses**, dans cet ordre : publier un hôte virtuel,
  certifier le domaine, sauvegarder, observer. **Le déploiement d'applications
  vient après.**
- **Il n'est pas propriétaire des machines.** Ce qui a été fait à la main reste
  fait à la main : il l'observe, il ne le reprend pas, il ne le réconcilie pas.
  La machine fait foi.
- **Aucun agent maison sur les machines** : un accès SSH dédié, et des outils
  standard (Traefik, CrowdSec, collecteur, expéditeur de journaux) déployés
  comme n'importe quel service. Rien à mettre à jour sur les machines quand
  openCloud évolue.
- **La machine openCloud déclenche, observe et rend compte** — elle n'est pas
  dans le chemin critique. Éteinte, les sites continuent, les sauvegardes
  locales tournent.
- **Une file d'actions par machine**, traitée en série ; les machines avancent
  donc en parallèle et deux actions ne se marchent jamais dessus.
- **Rien ne s'applique en silence.** Chaque action montre ce qu'elle va
  exécuter avant, et ce qu'elle a produit après.

Un mot de vocabulaire : **la machine openCloud** désigne la machine de
l'infrastructure où openCloud est déployé. C'est un **rôle**, pas un type de
machine — elle peut héberger des services comme les autres. Le reste du
vocabulaire est dans `03-modele.md`.

## Les documents

| Document | Ce qu'on y trouve | À lire si… |
|---|---|---|
| `01-perimetre.md` | Ce qu'openCloud est, ce qu'il n'est pas, ce qu'il fait d'abord | vous arrivez sur le projet |
| `02-roles.md` | **Qui est responsable de quoi** : la machine openCloud, les machines gérées, l'opérateur, les outils tiers — et ce qui continue quand la machine openCloud est éteinte | vous vous demandez « qui fait ça ? » |
| `03-modele.md` | Infrastructure, environnement, machine, service, domaine, et les règles qui les lient | vous manipulez les objets du produit |
| `04-implantation.md` | **La norme d'implantation des machines** — `/srv`, `workspace`/`data`, `/opt`. Elle vaut avec ou sans openCloud | vous préparez une machine |
| `05-execution.md` | Files par machine, exécuteurs, catalogue d'actions, tâches périodiques locales | vous écrivez une action |
| `06-reseau-et-certificats.md` | Joignabilité, accès à l'interface, proxy, DNS, TLS, jeton Cloudflare | vous branchez un domaine |
| `07-donnees-et-sauvegardes.md` | Ce qui est sauvegardé, où, comment, et l'élagage | vous parlez de données |
| `08-securite-et-secrets.md` | Authentification, secrets en jeu, durcissement des machines | vous touchez à un secret |
| `09-observation-et-interface.md` | Santé, journaux, alertes, et ce que l'interface montre | vous dessinez une vue |
| `10-cycle-de-vie.md` | État, reprise après perte, mises à jour, installation, amorçage | vous pensez au long terme |
| `11-decisions.md` | **Le registre** : acquis, à trancher par ordre d'impact, écarté et reporté | vous cherchez où en est une décision |
| `12-methode.md` | Comment le travail démarre concrètement | vous voulez commencer |
| `13-pile-technique.md` | Ce que le produit exige d'une pile, les options, la recommandation | vous vous demandez en quoi c'est écrit |
| `14-heritage-your-cloud.md` | Ce qu'on garde, simplifie, transpose ou jette de la V1 | vous connaissez your-cloud |
| `15-catalogue-actions.md` | **Le catalogue** : chaque action, ses paramètres, ses refus — et le modèle d'exécution sans injection | vous écrivez un script |
| `16-architecture.md` | **Les composants du binaire**, leurs frontières, le flux d'une action, la suite logique du dev | vous ouvrez un package |
| `17-conventions-code.md` | Dépôt, langue, erreurs, exécution, web, tests, CI | vous écrivez du code |
| `18-reprise-code-your-cloud.md` | Fichier par fichier, ce qu'on copie, adapte ou porte de la V1, dans l'ordre des étapes | vous commencez une étape |
| `19-cas-d-usage.md` | **Le terrain contre le registre** : personnes, moments, ce qui fait fuir, et chaque décision confirmée, à compléter ou contredite | vous doutez d'une décision |
| `annexes/lecture-syswarden.md` | Ce que le code de SysWarden apprend : journal de transaction, écriture atomique, config validée | vous écrivez le runner |
| `annexes/lecture-sudoers.md` | Listes blanches sudoers : ce qui tient, ce qui ne tient pas, pourquoi un lanceur | vous écrivez l'enrôlement |

## Où en est-on

Le cadrage est **fermé** (`11-decisions.md`). Pile : Go, interface rendue côté
serveur, un binaire. Actions : scripts par SSH, `params.env` lu par systemd.
Enrôlement par commande. TLS sur chaque machine. CrowdSec, Netdata ou Beszel,
Loki. Le catalogue d'actions (`15-catalogue-actions.md`) et les cibles du
Makefile d'un service (`03-modele.md`) sont écrits.

**Le cadrage a été confronté au terrain** (`19-cas-d-usage.md`) : huit
décisions contredites, toutes arbitrées ; ce qui reste ouvert est au registre
(`11-decisions.md`).

Le démarrage : dev en local, tests dans la CI GitHub, une fonctionnalité à la
fois (`12-methode.md`). L'architecture (`16`), les conventions (`17`) et le
code à reprendre de your-cloud (`18`) sont écrits. **On peut coder.**

## Tenue de ces documents

- **Chaque fait vit à un seul endroit** ; les autres documents y renvoient par
  nom de fichier.
- **Les questions ouvertes ne sont écrites qu'au registre** (`11-decisions.md`,
  partie 2). Aucun document n'en porte de liste locale.
- **Une décision prise se déplace** : elle quitte la partie 2 du registre pour
  rejoindre les acquis, et le document concerné l'explique.
