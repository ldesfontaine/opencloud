import "./Field.scss";

// Un choix parmi quelques-uns : le segment de la direction artistique. Chaque
// choix peut porter un compte, pour qu'on voie ce qu'un filtre garde avant de
// le prendre.
export function Choice<Value extends string>({
  label,
  name,
  choices,
  value,
  text,
  count,
  onChange,
}: {
  label: string;
  name: string;
  choices: readonly Value[];
  value: Value;
  text: (choice: Value) => string;
  count?: (choice: Value) => number;
  onChange: (choice: Value) => void;
}) {
  return (
    <span className="seg" role="radiogroup" aria-label={label}>
      {choices.map((choice) => (
        <label key={choice}>
          <input type="radio" name={name} checked={value === choice} onChange={() => onChange(choice)} />
          <span>
            {text(choice)}
            {count !== undefined && <b className="seg-count">{count(choice)}</b>}
          </span>
        </label>
      ))}
    </span>
  );
}
