// Minimal asar extractor (no external deps)
// usage: node extract-asar.js <archive.asar> <outDir> [--list]
const fs = require('fs');
const path = require('path');

const src = process.argv[2];
const outDir = process.argv[3];
const listOnly = process.argv.includes('--list');
if (!src || (!outDir && !listOnly)) {
  console.error('usage: node extract-asar.js <archive.asar> <outDir> [--list]');
  process.exit(1);
}

const fd = fs.openSync(src, 'r');
const head = Buffer.alloc(16);
fs.readSync(fd, head, 0, 16, 0);
const jsonLen = head.readUInt32LE(12);
const jb = Buffer.alloc(jsonLen);
fs.readSync(fd, jb, 0, jsonLen, 16);
const tree = JSON.parse(jb.toString('utf8'));
const dataStart = 16 + jsonLen;
fs.closeSync(fd);

const fd2 = fs.openSync(src, 'r');
let count = 0;
let bytes = 0;

function walk(node, prefix) {
  if (!node.files) return;
  for (const [name, entry] of Object.entries(node.files)) {
    const rel = prefix ? prefix + '/' + name : name;
    if (entry.files) {
      if (!listOnly) fs.mkdirSync(path.join(outDir, rel), { recursive: true });
      walk(entry, rel);
    } else {
      count++;
      bytes += entry.size;
      if (listOnly) {
        console.log(String(entry.size).padStart(10), rel);
        continue;
      }
      const abs = path.join(outDir, rel);
      fs.mkdirSync(path.dirname(abs), { recursive: true });
      const buf = Buffer.alloc(entry.size);
      let read = 0;
      while (read < entry.size) {
        read += fs.readSync(fd2, buf, read, entry.size - read, dataStart + Number(entry.offset) + read);
      }
      fs.writeFileSync(abs, buf);
    }
  }
}

walk(tree, '');
fs.closeSync(fd2);
if (!listOnly) console.log(`extracted ${count} files (${(bytes / 1048576).toFixed(1)} MB) -> ${outDir}`);
