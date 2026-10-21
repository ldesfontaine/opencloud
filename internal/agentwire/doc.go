// Package agentwire est le protocole entier entre l'agent et le serveur :
// en-têtes de la preuve signée, corps JSON de l'enrôlement, du défi et du
// signal, la liste fermée de ce que le serveur pousse (Kind, Command) et
// l'enveloppe signée d'un ordre. Des types, des constantes et les octets que
// les deux côtés signent ou comparent ; aucune règle. Leurs étiquettes sont
// figées par les agents installés.
package agentwire
