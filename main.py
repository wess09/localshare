import asyncio
import base64
import hashlib
import json
import os
import secrets
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
MAX_SSH_CONNECTIONS = int(os.environ.get('MAX_SSH_CONNECTIONS', '100000'))
MAX_SIGNAL_CONNECTIONS = int(os.environ.get('MAX_SIGNAL_CONNECTIONS', '100000'))
MAX_SIGNAL_VIEWERS_PER_PEER = int(os.environ.get('MAX_SIGNAL_VIEWERS_PER_PEER', '64'))

signal_peers = {}
signal_viewers = {}
active_ssh_connections = set()
active_ssh_peers = {}
admin_sessions = {}
admin_password_file = None
metrics = {
    'started_at': time.time(),
    'ssh_total': 0,
    'ssh_rejected': 0,
    'ssh_replaced': 0,
    'signal_total': 0,
    'signal_rejected': 0,
    'signal_messages_in': 0,
    'signal_messages_out': 0,
    'signal_bytes_in': 0,
    'signal_bytes_out': 0,
    'viewer_total': 0,
    'p2p_pages': 0,
    'p2p_page_bytes': 0,
    'admin_logins': 0,
    'admin_failed_logins': 0,
}
peer_stats = {}


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
  const fallbackUrl = location.origin + "/" + peerId;
  const signalUrl = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/signal";
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

  function bytesToExactBuffer(bytes) {
    return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
  }

  function normalizePath(input) {
    const url = new URL(String(input), location.href);
    return url.pathname + url.search;
  }

  function browserContext() {
    return {
      user_agent: navigator.userAgent || "",
      accept_language: navigator.languages && navigator.languages.length ? navigator.languages.join(",") : navigator.language || "",
    };
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
      const data = payload.binary ? bytesToExactBuffer(base64ToBytes(payload.data)) : payload.data;
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
      socket._message(item.binary ? bytesToExactBuffer(merged) : dec.decode(merged));
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
      ...browserContext(),
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
      this.bufferedAmount = 0;
      this.extensions = "";
      this.id = "ws-" + (++wsSeq).toString(36) + "-" + Date.now().toString(36);
      socketMap.set(this.id, this);
      queueMicrotask(() => {
        try {
          sendChannel({type: "ws.open", id: this.id, path: normalizePath(this.url), protocol: this.protocol, ...browserContext()});
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
      if (data instanceof ArrayBuffer) {
        value = this.binaryType === "blob" ? new Blob([data]) : data;
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
        sendChannel({type: "sse.open", id: this.id, path: normalizePath(this.url), ...browserContext()});
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

  async function installP2PPage(kind) {
    window.__apP2PFetch = p2pFetch;
    window.__apP2PWebSocket = P2PWebSocket;
    window.__apP2PEventSource = P2PEventSource;
    window.__apP2PConnectionKind = kind;
    const response = await p2pFetch("/");
    const html = await response.text();
    const shim = `<base href="/"><script>
      (() => {
        const p2pFetch = window.__apP2PFetch;
        const P2PWebSocket = window.__apP2PWebSocket;
        const P2PEventSource = window.__apP2PEventSource;
        const p2pConnectionKind = window.__apP2PConnectionKind;
        const originalFetch = window.fetch.bind(window);
        const OriginalXMLHttpRequest = window.XMLHttpRequest;
        function shouldBypassP2P(url) {
          return url.pathname.startsWith("/static/") || url.pathname.startsWith("/pywebio_static/");
        }
        function shouldProxy(input) {
          try {
            const url = new URL(typeof input === "string" ? input : input.url, location.href);
            return url.origin === location.origin
              && !url.pathname.startsWith("/p2p/")
              && url.pathname !== "/signal"
              && !shouldBypassP2P(url);
          } catch (e) {
            return false;
          }
        }
        window.fetch = (input, init) => shouldProxy(input) ? p2pFetch(input, init) : originalFetch(input, init);
        window.XMLHttpRequest = class {
          constructor() {
            this.native = null;
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
          open(method, url, async=true, user, password) {
            this.method = method;
            this.url = url;
            if (!shouldProxy(url)) {
              this.native = new OriginalXMLHttpRequest();
              this.native.onreadystatechange = () => {
                this.readyState = this.native.readyState;
                this.status = this.native.status;
                this.statusText = this.native.statusText;
                this.responseText = this.native.responseText;
                this.response = this.native.response;
                this._change();
              };
              this.native.onload = ev => { if (this.onload) this.onload(ev); };
              this.native.onerror = ev => { if (this.onerror) this.onerror(ev); };
              this.native.open(method, url, async, user, password);
              return;
            }
            this.readyState = 1;
            this._change();
          }
          setRequestHeader(k, v) {
            if (this.native) return this.native.setRequestHeader(k, v);
            this.headers[k] = v;
          }
          getResponseHeader(k) { return this.responseHeaders ? this.responseHeaders.get(k) : null; }
          getAllResponseHeaders() { return this.responseHeaders ? Array.from(this.responseHeaders.entries()).map(([k,v]) => k + ": " + v).join("\\r\\n") : ""; }
          async send(body) {
            if (this.native) return this.native.send(body);
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
          abort() { if (this.native) this.native.abort(); }
          _change() { if (this.onreadystatechange) this.onreadystatechange(); }
        };
        window.WebSocket = P2PWebSocket;
        window.EventSource = P2PEventSource;
        function showConnectionBadge() {
          const badge = document.createElement("div");
          const isTurn = p2pConnectionKind === "turn_relay";
          badge.textContent = isTurn ? "TURN 中继" : "P2P 直连";
          badge.title = isTurn ? "当前 WebUI 流量通过 WebRTC TURN 中继传输" : "当前 WebUI 流量通过 WebRTC DataChannel 直连传输";
          badge.style.cssText = [
            "position:fixed",
            "right:12px",
            "bottom:12px",
            "z-index:2147483647",
            "padding:6px 10px",
            "border-radius:6px",
            "font:12px/1.2 -apple-system,BlinkMacSystemFont,Segoe UI,sans-serif",
            "color:#fff",
            "background:" + (isTurn ? "rgba(180,83,9,.9)" : "rgba(5,150,105,.9)"),
            "box-shadow:0 6px 18px rgba(0,0,0,.22)",
            "pointer-events:none"
          ].join(";");
          if (document.body) {
            document.body.appendChild(badge);
          } else {
            document.addEventListener("DOMContentLoaded", () => document.body.appendChild(badge), {once: true});
          }
        }
        showConnectionBadge();
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
    pc.onicecandidate = ev => {
      sendSignal({
        type: "candidate",
        peer_id: peerId,
        viewer_id: viewerId,
        candidate: ev.candidate ? {
          candidate: ev.candidate.candidate,
          sdpMid: ev.candidate.sdpMid,
          sdpMLineIndex: ev.candidate.sdpMLineIndex,
        } : null,
      });
    };
    dc.onopen = async () => {
      const kind = await detectConnectionType(pc);
      sendSignal({type: "viewer_state", peer_id: peerId, viewer_id: viewerId, state: kind});
      setStatus(kind === "turn_relay" ? "已通过 TURN 中继连接，正在加载 WebUI..." : "P2P 直连成功，正在加载 WebUI...");
      installP2PPage(kind).catch(e => fallback(String(e)));
    };
    dc.onerror = () => fallback("DataChannel 打开失败");

    signal.onopen = async () => {
      signal.send(JSON.stringify({type: "browser", peer_id: peerId, viewer_id: viewerId}));
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
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


ADMIN_HTML = r"""<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Localshare Admin</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f6f7f9;
      --panel: #ffffff;
      --text: #18202a;
      --muted: #687385;
      --line: #dfe4ea;
      --blue: #2563eb;
      --green: #15915b;
      --red: #d33838;
      --amber: #b7791f;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--bg);
      color: var(--text);
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      letter-spacing: 0;
    }
    button, input { font: inherit; }
    .shell { min-height: 100vh; display: grid; grid-template-columns: 232px 1fr; }
    aside {
      border-right: 1px solid var(--line);
      background: #fbfcfd;
      padding: 22px 18px;
    }
    .brand { font-size: 18px; font-weight: 750; margin-bottom: 4px; }
    .subtle { color: var(--muted); font-size: 13px; }
    nav { margin-top: 28px; display: grid; gap: 8px; }
    nav a {
      color: var(--text);
      text-decoration: none;
      padding: 9px 10px;
      border-radius: 6px;
      font-size: 14px;
    }
    nav a.active { background: #eaf1ff; color: #174ea6; }
    main { padding: 24px; min-width: 0; }
    header { display: flex; justify-content: space-between; align-items: center; gap: 16px; margin-bottom: 22px; }
    h1 { margin: 0; font-size: 24px; line-height: 1.2; }
    .toolbar { display: flex; gap: 8px; align-items: center; }
    .btn {
      border: 1px solid var(--line);
      background: #fff;
      color: var(--text);
      border-radius: 6px;
      padding: 8px 11px;
      cursor: pointer;
    }
    .btn.primary { background: var(--blue); border-color: var(--blue); color: #fff; }
    .grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; }
    .metric, .panel {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
    }
    .metric { padding: 16px; }
    .metric .label { color: var(--muted); font-size: 13px; }
    .metric .value { margin-top: 8px; font-size: 28px; font-weight: 760; }
    .metric .note { margin-top: 6px; color: var(--muted); font-size: 12px; }
    .layout { display: grid; grid-template-columns: 1.25fr .75fr; gap: 12px; margin-top: 12px; }
    .panel h2 { margin: 0; padding: 14px 16px; border-bottom: 1px solid var(--line); font-size: 16px; }
    table { width: 100%; border-collapse: collapse; font-size: 13px; }
    th, td { padding: 11px 12px; border-bottom: 1px solid var(--line); text-align: left; white-space: nowrap; }
    th { color: var(--muted); font-weight: 650; background: #fbfcfd; }
    td.url { max-width: 260px; overflow: hidden; text-overflow: ellipsis; }
    .status { display: inline-flex; align-items: center; gap: 6px; }
    .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--green); }
    .dot.warn { background: var(--amber); }
    .stack { padding: 14px 16px; display: grid; gap: 12px; }
    .row { display: flex; justify-content: space-between; gap: 12px; color: var(--muted); font-size: 13px; }
    .row strong { color: var(--text); font-weight: 650; }
    .login {
      min-height: 100vh;
      display: grid;
      place-items: center;
      padding: 24px;
    }
    .login-card {
      width: min(420px, 100%);
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 22px;
    }
    .login-card h1 { font-size: 22px; margin-bottom: 8px; }
    label { display: grid; gap: 7px; margin: 14px 0; color: var(--muted); font-size: 13px; }
    input {
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 10px 11px;
      color: var(--text);
      background: #fff;
    }
    .error { color: var(--red); min-height: 20px; font-size: 13px; }
    .hidden { display: none; }
    @media (max-width: 900px) {
      .shell { grid-template-columns: 1fr; }
      aside { display: none; }
      main { padding: 16px; }
      .grid, .layout { grid-template-columns: 1fr; }
      header { align-items: flex-start; flex-direction: column; }
      th:nth-child(4), td:nth-child(4) { display: none; }
    }
  </style>
</head>
<body>
  <section id="login" class="login hidden">
    <form class="login-card" id="login-form">
      <h1>Localshare Admin</h1>
      <p class="subtle" id="login-copy">登录后查看活跃实例与流量指标。</p>
      <label>
        <span>密码</span>
        <input id="password" type="password" autocomplete="current-password" required>
      </label>
      <label id="confirm-wrap" class="hidden">
        <span>确认密码</span>
        <input id="confirm-password" type="password" autocomplete="new-password">
      </label>
      <div class="error" id="login-error"></div>
      <button class="btn primary" type="submit" id="login-button">登录</button>
    </form>
  </section>

  <section id="app" class="shell hidden">
    <aside>
      <div class="brand">Localshare</div>
      <div class="subtle">管理后台</div>
      <nav><a class="active" href="#overview">Overview</a></nav>
    </aside>
    <main>
      <header>
        <div>
          <h1>运行概览</h1>
          <div class="subtle" id="updated">等待刷新</div>
        </div>
        <div class="toolbar">
          <button class="btn" id="refresh" type="button">刷新</button>
          <button class="btn" id="logout" type="button">退出</button>
        </div>
      </header>

      <section class="grid">
        <div class="metric"><div class="label">活跃 SSH</div><div class="value" id="m-ssh">0</div><div class="note" id="m-ssh-note">0 / 0</div></div>
        <div class="metric"><div class="label">P2P Peer</div><div class="value" id="m-peer">0</div><div class="note" id="m-viewer">0 viewers</div></div>
        <div class="metric"><div class="label">信令流量</div><div class="value" id="m-signal-bytes">0 B</div><div class="note" id="m-signal-msg">0 messages</div></div>
        <div class="metric"><div class="label">运行时间</div><div class="value" id="m-uptime">0s</div><div class="note" id="m-p2p">0 bootstrap pages</div></div>
      </section>

      <section class="layout">
        <div class="panel">
          <h2>活跃实例</h2>
          <table>
            <thead><tr><th>Peer</th><th>SSH</th><th>Signal</th><th>Viewers</th><th>地址</th><th></th></tr></thead>
            <tbody id="peer-rows"></tbody>
          </table>
        </div>
        <div class="panel">
          <h2>资源与事件</h2>
          <div class="stack" id="resource-list"></div>
        </div>
      </section>
    </main>
  </section>

  <script>
    const login = document.getElementById("login");
    const app = document.getElementById("app");
    const form = document.getElementById("login-form");
    const password = document.getElementById("password");
    const confirmWrap = document.getElementById("confirm-wrap");
    const confirmPassword = document.getElementById("confirm-password");
    const loginButton = document.getElementById("login-button");
    const loginCopy = document.getElementById("login-copy");
    const loginError = document.getElementById("login-error");
    let setupRequired = false;
    let timer = null;

    function fmtBytes(n) {
      const units = ["B", "KB", "MB", "GB", "TB"];
      let value = Number(n || 0);
      let i = 0;
      while (value >= 1024 && i < units.length - 1) { value /= 1024; i++; }
      return `${value.toFixed(value >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
    }

    function fmtDuration(seconds) {
      seconds = Math.max(0, Math.floor(seconds || 0));
      const d = Math.floor(seconds / 86400);
      const h = Math.floor(seconds % 86400 / 3600);
      const m = Math.floor(seconds % 3600 / 60);
      if (d) return `${d}d ${h}h`;
      if (h) return `${h}h ${m}m`;
      if (m) return `${m}m`;
      return `${seconds}s`;
    }

    async function request(path, options = {}) {
      const res = await fetch(path, {
        credentials: "same-origin",
        headers: {"Content-Type": "application/json"},
        ...options,
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || res.statusText);
      return data;
    }

    function showLogin(status) {
      setupRequired = !!status.setup_required;
      login.classList.remove("hidden");
      app.classList.add("hidden");
      loginError.textContent = status.message || "";
      confirmWrap.classList.toggle("hidden", !setupRequired);
      confirmPassword.required = setupRequired;
      loginButton.textContent = setupRequired ? "设置密码" : "登录";
      loginCopy.textContent = setupRequired ? "首次使用，请设置管理密码。服务端仅保存 SHA-256。" : "登录后查看活跃实例与流量指标。";
    }

    function showApp() {
      login.classList.add("hidden");
      app.classList.remove("hidden");
      refresh();
      clearInterval(timer);
      timer = setInterval(refresh, 5000);
    }

    async function refresh() {
      const data = await request("/admin/api/stats");
      document.getElementById("updated").textContent = `最后刷新 ${new Date(data.now * 1000).toLocaleString()}`;
      document.getElementById("m-ssh").textContent = data.ssh.active;
      document.getElementById("m-ssh-note").textContent = `${data.ssh.total} total · ${data.ssh.rejected} rejected`;
      document.getElementById("m-peer").textContent = data.signal.peers;
      document.getElementById("m-viewer").textContent = `${data.signal.viewers} viewers`;
      document.getElementById("m-signal-bytes").textContent = fmtBytes(data.signal.bytes_in + data.signal.bytes_out);
      document.getElementById("m-signal-msg").textContent = `${data.signal.messages_in + data.signal.messages_out} messages`;
      document.getElementById("m-uptime").textContent = fmtDuration(data.uptime);
      document.getElementById("m-p2p").textContent = `${data.http.p2p_pages} bootstrap pages`;

      const rows = data.peers.map(peer => `
        <tr>
          <td>${peer.peer_id}</td>
          <td><span class="status"><span class="dot ${peer.ssh ? "" : "warn"}"></span>${peer.ssh ? "online" : "none"}</span></td>
          <td>${peer.signal ? "online" : "none"}</td>
          <td>${peer.viewers}</td>
          <td class="url">${peer.fallback_url || ""}</td>
          <td><button class="btn" type="button" data-peer="${peer.peer_id}">断开</button></td>
        </tr>
      `).join("");
      document.getElementById("peer-rows").innerHTML = rows || `<tr><td colspan="6">暂无活跃实例</td></tr>`;

      document.getElementById("resource-list").innerHTML = [
        ["SSH 上限", `${data.limits.ssh}`],
        ["信令上限", `${data.limits.signal}`],
        ["单实例 viewer 上限", `${data.limits.viewers_per_peer}`],
        ["SSH 替换次数", `${data.ssh.replaced}`],
        ["P2P 页面流量", fmtBytes(data.http.p2p_page_bytes)],
        ["后台登录", `${data.admin.logins} success · ${data.admin.failed_logins} failed`],
      ].map(([k, v]) => `<div class="row"><span>${k}</span><strong>${v}</strong></div>`).join("");
    }

    form.addEventListener("submit", async ev => {
      ev.preventDefault();
      loginError.textContent = "";
      if (setupRequired && password.value !== confirmPassword.value) {
        loginError.textContent = "两次输入的密码不一致";
        return;
      }
      try {
        await request(setupRequired ? "/admin/api/setup" : "/admin/api/login", {
          method: "POST",
          body: JSON.stringify({password: password.value}),
        });
        password.value = "";
        confirmPassword.value = "";
        if (setupRequired) {
          showLogin({setup_required: false, message: "密码已设置，请登录"});
        } else {
          showApp();
        }
      } catch (e) {
        loginError.textContent = e.message;
      }
    });

    document.getElementById("refresh").addEventListener("click", refresh);
    document.getElementById("logout").addEventListener("click", async () => {
      await request("/admin/api/logout", {method: "POST", body: "{}"}).catch(() => {});
      clearInterval(timer);
      showLogin({setup_required: false});
    });
    document.getElementById("peer-rows").addEventListener("click", async ev => {
      const btn = ev.target.closest("button[data-peer]");
      if (!btn) return;
      await request(`/admin/api/peers/${btn.dataset.peer}/disconnect`, {method: "POST", body: "{}"});
      refresh();
    });

    request("/admin/api/session").then(status => {
      if (status.authenticated) showApp();
      else showLogin(status);
    }).catch(() => showLogin({setup_required: false}));
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


def set_nofile_limit():
    value = int(os.environ.get('NOFILE_LIMIT', '200000'))
    try:
        import resource
    except ImportError:
        return

    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
    target = min(value, hard if hard > 0 else value)
    if soft < target:
        resource.setrlimit(resource.RLIMIT_NOFILE, (target, hard))
        print('RLIMIT_NOFILE raised from %s to %s' % (soft, target))


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


def sha256_text(value):
    return hashlib.sha256(value.encode('utf-8')).hexdigest()


def admin_password_hash():
    if admin_password_file and path.exists(admin_password_file):
        return open(admin_password_file, 'r', encoding='utf-8').read().strip()
    return ''


def save_admin_password(value):
    digest = sha256_text(value)
    open(admin_password_file, 'w', encoding='utf-8').write(digest + '\n')


def is_admin_setup():
    return bool(admin_password_hash())


def cleanup_admin_sessions():
    now = time.time()
    for sid, item in list(admin_sessions.items()):
        if now - item.get('seen', 0) > 86400:
            admin_sessions.pop(sid, None)


def get_admin_session(request):
    cleanup_admin_sessions()
    sid = request.cookies.get('localshare_admin')
    item = admin_sessions.get(sid)
    if not item:
        return None
    item['seen'] = time.time()
    return sid


def require_admin(request):
    if get_admin_session(request):
        return None
    return web.json_response({'error': 'Unauthorized'}, status=401)


def set_admin_cookie(response, sid):
    response.set_cookie(
        'localshare_admin',
        sid,
        path='/admin',
        httponly=True,
        samesite='Strict',
        secure=https,
        max_age=86400,
    )
    return response


def clear_admin_cookie(response):
    response.del_cookie('localshare_admin', path='/admin')
    return response


def peer_stat(peer_id):
    return peer_stats.setdefault(peer_id, {
        'created_at': time.time(),
        'ssh_connected_at': None,
        'signal_connected_at': None,
        'last_seen': None,
        'ssh_connections': 0,
        'signal_connections': 0,
        'viewers_total': 0,
    })


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
        text = json.dumps(payload, ensure_ascii=False)
        metrics['signal_messages_out'] += 1
        metrics['signal_bytes_out'] += len(text.encode('utf-8'))
        await ws.send_str(text)


async def close_ws(ws, code=1000, message='replaced'):
    if ws is not None and not ws.closed:
        await ws.close(code=code, message=message.encode('utf-8'))


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
                continue
            peer_id = viewer.get('peer_id')
            peer = signal_peers.get(peer_id)
            if not peer or peer.get('ws') is None or peer.get('ws').closed:
                signal_viewers.pop(viewer_id, None)
                await close_ws(ws, message='peer offline')
        await asyncio.sleep(30)


async def p2p_page(request):
    peer_id = request.match_info.get('peer_id', '').lower()
    if not peer_id or not peer_id.isalnum():
        return web.Response(status=404, text='Invalid peer id')

    html = (
        BOOTSTRAP_HTML
        .replace('__PEER_ID__', peer_id)
        .replace('__ICE_SERVERS__', json.dumps(ice_servers(), ensure_ascii=False))
    )
    metrics['p2p_pages'] += 1
    metrics['p2p_page_bytes'] += len(html.encode('utf-8'))
    return web.Response(text=html, content_type='text/html')


async def signal_handler(request):
    if len(signal_peers) + len(signal_viewers) >= MAX_SIGNAL_CONNECTIONS:
        metrics['signal_rejected'] += 1
        return web.Response(status=503, text='Too many signaling connections')

    ws = web.WebSocketResponse(heartbeat=30)
    await ws.prepare(request)
    metrics['signal_total'] += 1
    role = None
    peer_id = None
    viewer_id = None

    async for msg in ws:
        if msg.type != WSMsgType.TEXT:
            continue
        metrics['signal_messages_in'] += 1
        metrics['signal_bytes_in'] += len(msg.data.encode('utf-8'))
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
            old_peer = signal_peers.get(peer_id)
            if old_peer and old_peer.get('ws') is not ws:
                await close_ws(old_peer.get('ws'), message='peer replaced')
                for old_viewer_id, old_viewer in list(signal_viewers.items()):
                    if old_viewer.get('peer_id') == peer_id:
                        signal_viewers.pop(old_viewer_id, None)
                        await close_ws(old_viewer.get('ws'), message='peer replaced')
            role = 'ap'
            stat = peer_stat(peer_id)
            stat['signal_connected_at'] = time.time()
            stat['last_seen'] = time.time()
            stat['signal_connections'] += 1
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
            viewer_count = sum(1 for item in signal_viewers.values() if item.get('peer_id') == peer_id)
            if viewer_count >= MAX_SIGNAL_VIEWERS_PER_PEER:
                await send_ws(ws, {'type': 'error', 'message': 'Too many viewers'})
                await close_ws(ws, code=1013, message='too many viewers')
                continue
            role = 'browser'
            viewer_id = data.get('viewer_id') or base64.urlsafe_b64encode(os.urandom(12)).decode().rstrip('=')
            old_viewer = signal_viewers.get(viewer_id)
            if old_viewer and old_viewer.get('ws') is not ws:
                await close_ws(old_viewer.get('ws'), message='viewer replaced')
            signal_viewers[viewer_id] = {'ws': ws, 'peer_id': peer_id}
            metrics['viewer_total'] += 1
            peer_stat(peer_id)['viewers_total'] += 1
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
                peer_stat(peer_id)['last_seen'] = time.time()
            await send_ws(ws, {'type': 'pong'})

    if role == 'ap' and peer_id and signal_peers.get(peer_id, {}).get('ws') is ws:
        signal_peers.pop(peer_id, None)
        print('[%s] P2P peer disconnected' % peer_id)
    if role == 'browser' and viewer_id:
        signal_viewers.pop(viewer_id, None)
    return ws


async def admin_page(request):
    return web.Response(text=ADMIN_HTML, content_type='text/html')


async def admin_session_api(request):
    return web.json_response({
        'authenticated': bool(get_admin_session(request)),
        'setup_required': not is_admin_setup(),
    })


async def admin_setup_api(request):
    if is_admin_setup():
        return web.json_response({'error': 'Already initialized'}, status=409)
    data = await request.json()
    password = str(data.get('password') or '')
    if len(password) < 8:
        return web.json_response({'error': 'Password must be at least 8 characters'}, status=400)
    save_admin_password(password)
    return web.json_response({'ok': True})


async def admin_login_api(request):
    data = await request.json()
    password = str(data.get('password') or '')
    digest = admin_password_hash()
    if not digest:
        return web.json_response({'error': 'Admin password is not initialized'}, status=400)
    if not secrets.compare_digest(sha256_text(password), digest):
        metrics['admin_failed_logins'] += 1
        return web.json_response({'error': 'Invalid password'}, status=401)
    sid = secrets.token_urlsafe(32)
    admin_sessions[sid] = {'seen': time.time()}
    metrics['admin_logins'] += 1
    return set_admin_cookie(web.json_response({'ok': True}), sid)


async def admin_logout_api(request):
    sid = request.cookies.get('localshare_admin')
    if sid:
        admin_sessions.pop(sid, None)
    return clear_admin_cookie(web.json_response({'ok': True}))


def peer_summary(peer_id):
    stat = peer_stat(peer_id)
    viewers = [viewer_id for viewer_id, viewer in signal_viewers.items() if viewer.get('peer_id') == peer_id]
    signal = signal_peers.get(peer_id)
    return {
        'peer_id': peer_id,
        'ssh': peer_id in active_ssh_peers,
        'signal': bool(signal and signal.get('ws') and not signal.get('ws').closed),
        'viewers': len(viewers),
        'fallback_url': signal.get('fallback_url') if signal else public_url(peer_id),
        'created_at': stat.get('created_at'),
        'last_seen': stat.get('last_seen'),
        'ssh_connected_at': stat.get('ssh_connected_at'),
        'signal_connected_at': stat.get('signal_connected_at'),
        'ssh_connections': stat.get('ssh_connections', 0),
        'signal_connections': stat.get('signal_connections', 0),
        'viewers_total': stat.get('viewers_total', 0),
    }


async def admin_stats_api(request):
    denied = require_admin(request)
    if denied:
        return denied
    peer_ids = set(peer_stats) | set(active_ssh_peers) | set(signal_peers)
    peer_ids |= {viewer.get('peer_id') for viewer in signal_viewers.values() if viewer.get('peer_id')}
    now = time.time()
    return web.json_response({
        'now': now,
        'uptime': now - metrics['started_at'],
        'limits': {
            'ssh': MAX_SSH_CONNECTIONS,
            'signal': MAX_SIGNAL_CONNECTIONS,
            'viewers_per_peer': MAX_SIGNAL_VIEWERS_PER_PEER,
        },
        'ssh': {
            'active': len(active_ssh_connections),
            'peers': len(active_ssh_peers),
            'total': metrics['ssh_total'],
            'rejected': metrics['ssh_rejected'],
            'replaced': metrics['ssh_replaced'],
        },
        'signal': {
            'peers': len(signal_peers),
            'viewers': len(signal_viewers),
            'total': metrics['signal_total'],
            'rejected': metrics['signal_rejected'],
            'messages_in': metrics['signal_messages_in'],
            'messages_out': metrics['signal_messages_out'],
            'bytes_in': metrics['signal_bytes_in'],
            'bytes_out': metrics['signal_bytes_out'],
            'viewer_total': metrics['viewer_total'],
        },
        'http': {
            'p2p_pages': metrics['p2p_pages'],
            'p2p_page_bytes': metrics['p2p_page_bytes'],
        },
        'admin': {
            'logins': metrics['admin_logins'],
            'failed_logins': metrics['admin_failed_logins'],
        },
        'peers': [peer_summary(peer_id) for peer_id in sorted(peer_ids)],
    })


async def admin_disconnect_peer_api(request):
    denied = require_admin(request)
    if denied:
        return denied
    peer_id = request.match_info.get('peer_id', '').lower()
    conn = active_ssh_peers.pop(peer_id, None)
    if conn is not None:
        conn.close()
    peer = signal_peers.pop(peer_id, None)
    if peer:
        await close_ws(peer.get('ws'), message='admin disconnect')
    for viewer_id, viewer in list(signal_viewers.items()):
        if viewer.get('peer_id') == peer_id:
            signal_viewers.pop(viewer_id, None)
            await close_ws(viewer.get('ws'), message='admin disconnect')
    return web.json_response({'ok': True})


async def start_signal_server():
    app = web.Application()
    app.router.add_get('/signal', signal_handler)
    app.router.add_get('/p2p/{peer_id}', p2p_page)
    app.router.add_get('/admin', admin_page)
    app.router.add_get('/admin/', admin_page)
    app.router.add_get('/admin/api/session', admin_session_api)
    app.router.add_post('/admin/api/setup', admin_setup_api)
    app.router.add_post('/admin/api/login', admin_login_api)
    app.router.add_post('/admin/api/logout', admin_logout_api)
    app.router.add_get('/admin/api/stats', admin_stats_api)
    app.router.add_post('/admin/api/peers/{peer_id}/disconnect', admin_disconnect_peer_api)
    runner = web.AppRunner(app)
    await runner.setup()
    site = web.TCPSite(runner, signal_host, signal_port)
    await site.start()
    asyncio.create_task(cleanup_signal_peers())
    print('Signal server started at http://%s:%s' % (signal_host, signal_port))


class MySSHServer(asyncssh.SSHServer):
    conn = None
    sock_name = None

    def connection_made(self, conn):
        if len(active_ssh_connections) >= MAX_SSH_CONNECTIONS:
            metrics['ssh_rejected'] += 1
            print('Reject SSH connection: too many active SSH connections', file=sys.stderr)
            conn.close()
            return
        self.conn = conn
        active_ssh_connections.add(conn)
        metrics['ssh_total'] += 1

    def connection_lost(self, exc):
        if self.conn is not None:
            active_ssh_connections.discard(self.conn)
        if self.sock_name and active_ssh_peers.get(self.sock_name) is self.conn:
            active_ssh_peers.pop(self.sock_name, None)
        if exc:
            print('SSH connection error: ' + str(exc), file=sys.stderr)

    def begin_auth(self, username):
        if self.conn not in active_ssh_connections:
            return True
        # 保存用户名到连接信息
        self.conn.set_extra_info(username=username)
        return False

    def password_auth_supported(self):
        return True

    def new_sock_path(self):
        if self.conn not in active_ssh_connections:
            raise asyncssh.ChannelOpenError(asyncssh.OPEN_CONNECT_FAILED, 'Too many SSH connections')
        username = self.conn.get_extra_info('username', '')
        sock_name = get_sock_name(username)
        old_conn = active_ssh_peers.get(sock_name)
        if old_conn is not None and old_conn is not self.conn:
            metrics['ssh_replaced'] += 1
            print('[%s] Replacing previous SSH connection' % sock_name)
            old_conn.close()
        active_ssh_peers[sock_name] = self.conn
        self.sock_name = sock_name
        stat = peer_stat(sock_name)
        stat['ssh_connected_at'] = time.time()
        stat['last_seen'] = time.time()
        stat['ssh_connections'] += 1
        
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
    admin_password_file = os.environ.get('ADMIN_PASSWORD_FILE') or path.join(config_dir, 'admin_password.sha256')

    os.umask(0o000)

    set_nofile_limit()
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
