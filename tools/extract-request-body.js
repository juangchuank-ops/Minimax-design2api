// 从客户端/网关 bundle 里抠出「某个后端发起云端请求时组装的 body」。
//
// 用法：node extract-request-body.js <bundle.js> <锚点字符串> [前后字符数]
//
// 桌面客户端里每个媒体后端都有一个 Service 类，它的方法里会 return 一个
// 字面量对象作为上游请求体。这个脚本按锚点找到那段并打印出来。
const fs = require("fs");

const [, , file, anchor, spanArg] = process.argv;
if (!file || !anchor) {
  console.error("用法: node extract-request-body.js <bundle.js> <anchor> [span]");
  process.exit(2);
}
const span = Number(spanArg || 1400);
const src = fs.readFileSync(file, "utf8");

let from = 0;
let hits = 0;
while (hits < 12) {
  const i = src.indexOf(anchor, from);
  if (i < 0) break;
  from = i + anchor.length;
  hits++;
  const start = Math.max(0, i - 200);
  const seg = src.slice(start, i + span);
  console.log(`=== hit ${hits} @${i} ===`);
  console.log(seg.replace(/[ \t]+/g, " ").trim());
  console.log();
}
if (hits === 0) console.log(`### 未找到锚点: ${anchor}`);
