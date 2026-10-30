/**
 * Hands a downloaded file to the browser as a save. The request carried the
 * session token, so a plain link to the endpoint would not do; the blob is
 * saved through a temporary object URL instead.
 */
export function saveBlob({ blob, filename }, fallbackName) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename || fallbackName;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
