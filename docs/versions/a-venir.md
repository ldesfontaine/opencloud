# À venir

Les changements de la prochaine version, pas encore publiée. Cette page est
renommée `<tag>.md` à la publication, et son entrée passe dans le tableau de
[`README.md`](README.md).

## L'essentiel

| Nouveauté | Ce que ça change | Issue |
| --- | --- | --- |
| Inventaire des sauvegardes | Sur la page Sauvegardes, une section **Inventaire** : ce qui existe à sauvegarder (la base, les VM et LXC de chaque hôte Proxmox déclaré, les volumes Docker des machines), les plans qui le couvrent, les archives vzdump que Proxmox garde déjà (nombre, taille, stockage de la plus récente), et les cibles possibles. Les sources dont rien ne répond viennent en tête. Lecture seule : rien n'est écrit sur les machines | #368 |
| Historique des exécutions | Chaque cible d'un passage de plan laisse une ligne (résultat, raison, taille, destination), 400 par plan au plus. Les compteurs « reçues / en échec » du dernier passage survivent à un redémarrage | #368 |
| Deux lectures pour un jeton d'API | `GET /api/backups/inventory` et `GET /api/backups/runs?plan=&source=&limit=` | #368 |
| Limiter un jeton d'API à des machines | À la création d'un jeton en écriture (Paramètres › Jetons d'API) : « Toutes les machines » ou « Certaines machines », et, si un verbe Proxmox est coché, « Tous les invités » ou des numéros de VM. Un geste qui vise une autre machine ou un autre invité est refusé (`403 api_token.out_of_scope`) et journalisé avec le jeton, la machine et l'invité visés. La portée d'un jeton existant se modifie (opérateur seulement) | #382 |
| Volumes Docker rapportés | L'agent liste les volumes nommés de sa machine et les conteneurs qui les montent (jamais le contenu ni la taille) | #368 |

## À faire en mettant à jour

- Mettez à jour **le serveur d'abord** : les migrations `019` à `022`
  s'appliquent au démarrage.
- Mettez ensuite à jour **les agents** des machines qui portent Docker :
  tant qu'ils sont anciens, l'inventaire n'y voit aucun volume.
- L'inventaire des archives Proxmox se remplit au prochain relevé de chaque
  hôte (toutes les 5 minutes).

## Ce qui change

- `last_targets_ok` et `last_targets_failed` d'un plan ne sont plus en mémoire :
  ils se lisent dans l'historique. Un plan qui n'a pas encore repassé garde 0 et 0.
- La migration `021` ne touche pas aux données existantes ; `019` et `020` non plus.
- La migration `022` laisse tous les jetons d'API existants sur « toutes les machines » :
  rien ne change pour eux tant que vous ne les limitez pas.
- `GET /api/token/verbs` rend un champ de plus, `reach` ; `GET /api/settings/api-tokens` rend
  `machines` (les machines à cocher) et, par jeton, `machines` et `guests`.

## Ce que ça ne fait pas

- La portée d'un jeton ne limite que les **gestes**. Les lectures restent sans portée :
  un jeton limité à une machine lit encore les autres.
- Un numéro d'invité vaut sur chaque hôte Proxmox admis : deux hôtes qui portent le même
  numéro ne se distinguent pas.
- Une tâche sans machine n'est dans aucune liste : un jeton limité aux machines ne peut pas
  la mettre en pause.

- Rien n'est sauvegardé de plus : aucun producteur ne prend encore un volume Docker.
- Une destination d'openCloud et un stockage Proxmox qui sont le même endroit
  physique restent deux lignes : ils ne sont pas rapprochés.
- Une archive vzdump dont le numéro ne correspond à aucun invité du dernier relevé
  n'est rattachée à rien.
