"""WebRTC 信令服务与 P2P 引导页。

负责：`/signal` WebSocket（对端 `ap` 与浏览器 `browser` 之间的 SDP/ICE 交换）、
`/p2p/<peer_id>` 引导页，以及 `/admin*` 后台接口的路由组装。信令只做转发
不解析媒体数据，真正的流量经浏览器与 ap 之间协商出的 DataChannel 传输。
"""

import asyncio
import base64
import json
import os
import time
from typing import Any, Dict, Optional

from aiohttp import web, WSMsgType
from aiohttp.http_websocket import WSMessage

from admin import (
    admin_disconnect_peer_api,
    admin_login_api,
    admin_logout_api,
    admin_page,
    admin_session_api,
    admin_setup_api,
    admin_stats_api,
)
import state
from state import (
    MAX_SIGNAL_CONNECTIONS,
    MAX_SIGNAL_VIEWERS_PER_PEER,
    SIGNAL_HANDSHAKE_TIMEOUT,
    metrics,
    peer_stat,
    signal_peers,
    signal_viewers,
)
from templates import BOOTSTRAP_HTML
from urls import ice_servers, public_url

STATIC_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'static')


async def traffic_sampler() -> None:
    """每 TRAFFIC_INTERVAL 秒差分四项累计字节并追加到 traffic_history 环形缓冲。"""

    last = state.traffic_snapshot()
    while True:
        await asyncio.sleep(state.TRAFFIC_INTERVAL)
        cur = state.traffic_snapshot()
        state.traffic_history.append({
            'ts': time.time(),
            'ssh_in': cur['ssh_in'] - last['ssh_in'],
            'ssh_out': cur['ssh_out'] - last['ssh_out'],
            'signal_in': cur['signal_in'] - last['signal_in'],
            'signal_out': cur['signal_out'] - last['signal_out'],
        })
        last = cur


async def send_ws(ws: Optional[web.WebSocketResponse], payload: Dict[str, Any]) -> None:
    """向信令连接发送一条 JSON 消息并计入出站流量指标。"""

    if ws is not None and not ws.closed:
        text = json.dumps(payload, ensure_ascii=False)
        metrics['signal_messages_out'] += 1
        metrics['signal_bytes_out'] += len(text.encode('utf-8'))
        await ws.send_str(text)


async def close_ws(ws: Optional[web.WebSocketResponse], code: int = 1000, message: str = 'replaced') -> None:
    """关闭信令连接（默认以 'replaced' 表示被新连接顶替）。"""

    if ws is not None and not ws.closed:
        await ws.close(code=code, message=message.encode('utf-8'))


async def cleanup_signal_peers() -> None:
    """周期清理已断开的信令对端与观看者，并向失去对端的 viewer 发送关闭。"""

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


async def p2p_page(request: web.Request) -> web.Response:
    """渲染 P2P 引导页，替换 peer id 与 ICE 服务器占位符。"""

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


