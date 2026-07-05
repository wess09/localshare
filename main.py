import asyncio
import base64
import hashlib
import json
import os
import sys
import time
from os import path
import asyncssh
from asyncssh.listener import create_unix_forward_listener
from aiohttp import web, WSMsgType

config_dir = path.abspath(path.dirname(__file__))
server_name = 'remote.nanoda.work'
sock_dir = None
server_port = 1022
https = False
signal_host = '127.0.0.1'
signal_port = 8080
SOCK_NAME_LENGTH = 16
SIGNAL_PEER_TTL = 120

signal_peers = {}
signal_viewers = {}


BOOTSTRAP_HTML = r"""<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>AzurPilot 远程连接</title>
  <style>
    html, body {
      height: 100%;
      margin: 0;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      color: #d8e1ec;
      background: #111827;
    }
    .stage {
      min-height: 100%;
      display: grid;
      place-items: center;
      padding: 24px;
      box-sizing: border-box;
    }
    .panel {
      width: min(520px, 100%);
      border: 1px solid rgba(255,255,255,.14);
      border-radius: 8px;
      padding: 24px;
      background: rgba(17, 24, 39, .82);
      box-shadow: 0 18px 60px rgba(0,0,0,.35);
    }
    h1 {
      margin: 0 0 12px;
      font-size: 22px;
      font-weight: 700;
      letter-spacing: 0;
    }
    p {
      margin: 8px 0;
      line-height: 1.6;
      color: #aebace;
    }
    .status {
      margin-top: 18px;
      padding: 12px;
      border-radius: 6px;
      background: rgba(59, 130, 246, .14);
      color: #bfdbfe;
      word-break: break-word;
    }
    .fallback {
      color: #fde68a;
    }
  </style>
</head>
<body>
<div class="stage" id="p2p-stage">
  <div class="panel">
    <h1>正在建立 P2P 远程连接</h1>
    <p>将优先尝试浏览器与 AzurPilot 直连，失败后自动切换到中继或 SSH 转发。</p>
    <div class="status" id="p2p-status">初始化连接...</div>
  </div>
</div>
<script>
(() => {
  "use strict";

  const peerId = "__PEER_ID__";
  const fallbackUrl = "__FALLBACK_URL__";
  const signalUrl = "__SIGNAL_URL__";
  const iceServers = __ICE_SERVERS__;
  const setupTimeoutMs = 10000;
  const channelChunkSize = 12 * 1024;
  const statusEl = document.getElementById("p2p-status");

  let dc = null;
  let signal = null;
  let requestSeq = 0;
  let wsSeq = 0;
  const pending = new Map();
  const socketMap = new Map();
  const sourceMap = new Map();
  const wsChunkMap = new Map();
  const enc = new TextEncoder();
  const dec = new TextDecoder();

  function setStatus(text, cls) {
    statusEl.textContent = text;
    statusEl.className = cls ? "status " + cls : "status";
  }

  function fallback(reason) {
    setStatus("P2P 连接不可用，切换到 SSH 转发：" + reason, "fallback");
    setTimeout(() => location.replace(fallbackUrl), 300);
  }

  function sendSignal(payload) {
    if (signal && signal.readyState === WebSocket.OPEN) {
      signal.send(JSON.stringify(payload));
    }
  }

  function bytesToBase64(bytes) {
    let binary = "";
    const chunk = 0x8000;
    for (let i = 0; i < bytes.length; i += chunk) {
      binary += String.fromCharCode.apply(null, bytes.subarray(i, i + chunk));
    }
    return btoa(binary);
  }

  function base64ToBytes(text) {
    const binary = atob(text || "");
    const out = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
    return out;
  }

  function normalizePath(input) {
    const url = new URL(String(input), location.href);
    return url.pathname + url.search;
  }

  function sendChannel(payload) {
    if (!dc || dc.readyState !== "open") {
      throw new Error("P2P 通道尚未打开");
    }
    dc.send(JSON.stringify(payload));
  }

  function nextRequestId(prefix) {
    requestSeq += 1;
    return prefix + "-" + requestSeq.toString(36) + "-" + Date.now().toString(36);
  }

  function sendWsData(id, data) {
    let bytes;
    let binary = false;
    if (data instanceof ArrayBuffer) {
      bytes = new Uint8Array(data);
      binary = true;
    } else if (ArrayBuffer.isView(data)) {
      bytes = new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
      binary = true;
    } else {
      bytes = enc.encode(String(data));
    }
    const total = Math.max(1, Math.ceil(bytes.length / channelChunkSize));
    const messageId = id + "-" + Date.now().toString(36) + "-" + Math.random().toString(36).slice(2);
    sendChannel({type: "ws.send.start", id, message_id: messageId, binary, total});
    for (let index = 0; index < total; index++) {
      const chunk = bytes.subarray(index * channelChunkSize, (index + 1) * channelChunkSize);
      sendChannel({type: "ws.send.chunk", id, message_id: messageId, index, data: bytesToBase64(chunk)});
    }
    sendChannel({type: "ws.send.end", id, message_id: messageId});
  }

  function handleProxyMessage(payload) {
    if (payload.type === "http.response.start") {
      const item = pending.get(payload.id);
      if (!item) return;
      item.status = payload.status || 200;
      item.statusText = payload.status_text || "";
      item.headers = payload.headers || {};
      item.chunks = [];
      return;
    }
    if (payload.type === "http.response.chunk") {
      const item = pending.get(payload.id);
      if (!item) return;
      item.chunks.push(base64ToBytes(payload.data));
      return;
    }
    if (payload.type === "http.response.end") {
      const item = pending.get(payload.id);
      if (!item) return;
      pending.delete(payload.id);
      const response = new Response(new Blob(item.chunks || []), {
        status: item.status || 200,
        statusText: item.statusText || "",
        headers: item.headers || {},
      });
      item.resolve(response);
      return;
    }
    if (payload.type === "http.response.error") {
      const item = pending.get(payload.id);
      if (!item) return;
      pending.delete(payload.id);
      item.reject(new Error(payload.message || "P2P 请求失败"));
      return;
    }
    if (payload.type === "ws.opened") {
      const socket = socketMap.get(payload.id);
      if (socket) socket._open();
      return;
    }
    if (payload.type === "ws.message") {
      const socket = socketMap.get(payload.id);
      if (!socket) return;
      const data = payload.binary ? base64ToBytes(payload.data).buffer : payload.data;
      socket._message(data);
      return;
    }
    if (payload.type === "ws.message.start") {
      wsChunkMap.set(payload.message_id, {
        id: payload.id,
        binary: !!payload.binary,
        chunks: new Array(payload.total || 1),
      });
      return;
    }
    if (payload.type === "ws.message.chunk") {
      const item = wsChunkMap.get(payload.message_id);
      if (!item) return;
      item.chunks[payload.index || 0] = base64ToBytes(payload.data);
      return;
    }
    if (payload.type === "ws.message.end") {
      const item = wsChunkMap.get(payload.message_id);
      if (!item) return;
      wsChunkMap.delete(payload.message_id);
      const socket = socketMap.get(item.id);
      if (!socket) return;
      const size = item.chunks.reduce((n, chunk) => n + (chunk ? chunk.length : 0), 0);
      const merged = new Uint8Array(size);
      let offset = 0;
      for (const chunk of item.chunks) {
        if (!chunk) continue;
        merged.set(chunk, offset);
        offset += chunk.length;
      }
      socket._message(item.binary ? merged.buffer : dec.decode(merged));
      return;
    }
    if (payload.type === "ws.closed") {
      const socket = socketMap.get(payload.id);
      if (socket) socket._close(payload.code || 1000, payload.reason || "");
      return;
    }
    if (payload.type === "ws.error") {
      const socket = socketMap.get(payload.id);
      if (socket) socket._error(payload.message || "WebSocket 代理失败");
      return;
    }
    if (payload.type === "sse.chunk") {
      const source = sourceMap.get(payload.id);
      if (source) source._chunk(payload.data || "");
      return;
    }
    if (payload.type === "sse.closed") {
      const source = sourceMap.get(payload.id);
      if (source) source._close();
      return;
    }
  }

  async function p2pFetch(input, init) {
    const request = new Request(input, init);
    const id = nextRequestId("http");
    const headers = {};
    request.headers.forEach((value, key) => {
      if (!["connection", "host", "upgrade"].includes(key.toLowerCase())) {
        headers[key] = value;
      }
    });
    let body = "";
    if (!["GET", "HEAD"].includes(request.method.toUpperCase())) {
      body = bytesToBase64(new Uint8Array(await request.arrayBuffer()));
    }
    const promise = new Promise((resolve, reject) => {
      pending.set(id, {resolve, reject, status: 200, headers: {}, chunks: []});
    });
    sendChannel({
      type: "http.request",
      id,
      method: request.method,
      path: normalizePath(request.url),
      headers,
      body,
    });
    return promise;
  }

  class P2PWebSocket extends EventTarget {
    constructor(url, protocols) {
      super();
      this.url = String(url);
      this.protocol = Array.isArray(protocols) ? protocols[0] || "" : protocols || "";
      this.readyState = P2PWebSocket.CONNECTING;
      this.binaryType = "blob";
      this.id = "ws-" + (++wsSeq).toString(36) + "-" + Date.now().toString(36);
      socketMap.set(this.id, this);
      queueMicrotask(() => {
        try {
          sendChannel({type: "ws.open", id: this.id, path: normalizePath(this.url), protocol: this.protocol});
        } catch (e) {
          this._error(String(e));
        }
      });
    }
    send(data) {
      if (this.readyState !== P2PWebSocket.OPEN) throw new Error("WebSocket 尚未打开");
      sendWsData(this.id, data);
    }
    close(code, reason) {
      if (this.readyState === P2PWebSocket.CLOSED) return;
      sendChannel({type: "ws.close", id: this.id, code: code || 1000, reason: reason || ""});
      this._close(code || 1000, reason || "");
    }
    _open() {
      this.readyState = P2PWebSocket.OPEN;
      const ev = new Event("open");
      this.dispatchEvent(ev);
      if (this.onopen) this.onopen(ev);
    }
    _message(data) {
      let value = data;
      if (data instanceof ArrayBuffer && this.binaryType === "blob") {
        value = new Blob([data]);
      }
      const ev = new MessageEvent("message", {data: value});
      this.dispatchEvent(ev);
      if (this.onmessage) this.onmessage(ev);
    }
    _error(message) {
      const ev = new Event("error");
      ev.message = message;
      this.dispatchEvent(ev);
      if (this.onerror) this.onerror(ev);
      this._close(1011, message);
    }
    _close(code, reason) {
      this.readyState = P2PWebSocket.CLOSED;
      socketMap.delete(this.id);
      const ev = new CloseEvent("close", {code, reason});
      this.dispatchEvent(ev);
      if (this.onclose) this.onclose(ev);
    }
  }
  P2PWebSocket.CONNECTING = 0;
  P2PWebSocket.OPEN = 1;
  P2PWebSocket.CLOSING = 2;
  P2PWebSocket.CLOSED = 3;

  class P2PEventSource extends EventTarget {
    constructor(url) {
      super();
      this.url = String(url);
      this.readyState = P2PEventSource.CONNECTING;
      this.id = nextRequestId("sse");
      this.buffer = "";
      sourceMap.set(this.id, this);
      queueMicrotask(() => {
        sendChannel({type: "sse.open", id: this.id, path: normalizePath(this.url)});
      });
    }
    close() {
      this.readyState = P2PEventSource.CLOSED;
      sourceMap.delete(this.id);
      sendChannel({type: "sse.close", id: this.id});
    }
    _chunk(text) {
      if (this.readyState === P2PEventSource.CONNECTING) {
        this.readyState = P2PEventSource.OPEN;
        const ev = new Event("open");
        this.dispatchEvent(ev);
        if (this.onopen) this.onopen(ev);
      }
      this.buffer += text;
      let index;
      while ((index = this.buffer.indexOf("\n\n")) >= 0) {
        const raw = this.buffer.slice(0, index);
        this.buffer = this.buffer.slice(index + 2);
        const lines = raw.split(/\r?\n/);
        const data = lines.filter(line => line.startsWith("data:")).map(line => line.slice(5).trimStart()).join("\n");
        if (!data) continue;
        const ev = new MessageEvent("message", {data});
        this.dispatchEvent(ev);
        if (this.onmessage) this.onmessage(ev);
      }
    }
    _close() {
      this.readyState = P2PEventSource.CLOSED;
      sourceMap.delete(this.id);
      const ev = new Event("error");
      this.dispatchEvent(ev);
      if (this.onerror) this.onerror(ev);
    }
  }
  P2PEventSource.CONNECTING = 0;
  P2PEventSource.OPEN = 1;
  P2PEventSource.CLOSED = 2;

  async function detectConnectionType(pc) {
    try {
      const stats = await pc.getStats();
      let selectedPair = null;
      stats.forEach(item => {
        if (item.type === "candidate-pair" && (item.nominated || item.selected) && item.state === "succeeded") {
          selectedPair = item;
        }
      });
      if (!selectedPair) return "direct_p2p";
      const local = stats.get(selectedPair.localCandidateId);
      const remote = stats.get(selectedPair.remoteCandidateId);
      if ((local && local.candidateType === "relay") || (remote && remote.candidateType === "relay")) {
        return "turn_relay";
      }
    } catch (e) {}
    return "direct_p2p";
  }

  function waitIceComplete(pc) {
    if (pc.iceGatheringState === "complete") return Promise.resolve();
    return new Promise(resolve => {
      const timer = setTimeout(resolve, 3500);
      pc.addEventListener("icegatheringstatechange", () => {
        if (pc.iceGatheringState === "complete") {
          clearTimeout(timer);
          resolve();
        }
      });
    });
  }

  async function installP2PPage() {
    window.__apP2PFetch = p2pFetch;
    window.__apP2PWebSocket = P2PWebSocket;
    window.__apP2PEventSource = P2PEventSource;
    const response = await p2pFetch("/");
    const html = await response.text();
    const shim = `<base href="/"><script>
      (() => {
        const p2pFetch = window.__apP2PFetch;
        const P2PWebSocket = window.__apP2PWebSocket;
        const P2PEventSource = window.__apP2PEventSource;
        const originalFetch = window.fetch.bind(window);
        function shouldProxy(input) {
          try {
            const url = new URL(typeof input === "string" ? input : input.url, location.href);
            return url.origin === location.origin && !url.pathname.startsWith("/p2p/") && url.pathname !== "/signal";
          } catch (e) {
            return false;
          }
        }
        window.fetch = (input, init) => shouldProxy(input) ? p2pFetch(input, init) : originalFetch(input, init);
        window.XMLHttpRequest = class {
          constructor() {
            this.headers = {};
            this.readyState = 0;
            this.status = 0;
            this.statusText = "";
            this.responseText = "";
            this.response = null;
            this.onreadystatechange = null;
            this.onload = null;
            this.onerror = null;
          }
          open(method, url, async=true) { this.method = method; this.url = url; this.readyState = 1; this._change(); }
          setRequestHeader(k, v) { this.headers[k] = v; }
          getResponseHeader(k) { return this.responseHeaders ? this.responseHeaders.get(k) : null; }
          getAllResponseHeaders() { return this.responseHeaders ? Array.from(this.responseHeaders.entries()).map(([k,v]) => k + ": " + v).join("\\r\\n") : ""; }
          async send(body) {
            try {
              const resp = await p2pFetch(this.url, {method: this.method, headers: this.headers, body});
              this.status = resp.status;
              this.statusText = resp.statusText;
              this.responseHeaders = resp.headers;
              this.responseText = await resp.text();
              this.response = this.responseText;
              this.readyState = 4;
              this._change();
              if (this.onload) this.onload();
            } catch (e) {
              if (this.onerror) this.onerror(e);
            }
          }
          abort() {}
          _change() { if (this.onreadystatechange) this.onreadystatechange(); }
        };
        window.WebSocket = P2PWebSocket;
        window.EventSource = P2PEventSource;
      })();
    <\/script>`;
    document.open();
    document.write(html.replace(/<head([^>]*)>/i, "<head$1>" + shim));
    document.close();
  }

  async function connectP2P() {
    if (!window.RTCPeerConnection) {
      fallback("浏览器不支持 WebRTC");
      return;
    }

    const viewerId = crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2);
    signal = new WebSocket(signalUrl);
    const pc = new RTCPeerConnection({iceServers});
    dc = pc.createDataChannel("ap-proxy", {ordered: true});
    dc.onmessage = ev => handleProxyMessage(JSON.parse(ev.data));
    dc.onopen = async () => {
      const kind = await detectConnectionType(pc);
      sendSignal({type: "viewer_state", peer_id: peerId, viewer_id: viewerId, state: kind});
      setStatus(kind === "turn_relay" ? "已通过 TURN 中继连接，正在加载 WebUI..." : "P2P 直连成功，正在加载 WebUI...");
      installP2PPage().catch(e => fallback(String(e)));
    };
    dc.onerror = () => fallback("DataChannel 打开失败");

    signal.onopen = async () => {
      signal.send(JSON.stringify({type: "browser", peer_id: peerId, viewer_id: viewerId}));
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      await waitIceComplete(pc);
      signal.send(JSON.stringify({
        type: "offer",
        peer_id: peerId,
        viewer_id: viewerId,
        sdp: pc.localDescription.sdp,
        kind: pc.localDescription.type,
      }));
    };
    signal.onmessage = async ev => {
      const message = JSON.parse(ev.data);
      if (message.type === "answer") {
        await pc.setRemoteDescription({type: message.kind || "answer", sdp: message.sdp});
      } else if (message.type === "error") {
        fallback(message.message || "信令服务拒绝连接");
      }
    };
    signal.onerror = () => fallback("信令连接失败");
    signal.onclose = () => {
      if (!dc || dc.readyState !== "open") fallback("信令服务已断开");
    };
    setTimeout(() => {
      if (!dc || dc.readyState !== "open") fallback("10 秒内未打通");
    }, setupTimeoutMs);
  }

  connectP2P().catch(e => fallback(String(e)));
})();
</script>
</body>
</html>
"""


