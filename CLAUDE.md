# CLAUDE.md

本文件为在此仓库中工作的 Claude Code（claude.ai/code）提供指引。

## 项目概述

localshare 通过 SSH 反向隧道把用户的本地 HTTP 服务暴露到公网。客户端执行：

```bash
ssh -R /:localhost:80 -p 1022 <server_name>
```

然后会得到一个公网入口 URL，例如 `https://<server_name>/p2p/<peer_id>`（WebRTC P2P 引导页）或 `https://<server_name>/<peer_id>`（纯 SSH 转发兜底），其中 `peer_id` 是 `sha256(username)[:16]`——同一 SSH 账号永远是同一 URL。

本仓库**只有服务端**。"ap" 对端代理（负责把 DataChannel 流量转发到用户本地服务的客户端）不在此仓库中；协议中浏览器端的那一半是 `templates/bootstrap.html`（P2P 引导页）和 `templates/admin.html`（管理后台页面），由 `main.py` 在启动时加载。

## 开发规定

- **严禁使用 `pip` 安装或管理 Python 依赖，一切 Python 环境操作必须使用 `uv`**（`uv pip install ...`、`uv sync`、`uv run` 等）。环境里没有配置好的 uv 时，先用 `uv venv` 创建虚拟环境、用 `uv pip install -r requirements.txt` 安装依赖，而不是退回 pip。
- **代码必须通过类型检查**：全项目使用严格的类型标注，`pyright`（strict 模式，配置见 `pyrightconfig.json`）必须零报错。外部库 `asyncssh`/`aiohttp` 没有官方类型 stub，由 `_typeshed/` 下的手写 stub 提供类型（`python.analysis.stubPath` 指向它）。新增代码必须带完整类型标注，禁止用 `# type: ignore` 掩盖真实类型问题。
- **注释/文档字符串用中文**，遵循 PEP 8（注释）与 PEP 257（docstring）风格：docstring 用于模块、类、函数，说明"做什么"；行内注释只解释"为什么"（非显然处）。

## 常用命令

```bash
uv pip install -r requirements.txt       # 依赖：asyncssh、aiohttp、Jinja2（必须用 uv，禁止 pip）
uv run python main.py --help             # 用 uv 运行 Python 脚本
uv run --with pyright pyright            # 类型检查（strict，读 pyrightconfig.json，须零报错）

# 构建镜像（打包 nginx + main.py）
docker build -t localshare .

# 运行；/config 存放 ssh_host_key、可选的 cert.pem/cert.key、admin_password.sha256
docker run -v ~/localshare_config:/config \
  -e APP_SERVER_NAME=app.pywebio.online \
  --restart=always --name localshare \
  -p 1022:1022 -p 80:80 -p 443:443 -d localshare

# 客户端侧隧道冒烟测试
ssh -R /:localhost:8080 -p 1022 app.pywebio.online
```

**没有测试套件**——`tests/` 里只有一个过期的 `__pycache__` 残留。预期的部署方式是 Docker 镜像；仅运行 `main.py` 无法承载隧道流量，因为 main.py 创建的 unix socket 依赖 nginx 做路由（见架构）。`docker/start.sh` 是容器入口，要求设置 `APP_SERVER_NAME`。

## 架构

### 进程布局

- **`main.py`**（约 700 行的模块，不含模板）在同一个 asyncio 事件循环里跑两个服务：
  - **SSH 服务**（asyncssh，监听 `:1022`）。客户端的远程转发请求（`-R /:localhost:80`）会被 `MySSHServer.new_sock_path()` 重写为 `<socket-dir>/<peer_id>.sock` 下的 unix socket；随后 SSH 会话在 `handle_client` 里阻塞，打印入口 URL 并等待客户端断开。
  - **信令/HTTP 服务**（aiohttp，监听 `127.0.0.1:8080`），提供 `/signal`（WebRTC 信令 WebSocket）、`/p2p/{peer_id}`（引导页）和 `/admin*`（管理后台）。
- **模板**：`templates/bootstrap.html`（P2P 引导页，含占位符 `__PEER_ID__`、`__ICE_SERVERS__`）和 `templates/admin.html`（管理后台）。main.py 启动时用 `load_template()` 按文件名读取（路径基于 `__file__`，与 `--config-dir` 无关）。
- **nginx**（容器内）对外提供 `:80`/`:443`。其配置由 `docker/nginx_conf_gen.py` 根据 Jinja2 模板 `docker/nginx.conf.tpl` 生成。路由规则：
  - `/signal`、`/p2p/`、`/admin` → 代理到 aiohttp 服务（`127.0.0.1:8080`）
  - `/{peer_id}(/...)?` 与 `/ws/{peer_id}` → 代理到 `unix:<socket-dir>/<peer_id>.sock`（即 SSH 隧道 → 用户本地服务）。会写入 `localshare_sock` cookie，使直连 `/ws/` 的请求能找到正确的 socket。
  - `/static/`、`/pywebio_static/` → 本地静态文件
  - 其他一切 → 302 重定向

### 通往用户服务的两条并行传输路径

