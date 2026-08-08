"""SSH 反向隧道服务。

接受客户端的 SSH 连接，将 ``-R`` 远程转发请求重写为对端专属的 unix
socket 文件（由 ``MySSHServer`` 决定路径），并在会话建立后打印公网入口
地址、保持连接直到客户端断开。SSH 认证采用能力开放模式：只校验连接数
上限，不校验口令，访问控制依赖公网 URL 的不可枚举性。
"""

import asyncio
import json
import os
import sys
import time
from os import path
from typing import Any, Optional

import asyncssh
from asyncssh.listener import create_unix_forward_listener

import state
from state import (
    MAX_SSH_CONNECTIONS,
    active_ssh_connections,
    active_ssh_peers,
    metrics,
    peer_stat,
)
from urls import ice_servers, public_url, signal_url
from util import get_sock_name, parse_ssh_arguments


async def handle_client(process: asyncssh.SSHServerProcess) -> None:
    """处理一次 SSH 会话：打印入口地址后挂起等待客户端断开。

    socket 名称由 ``MySSHServer`` 在转发建立时写入连接的 extra info，
    这里最多轮询等待 60 秒。
    """

    sock_name = None
    for _ in range(600):
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
    await process.stdout.drain()  # 确保立即发送

    print(f'[{sock_name}] Response sent: address={entrypoint}, fallback={fallback_entrypoint}')

    # 关键修改：发送完地址后，绝对不主动退出，死等客户端断开
    try:
        await process.wait_closed()
    except Exception:
        pass


def keygen() -> None:
    """在配置目录生成 SSH 主机密钥（不存在时才生成）。"""

    key_file = path.join(state.config_dir, 'ssh_host_key')
    if not path.exists(key_file):
        key = asyncssh.generate_private_key('ssh-rsa')
        bytes = key.export_private_key()
        open(key_file, 'wb').write(bytes)
        print('ssh_host_key generated')


class TunnelTrafficSession:
    """隧道会话包装：统计 SSH 隧道进/出字节并写回全局与 peer 统计。

    入站数据经 ``data_received`` 计数；出站数据不经过 session 对象（asyncssh
    隧道内层 forwarder 直连 ``chan.write``），由 :func:`wrap_session_factory`
    对内层对象的 ``write`` 方法做实例补丁来计数。
    """

    def __init__(self, inner: Any, peer_id: str) -> None:
        self._inner = inner
        self._peer_id = peer_id

    def connection_made(self, chan: Any) -> None:
        self._inner.connection_made(chan)

    def session_started(self) -> None:
        if hasattr(self._inner, 'session_started'):
            self._inner.session_started()

    def data_received(self, data: bytes, datatype: Any = None) -> None:
        metrics['ssh_tunnel_bytes_in'] += len(data)
        peer_stat(self._peer_id)['bytes_in'] += len(data)
        self._inner.data_received(data, datatype)

    def eof_received(self) -> bool:
        return self._inner.eof_received()

    def connection_lost(self, exc: Optional[BaseException]) -> None:
        self._inner.connection_lost(exc)

    def pause_writing(self) -> None:
        if hasattr(self._inner, 'pause_writing'):
            self._inner.pause_writing()

    def resume_writing(self) -> None:
        if hasattr(self._inner, 'resume_writing'):
            self._inner.resume_writing()

    def write(self, data: bytes) -> None:
        self._inner.write(data)

    def close(self) -> None:
        self._inner.close()


def wrap_session_factory(session_factory: Any, peer_id: str) -> Any:
    """包装 asyncssh 隧道 session 工厂，统计该 peer 的隧道字节。

    出站数据在 asyncssh 内部直接走 ``inner.write``（实例方法补丁在工厂返回
    前完成，覆盖连接建立后的全部出站）；返回的包装对象同时代理入站计数。
    """

    def wrapped_factory() -> TunnelTrafficSession:
        inner = session_factory()
        real_write = inner.write

        def counted_write(data: bytes) -> None:
            metrics['ssh_tunnel_bytes_out'] += len(data)
            peer_stat(peer_id)['bytes_out'] += len(data)
            real_write(data)

        inner.write = counted_write  # type: ignore  # asyncssh 普通类无 __slots__
        return TunnelTrafficSession(inner, peer_id)

    return wrapped_factory


