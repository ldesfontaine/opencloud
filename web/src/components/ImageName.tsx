import "./ImageName.scss";

// Le nom d'une image de conteneur : sur une ligne, jamais coupé au milieu
// d'un mot ; un nom très long finit par « … » et l'infobulle donne la
// valeur entière (audit F-1).
export function ImageName({ image }: { image: string }) {
  return (
    <span className="image-name" title={image}>
      {image}
    </span>
  );
}
