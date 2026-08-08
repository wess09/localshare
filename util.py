"""无业务状态的纯工具函数集合。

这些函数不依赖服务运行时的任何可变状态，只做确定性的字符串/环境处理，
可在任意模块中安全复用。
"""

import hashlib
import os
from typing import Dict, cast

from state import SOCK_NAME_LENGTH


def sha256_text(value: str) -> str:
    """计算字符串的 SHA-256 十六进制摘要（UTF-8 编码后）。"""

    return hashlib.sha256(value.encode('utf-8')).hexdigest()


def get_sock_name(username: str) -> str:
    """根据 SSH 用户名生成确定性的 socket 名称（sha256 前 16 位）。

    同一用户名永远得到同一名称，用于把 SSH 反向隧道对端路由到固定的
    unix socket 文件。
    """

    digest = hashlib.sha256(username.encode('utf-8')).hexdigest()
    return digest[:SOCK_NAME_LENGTH]


def set_nofile_limit() -> None:
    """尝试将进程的文件描述符上限提高到 NOFILE_LIMIT 指定值。

    仅在支持 ``resource`` 模块的平台上生效（Linux）；Windows 上静默跳过。
    静默失败是设计意图：此优化失败不应阻断服务启动。
    """

    value = int(os.environ.get('NOFILE_LIMIT', '200000'))
    try:
        import resource
    except ImportError:
        return

    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)  # type: ignore  # Windows typeshed 无此属性，容器是 Linux
    soft, hard = cast(int, soft), cast(int, hard)
    target = min(value, hard if hard > 0 else value)
    if soft < target:
        resource.setrlimit(resource.RLIMIT_NOFILE, (target, hard))  # type: ignore
        print('RLIMIT_NOFILE raised from %s to %s' % (soft, target))


def parse_ssh_arguments(arguments_str: str) -> Dict[str, str]:
    """解析 SSH 客户端在命令串里传的参数。

    兼容 Alas 的 ``'-- --output json'`` 或 ``'--output=json'`` 两种写法，
    当前只关心 ``output`` 键，其余参数一律忽略。
    """

    res: Dict[str, str] = {}
    parts = arguments_str.split()
    for i, part in enumerate(parts):
        if part == '--output' and i + 1 < len(parts):
            res['output'] = parts[i + 1]
        elif part.startswith('--output='):
            res['output'] = part.split('=', 1)[1]
    return res
