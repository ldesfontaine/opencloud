// Package auth tient l'accès de l'opérateur à l'interface : le mode
// (aucun, HTTP Basic, un SSO en amont qui authentifie), l'identifiant,
// l'empreinte lente du mot de passe, le nom de l'en-tête d'identité et la
// mémoire des derniers identifiants vérifiés.
// Il ne sait rien de HTTP : le serveur lit l'en-tête, répond 401 et limite.
package auth
