Installer, sur Debian ou Ubuntu :

```
gh attestation verify opencloud_<version>_amd64.deb --repo ldesfontaine/opencloud
sha256sum --ignore-missing -c SHA256SUMS
sudo apt install ./opencloud_<version>_amd64.deb
```

Mettre à jour une installation existante : `sudo apt install` du nouveau `.deb`,
ou `sudo opencloud self-update`, qui vérifie lui-même la somme et l'attestation.
