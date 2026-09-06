// Package selfupdate remplace le binaire en place par une release GitHub
// vérifiée : somme SHA-256, attestation de provenance, pas de saut de version
// mineure, ancien binaire gardé en .prev, remplacement atomique, redémarrage
// de l'unité. Jamais automatique, jamais par la file : c'est l'opérateur qui
// l'appelle (20-installation-et-mise-a-jour.md).
package selfupdate