def keygen():
    key_file = path.join(config_dir, 'ssh_host_key')
    if not path.exists(key_file):
        key = asyncssh.generate_private_key('ssh-rsa')
        bytes = key.export_private_key()
        open(key_file, 'wb').write(bytes)
        print('ssh_host_key generated')


def parse_ssh_arguments(arguments_str):
    # 兼容 Alas 的 '-- --output json' 或 '--output=json'
    res = {}
    parts = arguments_str.split()
    for i, part in enumerate(parts):
        if part == '--output' and i + 1 < len(parts):
            res['output'] = parts[i+1]
        elif part.startswith('--output='):
            res['output'] = part.split('=', 1)[1]
    return res


async def handle_client(process):
    # 增加等待逻辑：最多等待 60 秒
    sock_name = None
    for i in range(600):
        sock_name = process.get_extra_info('sock_name')
        if sock_name:
            break
        await asyncio.sleep(0.1)

    if not sock_name:
        process.exit(1)
        return

    # 构造地址：address 优先给 P2P bootstrap，fallback_address 保留 SSH 反向隧道入口
    entrypoint = public_url('p2p', sock_name)
    fallback_entrypoint = public_url(sock_name)
    
    # 解析参数
    kwargs = parse_ssh_arguments(process.command or '')
    
    if kwargs.get('output') == 'json':
        # 严格按照官方格式返回 JSON
        response = json.dumps({
            'address': entrypoint,
            'fallback_address': fallback_entrypoint,
            'peer_id': sock_name,
            'signal_url': signal_url(),
            'ice_servers': ice_servers(),
            'status': 'success',
        }, ensure_ascii=False)
    else:
        # 文本模式
        response = 'The public entrypoint for your local web service is:\n%s' % entrypoint
    
    process.stdout.write(response + '\n')
    await process.stdout.drain() # 确保立即发送
    
    print(f'[{sock_name}] Response sent: address={entrypoint}, fallback={fallback_entrypoint}')

    # 关键修改：发送完地址后，绝对不主动退出，死等客户端断开
    try:
        await process.wait_closed()
    except Exception:
        pass


