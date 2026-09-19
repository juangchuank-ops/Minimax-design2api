// Decrypt MiniMax Design (hilo) v2enc values using .token-key
// usage: node decrypt-v2enc.js <tokenKeyFile> <v2encString>
const fs = require('fs');
const crypto = require('crypto');

const [keyFile, ...rest] = process.argv.slice(2);
const stored = rest.join(' ').trim();
const key = fs.readFileSync(keyFile);
if (key.length !== 32) {
  console.error('bad key length: ' + key.length);
  process.exit(1);
}
const PREFIX = 'v2enc:';
if (!stored.startsWith(PREFIX)) {
  console.error('not a v2enc value');
  process.exit(1);
}
const parts = stored.slice(PREFIX.length).split(':');
if (parts.length !== 3) {
  console.error('bad payload parts: ' + parts.length);
  process.exit(1);
}
const [ivB64, tagB64, ctB64] = parts;
const d = crypto.createDecipheriv('aes-256-gcm', key, Buffer.from(ivB64, 'base64'));
d.setAuthTag(Buffer.from(tagB64, 'base64'));
const out = Buffer.concat([d.update(Buffer.from(ctB64, 'base64')), d.final()]);
process.stdout.write(out.toString('utf8'));
