// Package machine porte le modèle Machine d'openCloud : la machine openCloud
// elle-même et les machines distantes, leur enrôlement par jeton à usage
// unique, l'authentification de leur agent par signature Ed25519, et les
// sessions ouvertes. Il ne parle ni HTTP ni SQL : le web et le store le font.
package machine