def get_sock_name(username):
    digest = hashlib.sha256(username.encode('utf-8')).hexdigest()
    return digest[:SOCK_NAME_LENGTH]


def public_scheme():
    return 'https' if https else 'http'


def websocket_scheme():
    return 'wss' if https else 'ws'


def public_url(*parts):
    tail = '/'.join(str(p).strip('/') for p in parts if str(p).strip('/'))
    if tail:
        return '%s://%s/%s' % (public_scheme(), server_name, tail)
    return '%s://%s' % (public_scheme(), server_name)


def signal_url():
    return '%s://%s/signal' % (websocket_scheme(), server_name)


def parse_json_list(value, default=None):
    if not value:
        return default or []
    try:
        data = json.loads(value)
        return data if isinstance(data, list) else default or []
    except json.JSONDecodeError:
        return [item.strip() for item in value.split(',') if item.strip()]


def ice_servers():
    servers = []
    stun_servers = parse_json_list(os.environ.get('STUN_SERVERS'), ['stun:stun.l.google.com:19302'])
    if stun_servers:
        servers.append({'urls': stun_servers})

    turn_servers = parse_json_list(os.environ.get('TURN_SERVERS'))
    turn_username = os.environ.get('TURN_USERNAME')
    turn_password = os.environ.get('TURN_PASSWORD')
    if turn_servers:
        item = {'urls': turn_servers}
        if turn_username:
            item['username'] = turn_username
        if turn_password:
            item['credential'] = turn_password
        servers.append(item)
    return servers