class MySSHServer(asyncssh.SSHServer):
    """asyncssh 服务端适配器：把反向转发重写为 peer 专属 socket。"""

    conn: Optional[asyncssh.SSHServerConnection] = None  # connection_made 时赋值
    sock_name: Optional[str] = None

    def connection_made(self, conn: asyncssh.SSHServerConnection) -> None:
        """新连接建立：先做连接数上限检查，再登记连接并采集客户端 IP。"""

        if len(active_ssh_connections) >= MAX_SSH_CONNECTIONS:
            metrics['ssh_rejected'] += 1
            print('Reject SSH connection: too many active SSH connections', file=sys.stderr)
            conn.close()
            return
        self.conn = conn
        active_ssh_connections.add(conn)
        metrics['ssh_total'] += 1

        # 采集客户端 IP（peername 为 (host, port) 元组），供后台地图与实例列表展示
        peername = conn.get_extra_info('peername')
        if isinstance(peername, tuple) and peername and isinstance(peername[0], str):
            conn.set_extra_info(peer_ip=peername[0])

    def connection_lost(self, exc: Optional[BaseException]) -> None:
        """连接断开：清理活跃连接登记，必要时移除对端映射。"""

        if self.conn is not None:
            active_ssh_connections.discard(self.conn)
        if self.sock_name and active_ssh_peers.get(self.sock_name) is self.conn:
            active_ssh_peers.pop(self.sock_name, None)
        if exc:
            print('SSH connection error: ' + str(exc), file=sys.stderr)

    def begin_auth(self, username: str) -> bool:
        """认证开始：记录用户名，接受任意口令（能力开放模式）。"""

        assert self.conn is not None
        if self.conn not in active_ssh_connections:
            return True
        # 保存用户名到连接信息，供后续 new_sock_path 派生 socket 名
        self.conn.set_extra_info(username=username)
        return False

    def password_auth_supported(self) -> bool:
        """声明支持密码认证。"""

        return True

    def new_sock_path(self) -> str:
        """派生并登记本连接的 unix socket 路径。

        同一用户名的新连接会替换旧连接（旧连接被关闭），确保同一 peer
        同时只有一个活跃隧道。
        """

        assert self.conn is not None
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
        stat['ip'] = self.conn.get_extra_info('peer_ip') or None

        sock_path = os.path.join(state.sock_dir, '%s.sock' % sock_name)
        self.conn.set_extra_info(sock_name=sock_name)
        return sock_path

    def unix_server_requested(self, listen_path: str) -> Any:
        """处理 ``-R /<path>`` 形式的 unix 转发请求。

        客户端声明的路径只是占位，实际监听路径被重写为 peer 专属 socket。
        """

        assert self.conn is not None
        rewrite_path = self.new_sock_path()

        async def tunnel_connection(session_factory: Any) -> Any:
            """在 SSH 通道上建立一条 unix 隧道连接（带字节统计包装）。"""

            # listen_path 是 fake path，真正的转发目标是客户端本地的 listen_path
            assert self.conn is not None
            assert self.sock_name is not None
            return await self.conn.create_unix_connection(wrap_session_factory(session_factory, self.sock_name),
                                                          listen_path)

        try:
            return create_unix_forward_listener(self.conn, asyncio.get_event_loop(),
                                                tunnel_connection,
                                                rewrite_path)
        except OSError:
            raise

    def server_requested(self, listen_host: str, listen_port: int) -> Any:
        """处理 TCP 远程转发请求：同样重写为 peer 专属 socket。"""

        assert self.conn is not None
        sock_path = self.new_sock_path()

        async def tunnel_connection(session_factory: Any) -> Any:
            """在 SSH 通道上建立一条 TCP 隧道连接（带字节统计包装）。"""

            fake_orig_host, fake_orig_port = '127.0.0.1', 8080
            assert self.conn is not None
            assert self.sock_name is not None
            return (await self.conn.create_connection(wrap_session_factory(session_factory, self.sock_name),
                                                      listen_host, listen_port,
                                                      fake_orig_host, fake_orig_port))

        try:
            return create_unix_forward_listener(self.conn, asyncio.get_event_loop(),
                                                tunnel_connection, sock_path)
        except OSError:
            raise

    def env_received(self, name: str, value: str) -> bool:
        """忽略客户端发送的所有环境变量，防止 LANG=C.UTF-8 等导致异常。"""

        return True


async def start_server(host: str = '0.0.0.0', port: int = 1022) -> None:
    """启动 SSH 服务（监听 :1022），绑定主机密钥与客户端会话处理器。"""

    key_file = path.join(state.config_dir, 'ssh_host_key')
    await asyncssh.create_server(
        MySSHServer, host=host, port=port,
        server_host_keys=[key_file],
        process_factory=handle_client,
        # allow_pty=False,  # 不分配伪终端（客户端是隧道而非交互 shell）
        agent_forwarding=False,
        allow_scp=False,
        keepalive_interval=30
    )
