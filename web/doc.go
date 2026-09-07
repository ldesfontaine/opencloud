// Package web embarque les gabarits et les fichiers statiques de l'interface.
// Le code qui les sert est dans internal/web ; ce package ne contient que des
// fichiers, parce qu'un embed ne remonte pas au-dessus de son dossier.
//
// Fichiers tiers, épinglés :
//
//	static/htmx.min.js — htmx 4.0.0, tiré de l'archive htmx-4.0.0-dist.zip de
//	la release GitHub bigskysoftware/htmx du 28 août 2026 ; 36 716 octets,
//	SHA-256 e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f.
//	Mise à jour : remplacer le fichier et cette ligne, dans le même commit.
//
//	static/fonts/manrope-latin.woff2 — Manrope de Mikhail Sharanda, graisses
//	400 à 800, sous-ensemble latin variable, tiré de Google Fonts le
//	7 septembre 2026 ; SIL Open Font License 1.1.
//
//	static/fonts/jetbrains-mono-latin.woff2 — JetBrains Mono de JetBrains,
//	graisses 400 à 600, sous-ensemble latin variable, tiré de Google Fonts le
//	7 septembre 2026 ; SIL Open Font License 1.1.
//
//	Les deux polices portent leur licence et leur origine dans
//	static/fonts/LICENSE.md. Rien ne vient d'Internet à l'exécution : la CSP
//	est default-src 'self'.
package web
