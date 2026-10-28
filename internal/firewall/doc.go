// Package firewall lit les pare-feu que l'opérateur déclare, en lecture seule :
// leur version, leurs interfaces, leurs règles, leurs baux DHCP et leurs
// passerelles. Il ne connaît aucune marque : chaque connecteur (sous-paquet)
// parle à une API et rend un relevé indépendant du constructeur. Il ne donne
// aucun ordre au pare-feu, n'en tire aucune alerte et ne garde que le dernier
// relevé réussi.
package firewall