async def _handle_signal_message(ws: web.WebSocketResponse,
                                 msg: WSMessage,
                                 sess: Dict[str, Any]) -> None:
    """处理一条信令消息，按 type 分派；会话状态（role/peer_id/viewer_id）存入 sess。"""

    metrics['signal_messages_in'] += 1
    metrics['signal_bytes_in'] += len(msg.data.encode('utf-8'))
    try:
        data = json.loads(msg.data)
    except json.JSONDecodeError:
        await send_ws(ws, {'type': 'error', 'message': 'Invalid JSON'})
        return

    role = sess.get('role')
    peer_id = sess.get('peer_id')
    msg_type = data.get('type')
    if role == 'ap' and peer_id in signal_peers:
        signal_peers[peer_id]['seen'] = time.time()

    if msg_type == 'register':
        peer_id = str(data.get('peer_id') or '').lower()
        if not peer_id or not peer_id.isalnum():
            await send_ws(ws, {'type': 'error', 'message': 'Invalid peer id'})
            return
        old_peer = signal_peers.get(peer_id)
        if old_peer and old_peer.get('ws') is not ws:
            await close_ws(old_peer.get('ws'), message='peer replaced')
            for old_viewer_id, old_viewer in list(signal_viewers.items()):
                if old_viewer.get('peer_id') == peer_id:
                    signal_viewers.pop(old_viewer_id, None)
                    await close_ws(old_viewer.get('ws'), message='peer replaced')
        sess['role'] = 'ap'
        sess['peer_id'] = peer_id
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
        return

    if msg_type == 'browser':
        peer_id = str(data.get('peer_id') or '').lower()
        peer = signal_peers.get(peer_id)
        if not peer:
            await send_ws(ws, {'type': 'error', 'message': 'Peer is offline'})
            return
        viewer_count = sum(1 for item in signal_viewers.values() if item.get('peer_id') == peer_id)
        if viewer_count >= MAX_SIGNAL_VIEWERS_PER_PEER:
            await send_ws(ws, {'type': 'error', 'message': 'Too many viewers'})
            await close_ws(ws, code=1013, message='too many viewers')
            return
        sess['role'] = 'browser'
        viewer_id = data.get('viewer_id') or base64.urlsafe_b64encode(os.urandom(12)).decode().rstrip('=')
        sess['viewer_id'] = viewer_id
        old_viewer = signal_viewers.get(viewer_id)
        if old_viewer and old_viewer.get('ws') is not ws:
            await close_ws(old_viewer.get('ws'), message='viewer replaced')
        signal_viewers[viewer_id] = {'ws': ws, 'peer_id': peer_id}
        metrics['viewer_total'] += 1
        peer_stat(peer_id)['viewers_total'] += 1
        await send_ws(peer['ws'], {'type': 'peer_join', 'viewer_id': viewer_id})
        await send_ws(ws, {'type': 'browser_registered', 'viewer_id': viewer_id})
        return

    if msg_type in ('offer', 'candidate'):
        peer_id = str(data.get('peer_id') or peer_id or '').lower()
        peer = signal_peers.get(peer_id)
        if not peer:
            await send_ws(ws, {'type': 'error', 'message': 'Peer is offline'})
            return
        payload = dict(data)
        payload['ice_servers'] = ice_servers()
        await send_ws(peer['ws'], payload)
        return

    if msg_type in ('answer',):
        viewer_id = data.get('viewer_id')
        viewer = signal_viewers.get(viewer_id)
        if viewer:
            await send_ws(viewer['ws'], data)
        return

    if msg_type == 'viewer_state':
        peer_id = str(data.get('peer_id') or '').lower()
        peer = signal_peers.get(peer_id)
        if peer:
            await send_ws(peer['ws'], data)
        return

    if msg_type == 'ping':
        if peer_id in signal_peers:
            signal_peers[peer_id]['seen'] = time.time()
            peer_stat(peer_id)['last_seen'] = time.time()
        await send_ws(ws, {'type': 'pong'})


async def signal_handler(request: web.Request) -> web.StreamResponse:
    """处理 `/signal` WebSocket 会话，转发注册/offer/candidate 等信令消息。

    对端以 `ap` 角色注册（携带 peer_id），浏览器以 `browser` 角色加入；
    SDP 与 ICE candidate 在两者之间透明转发。连接断开时清理登记。
    """

    if len(signal_peers) + len(signal_viewers) >= MAX_SIGNAL_CONNECTIONS:
        metrics['signal_rejected'] += 1
        return web.Response(status=503, text='Too many signaling connections')

    ws = web.WebSocketResponse(heartbeat=30)
    try:
        await ws.prepare(request)
    except (RuntimeError, ConnectionError):
        # 对端在握手阶段就断开（探活/健康检查常见），直接静默退出
        return ws
    metrics['signal_total'] += 1
    sess: Dict[str, Any] = {}

    # 首条消息必须在超时内到达（register/browser），否则视为僵尸连接主动关闭，
    # 避免探活类连接只建握手不发消息、长期占用 fd 与 nginx worker 连接
    try:
        msg = await asyncio.wait_for(ws.receive(), timeout=SIGNAL_HANDSHAKE_TIMEOUT)
    except asyncio.TimeoutError:
        await ws.close()
        return ws
    except ConnectionError:
        return ws

    if msg.type != WSMsgType.TEXT:
        await ws.close()
        return ws
    await _handle_signal_message(ws, msg, sess)

    try:
        async for msg in ws:
            if msg.type == WSMsgType.TEXT:
                await _handle_signal_message(ws, msg, sess)
    except ConnectionError:
        pass

    role = sess.get('role')
    peer_id = sess.get('peer_id')
    viewer_id = sess.get('viewer_id')
    if role == 'ap' and peer_id:
        peer = signal_peers.get(peer_id)
        if peer and peer.get('ws') is ws:
            signal_peers.pop(peer_id)
            print('[%s] P2P peer disconnected' % peer_id)
    if role == 'browser' and viewer_id:
        signal_viewers.pop(viewer_id, None)
    return ws


async def start_signal_server() -> None:
    """启动 aiohttp 信令/后台服务并挂载全部路由。"""

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
    # 本地直连 aiohttp 时的静态资源路由（生产环境由 nginx 的 /static/ alias 提供服务）
    app.router.add_static('/static', STATIC_DIR)
    runner = web.AppRunner(app)
    await runner.setup()
    site = web.TCPSite(runner, state.signal_host, state.signal_port)
    await site.start()
    asyncio.create_task(cleanup_signal_peers())
    asyncio.create_task(traffic_sampler())
    print('Signal server started at http://%s:%s' % (state.signal_host, state.signal_port))
