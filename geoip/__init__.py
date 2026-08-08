# pyright: basic
"""IP 属地查询封装（基于 ip2region 离线数据库）。

使用 vendored 的官方解析库（``geoip.vendor``）查询 ``ip2region.xdb``，
返回 ``(国家, 省份)`` 二元组。查询结果做 LRU 缓存（单线程事件循环，无需锁）。
数据文件缺失或查询失败时返回 ``(None, None)``，不阻断服务。
"""

import ipaddress
import os
from collections import OrderedDict
from typing import Optional, Tuple

from .vendor import util
from .vendor.searcher import Searcher

RegionInfo = Tuple[Optional[str], Optional[str]]

DB_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'vendor', 'ip2region.xdb')

_searcher: Optional[Searcher] = None
_cache: 'OrderedDict[str, RegionInfo]' = OrderedDict()
_CACHE_MAX = 8192
_warned = False


def _get_searcher() -> Optional[Searcher]:
    """懒加载 Searcher（内容缓冲模式，首次约几十毫秒），失败返回 None。"""

    global _searcher, _warned
    if _searcher is not None:
        return _searcher
    if not os.path.exists(DB_PATH):
        if not _warned:
            _warned = True
            print('geoip: ip2region.xdb 缺失，地域信息不可用: %s' % DB_PATH)
        return None
    try:
        with open(DB_PATH, 'rb') as f:
            header = util.load_header(f)
            version = util.version_from_header(header)
            f.seek(0)
            buf = f.read()
        if version is None:
            raise ValueError('无法识别 xdb 版本')
        _searcher = new_with_buffer(version, buf)
    except Exception as exc:
        if not _warned:
            _warned = True
            print('geoip: 加载 ip2region.xdb 失败: %s' % exc)
        return None
    return _searcher


def new_with_buffer(version: util.Version, buf: bytes) -> Searcher:
    """用内容缓冲构造 Searcher（查询最快，约微秒级）。"""

    return Searcher(version, None, None, buf)


def _normalize_ip(ip: str) -> str:
    """归一化 IPv4/IPv6 地址为 xdb 可查的 IPv4 字符串。"""

    try:
        addr = ipaddress.ip_address(ip.strip())
    except ValueError:
        return ip
    if addr.version == 6:
        mapped = addr.ipv4_mapped
        if mapped is not None:
            return str(mapped)
        return ip
    return ip


def parse_region(raw: str) -> RegionInfo:
    """解析 xdb 返回的 '国家|区域|省份|城市|ISP'，取国家与省份，空/'0'/'Reserved' 视为 None。"""

    fields = raw.split('|')
    country = fields[0].strip() if len(fields) > 0 else ''
    province = fields[2].strip() if len(fields) > 2 else ''
    return _clean(country), _clean(province)


def _clean(value: str) -> Optional[str]:
    """把空串、'0'、'Reserved' 等占位值规范化为 None。"""

    if not value or value == '0' or value == 'Reserved':
        return None
    return value


def locate(ip: Optional[str]) -> RegionInfo:
    """查询 IP 属地并缓存；任何失败都返回 (None, None)。"""

    if not ip:
        return (None, None)
    ip = _normalize_ip(ip)
    hit = _cache.get(ip)
    if hit is not None:
        _cache.move_to_end(ip)
        return hit

    result: RegionInfo = (None, None)
    searcher = _get_searcher()
    if searcher is not None:
        try:
            result = parse_region(searcher.search(ip))
        except Exception:
            result = (None, None)

    _cache[ip] = result
    if len(_cache) > _CACHE_MAX:
        _cache.popitem(last=False)
    return result
