// Package dockerwatch veille le Docker de la machine où il tourne : il
// sonde la socket, inventorie les conteneurs et les réseaux, suit leurs
// événements, mesure les conteneurs et lit leurs journaux à la demande. Il livre ce qu'il observe à
// un Sink : l'agent l'emporte dans son signal, opencloud serve l'écrit
// directement. Il ne sait rien du réseau ni de la base.
package dockerwatch
