// Package the ./build directory into a Chrome Web Store-ready zip.
// Output: mddb-browser-<version>.zip
import { createWriteStream } from 'node:fs';
import { readFile, readdir, stat, mkdir } from 'node:fs/promises';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createDeflateRaw } from 'node:zlib';
import { pipeline } from 'node:stream/promises';
import { Readable } from 'node:stream';

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, '..');
const buildDir = join(root, 'build');

async function walk(dir) {
  const out = [];
  for (const entry of await readdir(dir)) {
    const full = join(dir, entry);
    const st = await stat(full);
    if (st.isDirectory()) {
      out.push(...(await walk(full)));
    } else if (st.isFile()) {
      out.push(full);
    }
  }
  return out;
}

function crc32(buf) {
  let c;
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    c = n;
    for (let k = 0; k < 8; k++) {
      c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    }
    table[n] = c >>> 0;
  }
  let crc = 0xffffffff;
  for (let i = 0; i < buf.length; i++) {
    crc = (crc >>> 8) ^ table[(crc ^ buf[i]) & 0xff];
  }
  return (crc ^ 0xffffffff) >>> 0;
}

async function deflate(buf) {
  const chunks = [];
  await pipeline(Readable.from(buf), createDeflateRaw(), async (source) => {
    for await (const c of source) chunks.push(c);
  });
  return Buffer.concat(chunks);
}

async function zipDir(srcDir, outPath) {
  const files = await walk(srcDir);
  const centralDir = [];
  let offset = 0;
  const out = createWriteStream(outPath);

  for (const file of files.sort((a, b) => a.localeCompare(b))) {
    const raw = await readFile(file);
    const name = relative(srcDir, file).replaceAll('\\', '/');
    const nameBuf = Buffer.from(name, 'utf8');
    const compressed = await deflate(raw);
    const crc = crc32(raw);

    const localHeader = Buffer.alloc(30);
    localHeader.writeUInt32LE(0x04034b50, 0);
    localHeader.writeUInt16LE(20, 4); // version needed
    localHeader.writeUInt16LE(0x0800, 6); // utf-8 flag
    localHeader.writeUInt16LE(8, 8); // method: deflate
    localHeader.writeUInt16LE(0, 10); // time
    localHeader.writeUInt16LE(0, 12); // date
    localHeader.writeUInt32LE(crc, 14);
    localHeader.writeUInt32LE(compressed.length, 18);
    localHeader.writeUInt32LE(raw.length, 22);
    localHeader.writeUInt16LE(nameBuf.length, 26);
    localHeader.writeUInt16LE(0, 28);

    out.write(localHeader);
    out.write(nameBuf);
    out.write(compressed);

    const cd = Buffer.alloc(46);
    cd.writeUInt32LE(0x02014b50, 0);
    cd.writeUInt16LE(20, 4);
    cd.writeUInt16LE(20, 6);
    cd.writeUInt16LE(0x0800, 8);
    cd.writeUInt16LE(8, 10);
    cd.writeUInt16LE(0, 12);
    cd.writeUInt16LE(0, 14);
    cd.writeUInt32LE(crc, 16);
    cd.writeUInt32LE(compressed.length, 20);
    cd.writeUInt32LE(raw.length, 24);
    cd.writeUInt16LE(nameBuf.length, 28);
    cd.writeUInt16LE(0, 30);
    cd.writeUInt16LE(0, 32);
    cd.writeUInt16LE(0, 34);
    cd.writeUInt16LE(0, 36);
    cd.writeUInt32LE(0, 38);
    cd.writeUInt32LE(offset, 42);
    centralDir.push({ header: cd, name: nameBuf });

    offset += localHeader.length + nameBuf.length + compressed.length;
  }

  const cdStart = offset;
  let cdSize = 0;
  for (const { header, name } of centralDir) {
    out.write(header);
    out.write(name);
    cdSize += header.length + name.length;
  }

  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(0, 4);
  end.writeUInt16LE(0, 6);
  end.writeUInt16LE(centralDir.length, 8);
  end.writeUInt16LE(centralDir.length, 10);
  end.writeUInt32LE(cdSize, 12);
  end.writeUInt32LE(cdStart, 16);
  end.writeUInt16LE(0, 20);
  out.write(end);
  out.end();
}

async function main() {
  const pkg = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'));
  const distDir = join(root, 'dist');
  await mkdir(distDir, { recursive: true });
  const outPath = join(distDir, `mddb-browser-${pkg.version}.zip`);
  await zipDir(buildDir, outPath);
  console.log(`Wrote ${outPath}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
