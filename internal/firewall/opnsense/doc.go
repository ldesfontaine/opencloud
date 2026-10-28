// Package opnsense lit l'API REST d'un OPNsense avec une paire clé et secret,
// en lecture seule : tout passe par un GET, et aucun point d'accès qui écrit,
// applique ou supprime n'existe dans ce paquet. Il rend un relevé
// indépendant du constructeur (internal/firewall).
package opnsense
