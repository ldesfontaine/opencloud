package web

import "time"

// L'horloge du serveur, remplaçable dans les tests pour figer « vu il y a ».
func (s *Server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}
