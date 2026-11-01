package httpx

// maskedPrefixRunes : combien de caractères d'une clé ou d'un identifiant de
// jeton restent lisibles. Assez pour reconnaître laquelle, trop peu pour la
// rejouer.
const maskedPrefixRunes = 4

// MaskCredential ne laisse voir que le début d'une clé ou d'un identifiant
// de jeton : « AbCd… ». Toute réponse qui relit un identifiant d'accès passe
// par là ; le front fait de même pour ce que l'opérateur vient de saisir
// (maskCredential, web/src/lib/credential.ts).
func MaskCredential(value string) string {
	runes := []rune(value)
	if len(runes) <= maskedPrefixRunes {
		return "…"
	}
	return string(runes[:maskedPrefixRunes]) + "…"
}
