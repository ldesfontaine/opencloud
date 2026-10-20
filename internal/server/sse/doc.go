// Package sse tient les flux text/event-stream du direct : une connexion par
// visiteur sur un bus, avec ses événements numérotés et son ping. La mécanique
// est celle de l'administration et de la page publique ; chacun garde son bus.
package sse
