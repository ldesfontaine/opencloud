// Package resource porte les ressources d'une machine : les échantillons
// que l'agent mesure, leur historique en trois étages (brut, horaire,
// journalier), les fenêtres qui les lisent et la valeur courante, dite
// indisponible passé un délai. Il ne parle ni HTTP ni SQL ni /proc : le
// serveur, le store et le sampler le font.
package resource
