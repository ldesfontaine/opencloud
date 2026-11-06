// Package waittest attend une condition dans un test : on avance dès qu'elle
// est vraie, la borne (30 s) ne sert qu'à ne pas bloquer la CI si le code est
// cassé. Seuls des _test.go l'importent.
package waittest
