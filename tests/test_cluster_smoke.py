import importlib.util
import contextlib
import io
import os
import sys
import tempfile
import types
import unittest


def load_main_module(env=None):
    env = env or {}
    old_env = os.environ.copy()
    os.environ.update(env)
    old_modules = {name: sys.modules.get(name) for name in [
        'asyncssh', 'asyncssh.listener', 'aiohttp',
    ]}

    asyncssh = types.ModuleType('asyncssh')
    asyncssh.SSHServer = type('SSHServer', (), {})
    asyncssh.Error = Exception
    asyncssh.ChannelOpenError = Exception
    asyncssh.OPEN_CONNECT_FAILED = 2
    asyncssh.OPEN_ADMINISTRATIVELY_PROHIBITED = 1
    asyncssh.create_server = lambda *args, **kwargs: object()
    asyncssh.generate_private_key = lambda *args, **kwargs: type(
        'Key', (), {'export_private_key': lambda self: b'key'}
    )()
    sys.modules['asyncssh'] = asyncssh

    listener = types.ModuleType('asyncssh.listener')
    listener.create_unix_forward_listener = lambda *args, **kwargs: object()
    sys.modules['asyncssh.listener'] = listener

    aiohttp = types.ModuleType('aiohttp')
    aiohttp.web = types.SimpleNamespace(
        json_response=lambda data, status=200: {'data': data, 'status': status},
        Response=lambda *args, **kwargs: {'response': kwargs},
        HTTPFound=type(
            'HTTPFound', (Exception,),
            {'__init__': lambda self, location: setattr(self, 'location', location)},
        ),
    )
    aiohttp.WSMsgType = types.SimpleNamespace(TEXT='text')
    aiohttp.ClientSession = object
    aiohttp.ClientTimeout = lambda *args, **kwargs: None
    sys.modules['aiohttp'] = aiohttp

    try:
        spec = importlib.util.spec_from_file_location('localshare_main_test', 'main.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module
    finally:
        os.environ.clear()
        os.environ.update(old_env)
        for name, value in old_modules.items():
            if value is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = value


class ClusterSmokeTest(unittest.TestCase):
    def test_scheduler_capacity_weight_and_sticky_route(self):
        main = load_main_module()
        fd, db_path = tempfile.mkstemp()
        os.close(fd)
        store = main.ClusterStore(db_path, heartbeat_timeout=30)

        store.upsert_node({
            'node_id': 'master',
            'ssh_server': 'remote.example.com:1022',
            'public_base_url': 'https://remote.example.com',
            'weight': 100,
            'enabled': True,
            'maintenance': False,
            'max_tunnels': 1,
            'current_tunnels': 0,
            'is_local': True,
        })
        store.upsert_node({
            'node_id': 'node-a',
            'ssh_server': 'node-a.example.com:1022',
            'public_base_url': 'https://node-a.example.com',
            'weight': 200,
            'enabled': True,
            'maintenance': False,
            'max_tunnels': 10,
            'current_tunnels': 0,
            'token': 'node-token',
            'last_heartbeat': main.now_ts(),
        })

        node, reason = store.select_node_for_token('abc12345')
        self.assertEqual('node-a', node['node_id'])
        self.assertEqual('weighted_least_connection', reason)

        store.patch_node('node-a', {'max_tunnels': 0})
        node, _ = store.select_node_for_token('abc12345')
        self.assertEqual('master', node['node_id'])

        store.register_route({
            'token': 'abc12345',
            'node_id': 'master',
            'target_url': 'https://remote.example.com/abc12345',
            'public_url': 'https://remote.example.com/abc12345',
            'expires_at': main.now_ts() + 60,
        })
        node, reason = store.select_node_for_token('abc12345')
        self.assertEqual('master', node['node_id'])
        self.assertEqual('existing_route', reason)

    def test_success_payload_keeps_public_master_url(self):
        main = load_main_module()
        main.cluster_public_base_url = 'https://remote.example.com'
        payload = main.build_success_payload('abc12345')
        self.assertEqual('success', payload['status'])
        self.assertEqual('https://remote.example.com/p2p/abc12345', payload['address'])
        self.assertEqual('https://remote.example.com/abc12345', payload['fallback_address'])
        self.assertEqual('wss://remote.example.com/signal', payload['signal_url'])

    def test_master_setup_registers_local_worker(self):
        fd, db_path = tempfile.mkstemp()
        os.close(fd)
        config_dir = tempfile.mkdtemp()
        main = load_main_module({
            'LOCALSHARE_ROLE': 'master',
            'REMOTE_STATE_DB': db_path,
            'MASTER_MAX_TUNNELS': '2',
        })
        main.config_dir = config_dir
        main.server_name = 'remote.example.com'
        main.server_port = 1022
        main.https = True

        with contextlib.redirect_stdout(io.StringIO()):
            main.setup_cluster_state()
        nodes = main.cluster_store.list_nodes()
        self.assertTrue(any(
            node['node_id'] == 'master' and node['max_tunnels'] == 2
            for node in nodes
        ))

    def test_master_worker_disabled_disables_existing_local_node(self):
        fd, db_path = tempfile.mkstemp()
        os.close(fd)
        config_dir = tempfile.mkdtemp()

        enabled_main = load_main_module({
            'LOCALSHARE_ROLE': 'master',
            'REMOTE_STATE_DB': db_path,
        })
        enabled_main.config_dir = config_dir
        enabled_main.server_name = 'remote.example.com'
        enabled_main.server_port = 1022
        enabled_main.https = True
        with contextlib.redirect_stdout(io.StringIO()):
            enabled_main.setup_cluster_state()

        disabled_main = load_main_module({
            'LOCALSHARE_ROLE': 'master',
            'REMOTE_STATE_DB': db_path,
            'MASTER_WORKER_ENABLED': 'false',
        })
        disabled_main.config_dir = config_dir
        disabled_main.server_name = 'remote.example.com'
        disabled_main.server_port = 1022
        disabled_main.https = True
        with contextlib.redirect_stdout(io.StringIO()):
            disabled_main.setup_cluster_state()
        node = disabled_main.cluster_store.get_node('master')
        self.assertEqual(0, node['enabled'])
        self.assertEqual(1, node['maintenance'])
        self.assertEqual(0, node['max_tunnels'])

    def test_registration_token_is_limited_to_registration(self):
        main = load_main_module({'NODE_REGISTRATION_TOKEN': 'registration-token'})
        fd, db_path = tempfile.mkstemp()
        os.close(fd)
        main.cluster_store = main.ClusterStore(db_path, heartbeat_timeout=30)
        main.cluster_store.upsert_node({
            'node_id': 'node-a',
            'ssh_server': 'node-a.example.com:1022',
            'public_base_url': 'https://node-a.example.com',
            'token': 'node-token',
            'max_tunnels': 10,
            'last_heartbeat': main.now_ts(),
        })

        request = types.SimpleNamespace(headers={'Authorization': 'Bearer registration-token'})
        denied = main.require_node_auth(request, 'node-a')
        self.assertIsNotNone(denied)
        denied_existing = main.require_node_auth(request, 'node-a', allow_registration=True)
        self.assertIsNotNone(denied_existing)
        allowed = main.require_node_auth(request, 'missing-node', allow_registration=True)
        self.assertIsNone(allowed)

    def test_active_route_cannot_be_overwritten_by_other_healthy_node(self):
        main = load_main_module()
        fd, db_path = tempfile.mkstemp()
        os.close(fd)
        store = main.ClusterStore(db_path, heartbeat_timeout=30)
        for node_id in ('node-a', 'node-b'):
            store.upsert_node({
                'node_id': node_id,
                'ssh_server': f'{node_id}.example.com:1022',
                'public_base_url': f'https://{node_id}.example.com',
                'token': f'{node_id}-token',
                'max_tunnels': 10,
                'last_heartbeat': main.now_ts(),
            })

        store.register_route({
            'token': 'abc12345',
            'node_id': 'node-a',
            'target_url': 'https://node-a.example.com/abc12345',
            'public_url': 'https://remote.example.com/abc12345',
            'expires_at': main.now_ts() + 60,
        })
        with self.assertRaises(ValueError):
            store.register_route({
                'token': 'abc12345',
                'node_id': 'node-b',
                'target_url': 'https://node-b.example.com/abc12345',
                'public_url': 'https://remote.example.com/abc12345',
                'expires_at': main.now_ts() + 60,
            })

    def test_expired_route_is_not_sticky(self):
        main = load_main_module()
        fd, db_path = tempfile.mkstemp()
        os.close(fd)
        store = main.ClusterStore(db_path, heartbeat_timeout=30)
        store.upsert_node({
            'node_id': 'node-a',
            'ssh_server': 'node-a.example.com:1022',
            'public_base_url': 'https://node-a.example.com',
            'weight': 1,
            'token': 'node-a-token',
            'max_tunnels': 10,
            'current_tunnels': 9,
            'last_heartbeat': main.now_ts(),
        })
        store.upsert_node({
            'node_id': 'node-b',
            'ssh_server': 'node-b.example.com:1022',
            'public_base_url': 'https://node-b.example.com',
            'weight': 100,
            'token': 'node-b-token',
            'max_tunnels': 10,
            'current_tunnels': 0,
            'last_heartbeat': main.now_ts(),
        })
        store.register_route({
            'token': 'abc12345',
            'node_id': 'node-a',
            'target_url': 'https://node-a.example.com/abc12345',
            'public_url': 'https://remote.example.com/abc12345',
            'expires_at': main.now_ts() - 1,
        })

        node, reason = store.select_node_for_token('abc12345')
        self.assertEqual('node-b', node['node_id'])
        self.assertEqual('weighted_least_connection', reason)

    def test_cleanup_marks_expired_route_before_deleting(self):
        main = load_main_module()
        fd, db_path = tempfile.mkstemp()
        os.close(fd)
        store = main.ClusterStore(db_path, heartbeat_timeout=30)
        store.upsert_node({
            'node_id': 'node-a',
            'ssh_server': 'node-a.example.com:1022',
            'public_base_url': 'https://node-a.example.com',
            'token': 'node-a-token',
            'max_tunnels': 10,
            'last_heartbeat': main.now_ts(),
        })
        store.register_route({
            'token': 'abc12345',
            'node_id': 'node-a',
            'target_url': 'https://node-a.example.com/abc12345',
            'public_url': 'https://remote.example.com/abc12345',
            'expires_at': main.now_ts() - 1,
        })

        store.cleanup_expired_routes()
        route = store.get_route('abc12345')
        self.assertIsNotNone(route)
        self.assertEqual('expired', route['status'])
        self.assertFalse(store.route_active(route))

    def test_redirect_socket_paths_are_unique_and_cleaned(self):
        main = load_main_module()
        main.sock_dir = tempfile.mkdtemp()

        class Conn:
            def __init__(self):
                self.info = {}

            def get_extra_info(self, name, default=None):
                return self.info.get(name, default)

            def set_extra_info(self, **kwargs):
                self.info.update(kwargs)

        conn = Conn()
        first = main.temporary_redirect_sock_path(conn)
        second = main.temporary_redirect_sock_path(conn)
        self.assertNotEqual(first, second)
        open(first, 'w', encoding='utf-8').close()
        open(second, 'w', encoding='utf-8').close()
        main.cleanup_redirect_socks(conn)
        self.assertFalse(os.path.exists(first))
        self.assertFalse(os.path.exists(second))


if __name__ == '__main__':
    unittest.main()
