# tools/

逆向过程里用的一次性脚本，留档用。主程序（`-import-token`）已经把 1、2 两步的
逻辑用 Go 重写了一遍，跑 2api 不需要这些。

## `extract-asar.js`

解 Electron 的 `app.asar`，无第三方依赖。

```bash
node extract-asar.js "E:/Minimax design/current/resources/app.asar" ../recon/app-asar
node extract-asar.js "E:/Minimax design/current/resources/app.asar" --list   # 只列目录
```

**注意**：asar 的 JSON 目录树直接从 `offset 16` 开始，`offset 12` 存的就是 JSON 长度。
网上按 Electron 文档写「offset 16 是 pickle、前面还有 4 字节长度」的版本会少读 4 字节，
`JSON.parse` 会报 `Unexpected token 'l', "les":{"nod"...`。

## `decrypt-v2enc.js`

解开 MiniMax Design 的 `v2enc:` 密文（AES-256-GCM）。

```bash
node decrypt-v2enc.js "<userData>/.token-key" "v2enc:xxxx:yyyy:zzzz"
```

密文在 `<userData>/hub-config-global.json` 里，`tokens.accessToken` 和
`lkgProviderConfig` 两个字段都是这个格式。

`<userData>` 在 Windows 上是 `%APPDATA%\@hilo\MiniMax Hub Global`，
macOS 上是 `~/Library/Application Support/@hilo/MiniMax Hub Global`。

想批量解，配合 node 一行流：

```bash
node -e '
const fs=require("fs"),cp=require("child_process");
const dir=process.env.APPDATA+"/@hilo/MiniMax Hub Global";
const cfg=JSON.parse(fs.readFileSync(dir+"/hub-config-global.json","utf8"));
console.log(cp.execFileSync("node",["decrypt-v2enc.js",dir+"/.token-key",cfg.tokens.accessToken]).toString());
'
```
