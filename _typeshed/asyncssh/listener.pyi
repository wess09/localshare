from typing import Any

from asyncssh import SSHConnection


def create_unix_forward_listener(conn: SSHConnection, loop: Any,
                                 session_factory: Any, listen_path: str) -> Any: ...
