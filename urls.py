"""URL 构造与 ICE（STUN/TURN）服务器配置解析。

对外展示的入口地址、信令 WebSocket 地址都从这里生成，并统一随
``state.https`` 切换 http/https、ws/wss。所有函数只读共享配置，不修改状态。
"""

import json
import os
from typing import Dict, List, Optional, Union, cast

import state


def public_scheme() -> str:
    """返回公网入口使用的 URL 协议（http 或 https）。"""

    return 'https' if state.https else 'http'


def websocket_scheme() -> str:
    """返回信令 WebSocket 使用的协议（wss 或 ws）。"""

    return 'wss' if state.https else 'ws'


def public_url(*parts: str) -> str:
    """拼接完整的公网入口 URL。

    将若干路径片段拼到 ``state.server_name`` 之后，例如 ``public_url('p2p', id)``
    得到 ``https://<server>/p2p/<id>``。空片段会被忽略。
    """

    tail = '/'.join(p.strip('/') for p in parts if p.strip('/'))
    if tail:
        return '%s://%s/%s' % (public_scheme(), state.server_name, tail)
    return '%s://%s' % (public_scheme(), state.server_name)


def signal_url() -> str:
    """返回 WebRTC 信令服务的 WebSocket 地址。"""

    return '%s://%s/signal' % (websocket_scheme(), state.server_name)


def parse_json_list(value: Optional[str], default: Optional[List[str]] = None) -> List[str]:
    """解析环境变量中的列表配置。

    兼容两种格式：JSON 数组字符串（如 ``'["a","b"]'``）或逗号分隔字符串
    （如 ``'a,b'``）。解析失败或值为空时返回 ``default``。
    """

    if not value:
        return default or []
    try:
        data = json.loads(value)
        if isinstance(data, list):
            items = cast(List[object], data)
            return [str(item) for item in items]
        return default or []
    except json.JSONDecodeError:
        return [item.strip() for item in value.split(',') if item.strip()]


IceServer = Dict[str, Union[str, List[str]]]


def ice_servers() -> List[IceServer]:
    """从环境变量构造 WebRTC 的 ICE 服务器列表。

    STUN 默认使用 Google 公共服务器；TURN 仅在配置了 ``TURN_SERVERS`` 时加入，
    可附带 ``TURN_USERNAME``/``TURN_PASSWORD`` 做认证。
    """

    servers: List[IceServer] = []
    stun_servers = parse_json_list(os.environ.get('STUN_SERVERS'), ['stun:stun.l.google.com:19302'])
    if stun_servers:
        servers.append({'urls': stun_servers})

    turn_servers = parse_json_list(os.environ.get('TURN_SERVERS'))
    turn_username = os.environ.get('TURN_USERNAME')
    turn_password = os.environ.get('TURN_PASSWORD')
    if turn_servers:
        item: IceServer = {'urls': turn_servers}
        if turn_username:
            item['username'] = turn_username
        if turn_password:
            item['credential'] = turn_password
        servers.append(item)
    return servers
