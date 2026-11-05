-- La taille de l'ARC ZFS d'une machine à l'instant de la mesure (octets).
--
-- mem_used ne la compte déjà plus (sa part rendue à la demande est déduite) ;
-- cette colonne sert seulement à la montrer à part. NULL : machine sans ZFS,
-- ou agent d'avant ce champ. Le brut seul la porte, les agrégats non.
ALTER TABLE machine_samples ADD COLUMN zfs_cache INTEGER;