async def send_ws(ws, payload):
    if ws is not None and not ws.closed:
        await ws.send_str(json.dumps(payload, ensure_ascii=False))


async def cleanup_signal_peers():
    while True:
        for peer_id, peer in list(signal_peers.items()):
            ws = peer.get('ws')
            if ws is None or ws.closed:
                signal_peers.pop(peer_id, None)
        for viewer_id, viewer in list(signal_viewers.items()):
            ws = viewer.get('ws')
            if ws is None or ws.closed:
                signal_viewers.pop(viewer_id, None)
        await asyncio.sleep(30)


async def p2p_page(request):
    peer_id = request.match_info.get('peer_id', '').lower()
    if not peer_id or not peer_id.isalnum():
        return web.Response(status=404, text='Invalid peer id')

    html = (
        BOOTSTRAP_HTML
        .replace('__PEER_ID__', peer_id)
        .replace('__FALLBACK_URL__', public_url(peer_id))
        .replace('__SIGNAL_URL__', signal_url())
        .replace('__ICE_SERVERS__', json.dumps(ice_servers(), ensure_ascii=False))
    )
    return web.Response(text=html, content_type='text/html')


async def signal_handler(request):
    ws = web.WebSocketResponse(heartbeat=30)
    await ws.prepare(request)
    role = None
    peer_id = None
    viewer_id = None

    async for msg in ws:
        if msg.type != WSMsgType.TEXT:
            continue
        try:
            data = json.loads(msg.data)
        except json.JSONDecodeError:
            await send_ws(ws, {'type': 'error', 'message': 'Invalid JSON'})
            continue

        msg_type = data.get('type')
        if role == 'ap' and peer_id in signal_peers:
            signal_peers[peer_id]['seen'] = time.time()

        if msg_type == 'register':
            peer_id = str(data.get('peer_id') or '').lower()
            if not peer_id or not peer_id.isalnum():
                await send_ws(ws, {'type': 'error', 'message': 'Invalid peer id'})
                continue
            role = 'ap'
            signal_peers[peer_id] = {
                'ws': ws,
                'seen': time.time(),
                'fallback_url': data.get('fallback_url') or public_url(peer_id),
            }
            await send_ws(ws, {
                'type': 'registered',
                'peer_id': peer_id,
                'address': public_url('p2p', peer_id),
                'fallback_url': signal_peers[peer_id]['fallback_url'],
                'ice_servers': ice_servers(),
                'status': 'success',
            })
            print('[%s] P2P peer registered' % peer_id)
            continue

        if msg_type == 'browser':
            peer_id = str(data.get('peer_id') or '').lower()
            peer = signal_peers.get(peer_id)
            if not peer:
                await send_ws(ws, {'type': 'error', 'message': 'Peer is offline'})
                continue
            role = 'browser'
            viewer_id = data.get('viewer_id') or base64.urlsafe_b64encode(os.urandom(12)).decode().rstrip('=')
            signal_viewers[viewer_id] = {'ws': ws, 'peer_id': peer_id}
            await send_ws(peer['ws'], {'type': 'peer_join', 'viewer_id': viewer_id})
            await send_ws(ws, {'type': 'browser_registered', 'viewer_id': viewer_id})
            continue

        if msg_type in ('offer', 'candidate'):
            peer_id = str(data.get('peer_id') or peer_id or '').lower()
            peer = signal_peers.get(peer_id)
            if not peer:
                await send_ws(ws, {'type': 'error', 'message': 'Peer is offline'})
                continue
            payload = dict(data)
            payload['ice_servers'] = ice_servers()
            await send_ws(peer['ws'], payload)
            continue

        if msg_type in ('answer',):
            viewer_id = data.get('viewer_id')
            viewer = signal_viewers.get(viewer_id)
            if viewer:
                await send_ws(viewer['ws'], data)
            continue

        if msg_type == 'viewer_state':
            peer_id = str(data.get('peer_id') or '').lower()
            peer = signal_peers.get(peer_id)
            if peer:
                await send_ws(peer['ws'], data)
            continue

        if msg_type == 'ping':
            if peer_id in signal_peers:
                signal_peers[peer_id]['seen'] = time.time()
            await send_ws(ws, {'type': 'pong'})

    if role == 'ap' and peer_id and signal_peers.get(peer_id, {}).get('ws') is ws:
        signal_peers.pop(peer_id, None)
        print('[%s] P2P peer disconnected' % peer_id)
    if role == 'browser' and viewer_id:
        signal_viewers.pop(viewer_id, None)
    return ws


