"""管理后台：登录、会话与运行状态统计。

后台页面（``templates/admin.html``）通过一系列 ``/admin/api/*`` 接口读取
运行指标与活跃实例。密码以 SHA-256 摘要存于配置文件，登录后以 HttpOnly
cookie 维持会话，超时 24 小时自动失效。
"""

import secrets
import time
from os import path
from typing import Optional, TypedDict

from aiohttp import web

import geoip
import state
from state import (
    MAX_SSH_CONNECTIONS,
    MAX_SIGNAL_CONNECTIONS,
    MAX_SIGNAL_VIEWERS_PER_PEER,
    active_ssh_connections,
    active_ssh_peers,
    admin_sessions,
    metrics,
    peer_stats,
    signal_peers,
    signal_viewers,
)
from templates import ADMIN_HTML
from urls import public_url
from util import sha256_text


def admin_password_hash() -> str:
    """读取已保存的管理员密码摘要；未配置时返回空串。"""

    if state.admin_password_file and path.exists(state.admin_password_file):
        return open(state.admin_password_file, 'r', encoding='utf-8').read().strip()
    return ''


def save_admin_password(value: str) -> None:
    """计算并写入管理员密码的 SHA-256 摘要（明文不落盘）。"""

    digest = sha256_text(value)
    assert state.admin_password_file, 'admin password file not configured'
    open(state.admin_password_file, 'w', encoding='utf-8').write(digest + '\n')


def is_admin_setup() -> bool:
    """管理员密码是否已初始化。"""

    return bool(admin_password_hash())


def cleanup_admin_sessions() -> None:
    """清除超过 24 小时未活动的会话记录。"""

    now = time.time()
    for sid, item in list(admin_sessions.items()):
        if now - item.get('seen', 0) > 86400:
            admin_sessions.pop(sid, None)


def get_admin_session(request: web.Request) -> Optional[str]:
    """校验请求携带的管理员会话 cookie，有效则返回 sid 并刷新活跃时间。"""

    cleanup_admin_sessions()
    sid = request.cookies.get('localshare_admin')
    if not sid:
        return None
    item = admin_sessions.get(sid)
    if not item:
        return None
    item['seen'] = time.time()
    return sid


def require_admin(request: web.Request) -> Optional[web.Response]:
    if get_admin_session(request):
        return None
    return web.json_response({'error': 'Unauthorized'}, status=401)


def set_admin_cookie(response: web.Response, sid: str) -> web.Response:
    """在响应上写入管理会话 cookie（HttpOnly、SameSite=Strict、HTTPS 下加 secure）。"""

    response.set_cookie(
        'localshare_admin',
        sid,
        path='/admin',
        httponly=True,
        samesite='Strict',
        secure=state.https,
        max_age=86400,
    )
    return response


def clear_admin_cookie(response: web.Response) -> web.Response:
    """清除响应上的管理会话 cookie。"""

    response.del_cookie('localshare_admin', path='/admin')
    return response


async def admin_page(request: web.Request) -> web.Response:
    """返回管理后台单页应用的 HTML。"""

    return web.Response(text=ADMIN_HTML, content_type='text/html')


async def admin_session_api(request: web.Request) -> web.Response:
    """查询当前会话状态与是否需要首次设置密码。"""

    return web.json_response({
        'authenticated': bool(get_admin_session(request)),
        'setup_required': not is_admin_setup(),
    })


async def admin_setup_api(request: web.Request) -> web.Response:
    """首次使用：设置管理员密码（至少 8 位），只允许初始化一次。"""

    if is_admin_setup():
        return web.json_response({'error': 'Already initialized'}, status=409)
    data = await request.json()
    password = str(data.get('password') or '')
    if len(password) < 8:
        return web.json_response({'error': 'Password must be at least 8 characters'}, status=400)
    save_admin_password(password)
    return web.json_response({'ok': True})


async def admin_login_api(request: web.Request) -> web.Response:
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


async def admin_logout_api(request: web.Request) -> web.Response:
    """登出：删除会话并清除 cookie。"""

    sid = request.cookies.get('localshare_admin')
    if sid:
        admin_sessions.pop(sid, None)
    return clear_admin_cookie(web.json_response({'ok': True}))


class PeerSummary(TypedDict):
    """管理后台中单个 peer 的概览信息。"""

    peer_id: str
    ssh: bool
    signal: bool
    viewers: int
    fallback_url: str
    created_at: Optional[float]
    last_seen: Optional[float]
    ssh_connected_at: Optional[float]
    signal_connected_at: Optional[float]
    ssh_connections: int
    signal_connections: int
    viewers_total: int
    ip: Optional[str]
    country: Optional[str]
    province: Optional[str]
    bytes_in: int
    bytes_out: int


def peer_summary(peer_id: str) -> PeerSummary:
    """汇总单个 peer 的 SSH/信令/观看者状态，供后台表格展示。"""

    from state import peer_stat
    stat = peer_stat(peer_id)
    viewers = [viewer_id for viewer_id, viewer in signal_viewers.items() if viewer.get('peer_id') == peer_id]
    signal = signal_peers.get(peer_id)
    country, province = geoip.locate(stat.get('ip'))
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
        'ip': stat.get('ip'),
        'country': country,
        'province': province,
        'bytes_in': stat.get('bytes_in', 0),
        'bytes_out': stat.get('bytes_out', 0),
    }


async def admin_stats_api(request: web.Request) -> web.Response:
    """返回运行指标与活跃实例列表（需登录）。"""

    denied = require_admin(request)
    if denied:
        return denied
    peer_ids = set(peer_stats) | set(active_ssh_peers) | set(signal_peers)
    peer_ids |= {viewer.get('peer_id') for viewer in signal_viewers.values() if viewer.get('peer_id')}
    now = time.time()

    # 当前速率：取最近一次采样的差分除以采样间隔；尚无采样时按 0 处理
    cur_in = cur_out = 0.0
    if state.traffic_history:
        last = state.traffic_history[-1]
        cur_in = (last['ssh_in'] + last['signal_in']) / state.TRAFFIC_INTERVAL
        cur_out = (last['ssh_out'] + last['signal_out']) / state.TRAFFIC_INTERVAL

    return web.json_response({
        'now': now,
        'uptime': now - metrics['started_at'],        'limits': {
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
            'tunnel_bytes_in': metrics['ssh_tunnel_bytes_in'],
            'tunnel_bytes_out': metrics['ssh_tunnel_bytes_out'],
        },
        'traffic': {
            'interval': state.TRAFFIC_INTERVAL,
            'in': cur_in,
            'out': cur_out,
        },
        'traffic_history': [dict(sample) for sample in state.traffic_history],
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


async def admin_disconnect_peer_api(request: web.Request) -> web.Response:
    """强制断开指定 peer 的所有 SSH 与信令连接（需登录）。"""

    denied = require_admin(request)
    if denied:
        return denied
    peer_id = request.match_info.get('peer_id', '').lower()
    conn = active_ssh_peers.pop(peer_id, None)
    if conn is not None:
        conn.close()
    from signaling import close_ws
    peer = signal_peers.pop(peer_id, None)
    if peer:
        await close_ws(peer.get('ws'), message='admin disconnect')
    for viewer_id, viewer in list(signal_viewers.items()):
        if viewer.get('peer_id') == peer_id:
            signal_viewers.pop(viewer_id, None)
            await close_ws(viewer.get('ws'), message='admin disconnect')
    return web.json_response({'ok': True})
