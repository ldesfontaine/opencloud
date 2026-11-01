// Package policy est le modèle des échanges qu'un réseau autorise, indépendant
// de tout pare-feu : des zones, des objets (ce qu'une règle désigne) et des
// règles « de → vers : quoi, pourquoi ». Un connecteur traduit ce que son
// pare-feu a dit vers ce modèle ; la grille « qui parle à qui » se lit dans
// ce seul modèle. Lecture seule : rien ici n'écrit dans un pare-feu. L'écriture
// de règles depuis openCloud, plus tard, partira de ces mêmes types.
package policy
