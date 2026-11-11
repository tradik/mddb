/**
 * The postcode files a geo reindex loads first, typed one per line as
 * "country, path on the server" (/v1/geo-reindex `loadPostcodes`).
 */

/** "PL, /var/lib/mddb/pl.csv" lines → [{ country, csvPath }]; blank lines skipped. */
export function parsePostcodeLines(text) {
  return text.split('\n').map((line) => line.trim()).filter(Boolean).map((line) => {
    const [country, ...path] = line.split(',');
    return { country: country.trim(), csvPath: path.join(',').trim() };
  });
}