async def start_signal_server():
    app = web.Application()
    app.router.add_get('/signal', signal_handler)
    app.router.add_get('/p2p/{peer_id}', p2p_page)
    runner = web.AppRunner(app)
    await runner.setup()
    site = web.TCPSite(runner, signal_host, signal_port)
    await site.start()
    asyncio.create_task(cleanup_signal_peers())
    print('Signal server started at http://%s:%s' % (signal_host, signal_port))


class MySSHServer(asyncssh.SSHServer):
    conn = None

    def connection_made(self, conn):
        self.conn = conn

    def connection_lost(self, exc):
        if exc:
            print('SSH connection error: ' + str(exc), file=sys.stderr)

    def begin_auth(self, username):
        # 保存用户名到连接信息
        self.conn.set_extra_info(username=username)
        return False

    def password_auth_supported(self):
        return True

    def new_sock_path(self):
        username = self.conn.get_extra_info('username', '')
        sock_name = get_sock_name(username)
        
        sock_path = os.path.join(sock_dir, '%s.sock' % sock_name)
        self.conn.set_extra_info(sock_name=sock_name)
        return sock_path

    def unix_server_requested(self, listen_path):
        rewrite_path = self.new_sock_path()

        async def tunnel_connection(session_factory):
            """Forward a local connection over SSH"""
            # listen_path is a fake path
            return await self.conn.create_unix_connection(session_factory, listen_path)

        try:
            return create_unix_forward_listener(self.conn, asyncio.get_event_loop(),
                                                tunnel_connection,
                                                rewrite_path)
        except OSError as exc:
            raise

    def server_requested(self, listen_host, listen_port):
        """use sock forward even request port forward"""
        sock_path = self.new_sock_path()

        async def tunnel_connection(session_factory):
            """Forward a local connection over SSH"""
            fake_orig_host, fake_orig_port = '127.0.0.1', 8080
            return (await self.conn.create_connection(session_factory, listen_host, listen_port,
                                                      fake_orig_host, fake_orig_port))

        try:
            return create_unix_forward_listener(self.conn, asyncio.get_event_loop(),
                                                tunnel_connection, sock_path)
        except OSError as exc:
            raise


    def env_received(self, name, value):
        # 忽略客户端发送的所有环境变量，防止 LANG=C.UTF-8 等导致异常
        return True

