-- Les secrets saisis dans l'interface sont chiffrés en base : le mot de passe
-- du relais (settings.mail_password), l'URL et le secret des canaux
-- (alert_channels.url, .secret) et le jeton de ping (heartbeats.token).
-- Les colonnes gardent leur nom. Le chiffrement ne peut pas se faire en SQL :
-- il a lieu au démarrage, dans store.SealPlaintextSecrets, qui reconnaît un
-- clair à l'absence de la forme d'un chiffré et le remplace sur place.
--
-- Le jeton de ping est cherché à chaque ping, et un chiffré change à chaque
-- écriture : on cherche donc par son empreinte SHA-256, posée à côté. NULL
-- tant que le démarrage n'a pas chiffré la ligne ; l'index ignore alors la
-- ligne, et l'unicité du jeton reste portée par la colonne d'origine.
ALTER TABLE heartbeats ADD COLUMN token_hash TEXT;

CREATE UNIQUE INDEX heartbeats_token_hash ON heartbeats (token_hash) WHERE token_hash IS NOT NULL;