1. **SSH 反向隧道**（兜底）：nginx → unix socket → SSH → 用户 localhost。
2. **WebRTC P2P**（优先）：浏览器加载 `/p2p/<peer_id>`，建立 `/signal` WebSocket，与客户端 ap 代理协商 DataChannel，然后注入的 shim 透明地把 `fetch`/`XMLHttpRequest`/`WebSocket`/`EventSource` 代理到该通道上。如果 10 秒内 DataChannel 未打通，页面会重定向到 SSH 转发 URL。

P2P 协议有**两层**，改动时必须保持两者同步：

- **信令 WebSocket**（浏览器 ↔ 服务端，JSON）：`register`、`browser`、`offer`、`answer`、`candidate`、`viewer_state`、`ping`/`pong`，以及回发的 `registered`/`peer_join`/`browser_registered`/`error`。对端以 `ap` 角色注册，浏览器以 `browser` 角色注册。
- **DataChannel**（浏览器 ↔ ap 代理，携带 base64 分块的 JSON 消息）：`http.request` / `http.response.start|chunk|end|error`、`ws.open|opened|message(.start|chunk|end)|closed|error`、`sse.open|chunk|closed`。分块消息用 `message_id` + `total`/`index` 方案（12 KiB 每块）。

### 服务端状态与配置

- 几乎所有状态都是**模块级可变全局变量**：`signal_peers`、`signal_viewers`、`active_ssh_connections`、`active_ssh_peers`、`admin_sessions`、`peer_stats`，以及 `metrics` 计数 dict。新增/删除子系统意味着要同时改动这些全局变量和对应的 handler。
- **流量监控**：SSH 隧道字节由 `ssh.py` 的 `TunnelTrafficSession`/`wrap_session_factory` 统计（入站走 `data_received`，出站对内层对象 `write` 打补丁），写入 `metrics['ssh_tunnel_bytes_in/out']` 与 `peer_stat` 的 `bytes_in/out`；客户端 IP 在 `connection_made` 用 `get_extra_info('peername')` 采集，存入 `peer_stat['ip']`。`signaling.py` 的 `traffic_sampler` 任务每 5 秒对四项累计字节差分，追加到 `state.traffic_history`（`deque(maxlen=1440)`，2 小时窗口）。
- **IP 属地**：`geoip/` 包封装 ip2region 离线库（`geoip/vendor/` 是 vendored 的官方解析库，`ip2region.xdb` 11MB），`geoip.locate(ip)` 返回 `(country, province)` 并做 LRU 缓存。数据文件缺失时静默降级为 `(None, None)`，不阻断服务。vendor 目录在 pyrightconfig 的 `exclude` 里（`# pyright: basic`），封装层也是 basic 级。
- **管理后台静态资源**：`static/echarts.min.js`、`static/world.json`（世界地图）、`static/china.json`（中国地图）由 nginx `/static/` alias 提供（生产）；本地 `uv run` 时 aiohttp 在 `start_signal_server` 里 `add_static('/static', ...)` 提供。admin.html 用 ECharts 渲染流量曲线与地图（世界按国家、中国按省聚合在线实例）。
- 管理后台页面在 `templates/admin.html`——密码以 SHA-256 形式存于 `<config-dir>/admin_password.sha256`（可用 `ADMIN_PASSWORD_FILE` 覆盖路径），会话 cookie 为 `localshare_admin`（HttpOnly、SameSite=Strict，`--https` 下 `secure`）。
- 环境变量：`MAX_SSH_CONNECTIONS`、`MAX_SIGNAL_CONNECTIONS`、`MAX_SIGNAL_VIEWERS_PER_PEER`、`NOFILE_LIMIT`、`STUN_SERVERS` / `TURN_SERVERS` / `TURN_USERNAME` / `TURN_PASSWORD`（逗号或 JSON 列表）、`ADMIN_PASSWORD_FILE`、`HTTPS=true`。
- `main.py` 的 CLI 参数：`--port`、`--config-dir`、`--socket-dir`、`--signal-host`、`--signal-port`、`--https`，位置参数 `server_name`。`https` 模式会切换 `public_scheme()`/`websocket_scheme()` 的 `http(s)`/`ws(s)`——所有内容（URL、cookie、信令 URL）都由此推导，所以打开它是全局生效的。

### 配置目录（`/config`，通过 `-v` 挂载）

首次运行自动生成 `ssh_host_key`。可选的 `cert.pem` + `cert.key` 用于在 nginx 上启用 HTTPS。可选的 `admin_password.sha256` 用于启用管理后台。

## 注意事项

- **单体强耦合**：P2P 协议契约横跨 `templates/bootstrap.html`（引导页 JS）、`signal_handler`/数据面 handler 和 `docker/nginx.conf.tpl`。改一处几乎必然要联动改其他几处；模板文件是纯 HTML/JS，没有构建步骤，也没有语法检查。
- SSH 服务用的是**假/匿名认证**（`password_auth_supported`、`begin_auth` 返回占位值）——访问控制靠能力而非真实凭据。用户名决定了该对端的公网 URL。
- `handle_client` 故意不主动退出，一直等到客户端断开；同一用户名的新 SSH 会话会关闭旧的连接。
- 存在两套 CI：`.drone.yml`（Drone CI，部署到 `app.pywebio.online`）和 `.github/workflows/build.yml`（GitHub Actions，推送到 Docker Hub `hajiming/localshare`）。Git 远程：`origin` → GitHub，`codeup` → 阿里云。
