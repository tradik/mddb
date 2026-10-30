// @vitest-environment jsdom
// download: a fetched file is saved through a temporary object URL.
import { test, expect, vi } from 'vitest';
import { saveBlob } from './download.js';

test.each([
  ['the server name', 'bundle.tar.gz', 'bundle.tar.gz'],
  ['the fallback name', null, 'fallback.bin'],
])('saves under %s and cleans up', (_label, filename, expected) => {
  URL.createObjectURL = vi.fn(() => 'blob:x');
  URL.revokeObjectURL = vi.fn();
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function () {
    expect(this.download).toBe(expected);
    expect(this.href).toBe('blob:x');
  });
  saveBlob({ blob: new Blob(['x']), filename }, 'fallback.bin');
  expect(click).toHaveBeenCalledTimes(1);
  expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:x');
  expect(document.querySelector('a')).toBeNull();
});
