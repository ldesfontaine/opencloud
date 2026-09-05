# Cycle de vie — état, reprise, mises à jour, installation

## L'état et sa reprise

Une seule instance d'openCloud, donc **une seule source de vérité**, sur la
machine openCloud, sous `/var/lib/opencloud/`. **Ni consensus, ni réplication, ni
quorum.**

Quatre choses rendent ce choix tenable :

1. **Les services ne dépendent pas d'openCloud pour tourner** (voir
   `02-roles.md`).
2. **L'état est reconstructible.** Ce qu'openCloud sait des machines, il doit
   pouvoir le réapprendre en les réinterrogeant. Seules les données qu'il est
   seul à détenir — identités, secrets, historique — exigent une vraie
   sauvegarde.
3. **La sauvegarde de cet état part sur une autre machine** de l'infrastructure,
   dans son `data/backups/`. **L'interface dit sur laquelle et à quelle date**
   (`07-donnees-et-sauvegardes.md`).
4. **La perte de la machine openCloud se répare par réinstallation ailleurs**,
   puis ré-enrôlement des machines.

> Cette procédure de reprise est **à écrire et à éprouver**. Elle remplace la
> haute disponibilité, qui est écartée.

## Les mises à jour — trois sujets distincts

| | Quoi | Décidé |
|---|---|---|
| **1** | **openCloud lui-même** | Jamais par la file d'actions. Forme à fixer au dev — le modèle de mise à jour signée de SysWarden est noté (`annexes/lecture-syswarden.md`). |
| **2** | **Les services déployés** | **Jamais seuls.** Redéployer est une action ; revenir en arrière ne ramène pas les données. |
| **3** | **Le système des machines** | **Sécurité en automatique** (`unattended-upgrades`). Si un redémarrage est requis, l'interface le signale et l'opérateur clique. |

Deux gestes que l'outil ne garantit pas tout seul, et qu'openCloud reprend à
son compte :

- **Tout redémarrage non demandé est signalé.** `unattended-upgrades` redémarre
  parfois malgré `Automatic-Reboot=false` : openCloud lit la durée de
  fonctionnement, constate le redémarrage, et le dit.
- **Après chaque mise à jour, les fichiers de configuration en conflit sont
  listés** — `.dpkg-dist`, `.ucf-dist`. Le paquet les pose sans prévenir ;
  openCloud les nomme, il ne les fusionne pas (`15-catalogue-actions.md`).

## L'installation d'openCloud

- **Un binaire dans `/opt`, une unité systemd durcie.** Pas de conteneur : il
  serait isolé de tout ce qu'openCloud doit toucher — `/srv`, systemd, le
  Traefik de l'hôte.
- Sur **Debian ou Ubuntu**.
- La machine openCloud est **enrôlée comme les autres**, avec transport local
  (`05-execution.md`).
- Réinstallation ailleurs après perte : restaurer `/var/lib/opencloud`, vérifier
  que la nouvelle machine joint chacune, retirer l'ancienne.

## L'amorçage

**Une commande, générée par l'interface, jouée sur la machine.** Elle crée
l'utilisateur openCloud, pose sa clé et sa règle `sudo`, affiche l'empreinte de
la machine que l'opérateur saisit dans l'interface. Aucun mot de passe ne
traverse le navigateur.

Avec l'option « derrière NAT », elle crée aussi la clé WireGuard et monte le
lien vers la machine openCloud au démarrage. Un seul geste, y compris pour une
machine sans adresse publique.

> **L'exigence est nommée : aussi indolore que Tailscale.** Une commande, zéro
> réglage à saisir, et le lien tient sans entretien. Si l'option demande plus
> que ça, elle est ratée.

**Enrôler ne touche ni au proxy ni aux certificats des autres machines**, et
**vérifie après coup** que leurs hôtes virtuels répondent encore. Ajouter une
machine ne casse pas celles qui tournent (`15-catalogue-actions.md`).

