# Le service témoin

Le plus petit service sous la norme qui répond en HTTP. Il sert à éprouver la
publication d'un nom, rien d'autre.

Le poser à la main, sur une machine où *Poser le socle* et *Installer le proxy*
sont passés :

```sh
sudo cp -r examples/temoin /srv/workspace/prod/temoin
sudo make -C /srv/workspace/prod/temoin config   # valide, n'écrit rien
sudo make -C /srv/workspace/prod/temoin up
```

Puis, depuis l'interface, *Créer un hôte virtuel* : `prod`, `temoin`, et le nom
voulu. Le port n'est pas demandé — il est lu dans `compose.yaml`.

**Tout ce qu'openCloud fait se fait aussi à la main** : ces trois commandes sont
exactement ce que l'action *Déployer* appellera.
