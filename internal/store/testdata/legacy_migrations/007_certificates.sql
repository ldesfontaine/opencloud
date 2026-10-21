-- Les certificats : la chaîne que la sonde voyait déjà, désormais jugée.
-- Rien de neuf à stocker en plus du dernier certificat vu : « renouvelé il
-- y a six jours » se lit sur cert_not_before, et un renouvellement se
-- reconnaît au changement d'empreinte. Pas de table d'historique, pas de
-- chaîne complète : la fiche n'en a pas besoin et le volume grossirait à
-- chaque essai pour une donnée qui ne bouge qu'au renouvellement.

-- Une sonde TCP qui fait une poignée de main plutôt qu'une simple
-- connexion : c'est ce qui donne son certificat à un port chiffré qui ne
-- parle pas HTTP, SMTP ou IMAP. Sans objet pour une sonde HTTP, dont
-- l'URL dit déjà le protocole.
ALTER TABLE probes ADD COLUMN tls INTEGER NOT NULL DEFAULT 0;

-- L'échéance et la confiance sont deux faits, jamais l'un déduit de
-- l'autre : un certificat d'autorité interne a une date parfaitement
-- lisible, et c'est justement le cas où l'opérateur se fait avoir.
ALTER TABLE probes ADD COLUMN cert_chain_valid INTEGER NOT NULL DEFAULT 0;
ALTER TABLE probes ADD COLUMN cert_hostname_match INTEGER NOT NULL DEFAULT 0;

-- Ce que l'agrafe OCSP remise pendant la poignée de main a dit :
-- « good », « revoked », « unknown ». Vide : la cible n'agrafe rien, et
-- openCloud ne contacte aucun répondeur pour le savoir.
ALTER TABLE probes ADD COLUMN cert_ocsp TEXT NOT NULL DEFAULT '';

-- La vue d'ensemble lit l'échéance la plus proche à chaque page.
CREATE INDEX probes_cert_expiry ON probes (cert_not_after) WHERE cert_fingerprint != '';
