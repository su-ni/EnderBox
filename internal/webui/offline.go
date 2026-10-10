package webui

// offlinePage 是这个二进制「没打包前端」时给用户看的说明页。
//
// 为什么不把这份 HTML 当成文件放进 dist/：
// 那会让 build.sh 每次构建都覆盖它 —— 仓库里天天出现一个「被改脏」的文件，
// 而且还会造出「dist/assets 已经存在、index.html 还是占位页」这种自相矛盾的状态
// （Built() 会据此声称前端已内嵌，实际给出的却是说明页）。
// 内置成字符串之后，它永远不会缺失、不会被构建踩到，git 也就干净了。
const offlinePage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>EnderBox · 这个二进制里没有前端产物</title>
<style>
  :root{
    --bg:#111311; --panel:#1c201d; --line:#3d453d;
    --ink1:#eef1ec; --ink2:#b7c2b4; --ink3:#8b978a;
    --green:#7cc24f; --amber:#f2c14e;
  }
  *{box-sizing:border-box}
  body{
    margin:0; background:var(--bg); color:var(--ink1);
    font:14px/1.6 -apple-system,"Segoe UI",system-ui,"Microsoft YaHei",sans-serif;
    display:flex; justify-content:center; align-items:flex-start; padding:64px 24px;
  }
  main{max-width:620px; width:100%}
  .tag{
    display:inline-block; padding:2px 10px; margin-bottom:18px;
    background:#2b1d10; color:var(--amber); border:1px solid #5a4420;
    font-size:11px; letter-spacing:.08em;
  }
  h1{font-size:22px; font-weight:500; margin:0 0 10px; line-height:1.35}
  p{color:var(--ink2); margin:0 0 14px}
  .muted{color:var(--ink3); font-size:12px}
  ol{color:var(--ink2); padding-left:20px; margin:0 0 20px}
  li{margin-bottom:8px}
  code{
    font-family:ui-monospace,Consolas,"Cascadia Mono",monospace;
    background:var(--panel); border:1px solid var(--line);
    padding:1px 6px; font-size:12.5px; color:var(--ink1);
  }
  pre{
    background:var(--panel); border:1px solid var(--line);
    padding:14px 16px; margin:0 0 20px; overflow-x:auto;
    font-family:ui-monospace,Consolas,"Cascadia Mono",monospace;
    font-size:12.5px; color:var(--ink1);
  }
  .hr{height:1px; background:var(--line); margin:24px 0}
  a{color:var(--green); text-decoration:none; border-bottom:1px solid #3f6a28}
  a:hover{border-bottom-color:var(--green)}
</style>
</head>
<body>
<main>
  <div class="tag">未打包前端</div>
  <h1>这个二进制里没有前端产物</h1>
  <p>
    守护进程本身是好的 —— 接口全都活着，只是界面没被塞进来。
    这是一个「光有后端」的 exe，所以看不到面板。
  </p>

  <div class="hr"></div>

  <p>要出完整版本，在仓库根目录跑构建脚本：</p>
<pre>./build.sh</pre>

  <p class="muted">它会依次做三件事：</p>
  <ol>
    <li>构建前端（<code>tsc -b --force</code> + <code>vite build</code>）</li>
    <li>把 <code>web/dist</code> 复制到 <code>internal/webui/dist</code><br>
        <span class="muted">go:embed 不能跨 <code>..</code>，所以必须先搬进同一个包目录</span></li>
    <li>编译出带界面的 <code>bin/serverbox.exe</code></li>
  </ol>

  <p>
    这一步只是显式提示，不是错误。开发时更常见的做法是前后端分开跑：
    前端 <code>npx vite</code>（5173，会把 <code>/api</code> 代理到 23333），
    后端 <code>go run ./cmd/server</code>。
  </p>

  <div class="hr"></div>

  <p class="muted">
    接口目前是活的，可以直接试：
    <a href="/api/health">/api/health</a> ·
    <a href="/api/instances">/api/instances</a>
  </p>
</main>
</body>
</html>
`
