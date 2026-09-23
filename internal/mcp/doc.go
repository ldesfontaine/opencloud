// Package mcp tient l'accès d'un agent IA à openCloud : le client OAuth
// unique et son secret, les codes d'autorisation, les jetons d'accès, de
// rafraîchissement et d'API, la vérification d'un Bearer, la purge. Il ne
// sait rien de HTTP ni du protocole MCP lui-même : le serveur monte les
// routes et les outils, ce package dit qui a le droit d'entrer.
package mcp
