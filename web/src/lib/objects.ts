// Un service, une tâche ou une sonde de même nom peut vivre sur deux
// machines : dans un sélecteur, le nom seul ne les distingue pas.
export function nameWithMachine(object: { name: string; machine_name: string }): string {
  if (object.machine_name === "") {
    return object.name;
  }
  return `${object.name} · ${object.machine_name}`;
}
