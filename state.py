"""集中管理服务共享的可变状态与运行时配置。

本项目拆分为多个模块后，所有跨模块共享的全局 dict/set 统一放在这里，
各模块通过 ``import state`` 引用同一批对象（而不是 ``from state import ...``
复制引用），保证信令服务、SSH 服务和管理后台操作的是同一份状态。
"""

import os
import time
from collections import deque
from typing import Any, Deque, Dict, Optional, Set, TypedDict

from asyncssh import SSHServerConnection

# 运行时配置：由 main.py 入口在启动时用命令行参数覆盖
config_dir: str = os.path.abspath(os.path.dirname(__file__))
server_name: str = 'remote.nanoda.work'
sock_dir: str = ''  # unix socket 输出目录，启动时被 --socket-dir 覆盖
server_port: int = 1022
https: bool = False  # 是否启用 HTTPS/WSS（影响所有 URL 与 cookie 的 secure 标志）
signal_host: str = '127.0.0.1'
signal_port: int = 8080

# 标识与容量常量
SOCK_NAME_LENGTH: int = 16
SIGNAL_PEER_TTL: int = 120  # 信令对端存活判定阈值（秒），当前由 cleanup 任务使用
MAX_SSH_CONNECTIONS: int = int(os.environ.get('MAX_SSH_CONNECTIONS', '100000'))
MAX_SIGNAL_CONNECTIONS: int = int(os.environ.get('MAX_SIGNAL_CONNECTIONS', '100000'))
MAX_SIGNAL_VIEWERS_PER_PEER: int = int(os.environ.get('MAX_SIGNAL_VIEWERS_PER_PEER', '64'))

# 流量采样配置
TRAFFIC_INTERVAL: float = 5.0  # 采样间隔（秒），对齐前端 5s 轮询
TRAFFIC_HISTORY_LEN: int = 1440  # 环形缓冲容量：2 小时（1440 × 5s）


class SignalPeer(TypedDict):
    """一条已注册的 P2P 对端（ap 代理）记录。"""

    ws: Any  # aiohttp WebSocketResponse
    seen: float  # 最近一次活跃时间戳
    fallback_url: str  # 无 P2P 时的 SSH 转发兜底入口


class SignalViewer(TypedDict):
    """一个等待接入 P2P 的浏览器 viewer 记录。"""

    ws: Any  # aiohttp WebSocketResponse
    peer_id: str


class AdminSession(TypedDict):
    """一条管理后台登录会话记录。"""

    seen: float  # 最近一次活动时间戳，用于超时清理


class Metrics(TypedDict):
    """服务运行以来累计的计数指标（供管理后台展示）。"""

    started_at: float
    ssh_total: int
    ssh_rejected: int
    ssh_replaced: int
    signal_total: int
    signal_rejected: int
    signal_messages_in: int
    signal_messages_out: int
    signal_bytes_in: int
    signal_bytes_out: int
    ssh_tunnel_bytes_in: int  # SSH 隧道累计入站字节（客户端→服务端）
    ssh_tunnel_bytes_out: int  # SSH 隧道累计出站字节（服务端→客户端）
    viewer_total: int
    p2p_pages: int
    p2p_page_bytes: int
    admin_logins: int
    admin_failed_logins: int


class PeerStat(TypedDict):
    """某个 peer（按 sock_name 识别）的生命周期统计。"""

    created_at: float
    ssh_connected_at: Optional[float]
    signal_connected_at: Optional[float]
    last_seen: Optional[float]
    ssh_connections: int
    signal_connections: int
    viewers_total: int
    ip: Optional[str]  # SSH 客户端 IP（connection_made 时采集）
    bytes_in: int  # 该 peer 隧道累计入站字节
    bytes_out: int  # 该 peer 隧道累计出站字节


class TrafficSample(TypedDict):
    """一次流量采样的差分值（本间隔内的增量字节）。"""

    ts: float  # 采样时间戳
    ssh_in: int
    ssh_out: int
    signal_in: int
    signal_out: int


# 共享可变状态：所有模块通过这些全局 dict/set 通信
signal_peers: Dict[str, SignalPeer] = {}
signal_viewers: Dict[str, SignalViewer] = {}
active_ssh_connections: Set[SSHServerConnection] = set()
active_ssh_peers: Dict[str, SSHServerConnection] = {}
admin_sessions: Dict[str, AdminSession] = {}
admin_password_file: str = ''  # 启动时被 ADMIN_PASSWORD_FILE 或 config-dir 覆盖
metrics: Metrics = {
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
    'ssh_tunnel_bytes_in': 0,
    'ssh_tunnel_bytes_out': 0,
    'viewer_total': 0,
    'p2p_pages': 0,
    'p2p_page_bytes': 0,
    'admin_logins': 0,
    'admin_failed_logins': 0,
}
peer_stats: Dict[str, PeerStat] = {}
traffic_history: Deque[TrafficSample] = deque(maxlen=TRAFFIC_HISTORY_LEN)


def peer_stat(peer_id: str) -> PeerStat:
    """取回 peer 的统计记录，不存在时先初始化一份再返回。"""

    return peer_stats.setdefault(peer_id, {
        'created_at': time.time(),
        'ssh_connected_at': None,
        'signal_connected_at': None,
        'last_seen': None,
        'ssh_connections': 0,
        'signal_connections': 0,
        'viewers_total': 0,
        'ip': None,
        'bytes_in': 0,
        'bytes_out': 0,
    })


def traffic_snapshot() -> Dict[str, int]:
    """返回四项累计字节计数快照，供采样任务做差分。"""

    return {
        'ssh_in': metrics['ssh_tunnel_bytes_in'],
        'ssh_out': metrics['ssh_tunnel_bytes_out'],
        'signal_in': metrics['signal_bytes_in'],
        'signal_out': metrics['signal_bytes_out'],
    }
