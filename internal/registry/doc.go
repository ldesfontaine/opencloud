// Package registry parle aux registres d'images par l'API Registry v2, en
// lecture seule : la liste des tags d'un dépôt et l'empreinte d'un tag.
// Il obtient un jeton anonyme, ou signé des identifiants du trousseau
// Docker de la machine, et sort par la garde egress. Il ne tire jamais
// une image et ne dépend que de la bibliothèque standard.
package registry