async def start_server(host='0.0.0.0', port=1022):
    key_file = path.join(config_dir, 'ssh_host_key')
    await asyncssh.create_server(
        MySSHServer, host=host, port=port,
        server_host_keys=[key_file],
        process_factory=handle_client,
        # allow_pty=False,  # no allocation of a pseudo-tty
        agent_forwarding=False,
        allow_scp=False,
        keepalive_interval=30
    )


if __name__ == '__main__':
    import argparse

    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=1022, help='The port for ssh server')
    parser.add_argument("--config-dir", type=str, default='.', help='The dir to provide the required files')
    parser.add_argument("--socket-dir", type=str, default='/tmp/localshare', help='The dir to save the socket files')
    parser.add_argument("--signal-host", type=str, default='127.0.0.1', help='The host for local signaling HTTP server')
    parser.add_argument("--signal-port", type=int, default=8080, help='The port for local signaling HTTP server')
    parser.add_argument("--https", action="store_true", help='Whether to enable https')
    parser.add_argument("server_name", type=str, help='The domain name of the server')
    args = parser.parse_args()

    sock_dir = args.socket_dir
    server_name = args.server_name
    config_dir = args.config_dir
    server_port = args.port
    signal_host = args.signal_host
    signal_port = args.signal_port
    https = args.https or os.environ.get('HTTPS', '').lower() == 'true'

    os.umask(0o000)

    keygen()

    if not path.exists(sock_dir):
        os.mkdir(sock_dir)

    loop = asyncio.get_event_loop()
    try:
        loop.run_until_complete(start_server(port=args.port))
        loop.run_until_complete(start_signal_server())
    except (OSError, asyncssh.Error) as exc:
        sys.exit('Error starting server: ' + str(exc))
    loop.run_forever()
