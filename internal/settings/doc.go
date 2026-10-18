// Package settings décrit les réglages de l'opérateur, ceux qu'il change dans
// l'interface. Ils vivent dans la base, sur une seule ligne (internal/store) ;
// ce paquet ne décide rien : la valeur par défaut d'un réglage vit chez qui
// l'utilise. Il reprend une fois l'ancien settings.toml, puis l'efface.
package settings
