// Remplit %s et %d dans l'ordre, et lit %% comme un « % », comme fmt.Sprintf
// côté serveur : les catalogues TOML sont la seule source, écrits en formats
// Go, et le front ne les réécrit pas.
export function format(template: string, args: readonly (string | number)[]): string {
  let index = 0;
  return template.replace(/%[sd%]/g, (verb) => {
    if (verb === "%%") {
      return "%";
    }
    const value = args[index];
    index += 1;
    return value === undefined ? verb : String(value);
  });
}
