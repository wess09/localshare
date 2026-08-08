"""服务入口：解析命令行参数、注入运行时配置并启动全部服务。

将命令行参数写入 ``state`` 模块（各业务模块通过 ``state.xxx`` 读取），
然后依次启动 SSH 隧道服务（:1022）与信令/后台服务（:8080），
最后进入事件循环常驻。入口本身的逻辑极薄，业务实现见 ``ssh``/``signaling``。
"""

import argparse
import asyncio
import os
import sys
from os import path

import asyncssh

import state
from signaling import start_signal_server
from ssh import keygen, start_server
from util import set_nofile_limit


def main() -> None:
    """解析参数并启动 SSH 服务与信令服务。"""

    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=1022, help='The port for ssh server')
    parser.add_argument("--config-dir", type=str, default='.', help='The dir to provide the required files')
    parser.add_argument("--socket-dir", type=str, default='/tmp/localshare', help='The dir to save the socket files')
    parser.add_argument("--signal-host", type=str, default='127.0.0.1', help='The host for local signaling HTTP server')
    parser.add_argument("--signal-port", type=int, default=8080, help='The port for local signaling HTTP server')
    parser.add_argument("--https", action="store_true", help='Whether to enable https')
    parser.add_argument("server_name", type=str, help='The domain name of the server')
    args = parser.parse_args()

    # 注入运行时配置：必须在启动任何服务之前完成
    state.sock_dir = args.socket_dir
    state.server_name = args.server_name
    state.config_dir = args.config_dir
    state.server_port = args.port
    state.signal_host = args.signal_host
    state.signal_port = args.signal_port
    state.https = args.https or os.environ.get('HTTPS', '').lower() == 'true'
    state.admin_password_file = os.environ.get('ADMIN_PASSWORD_FILE') or path.join(args.config_dir, 'admin_password.sha256')

    os.umask(0o000)

    set_nofile_limit()
    keygen()

    if not path.exists(state.sock_dir):
        os.mkdir(state.sock_dir)

    loop = asyncio.get_event_loop()
    try:
        loop.run_until_complete(start_server(port=args.port))
        loop.run_until_complete(start_signal_server())
    except (OSError, asyncssh.Error) as exc:
        sys.exit('Error starting server: ' + str(exc))
    loop.run_forever()


if __name__ == '__main__':
    main()
