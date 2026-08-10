from jinja2 import Template
from os import path
import argparse

parser = argparse.ArgumentParser()
parser.add_argument("--cert-dir", type=str,
                    help='The dir to the cert files("cert.pem" and "cert.key"). Provide this argument to enable HTTPS.')
parser.add_argument("--socket-dir", type=str, default='/tmp/localshare', help='The dir to save the socket files')
parser.add_argument("--worker-connections", type=int, default=8192,
                    help='The max number of worker connections (worker_connections)')
parser.add_argument("--worker-rlimit-nofile", type=int, default=65536,
                    help='The max file descriptor limit for nginx worker processes')
parser.add_argument("server_name", type=str, help='The domain name of the server')
args = parser.parse_args()

here_dir = path.dirname(path.abspath(__file__))
tpl_path = path.join(here_dir, 'nginx.conf.tpl')
tpl = Template(open(tpl_path, encoding='utf-8').read())

cert_dir = args.cert_dir.rstrip('/') if args.cert_dir else None
socket_dir = args.socket_dir.rstrip('/')
conf = tpl.render(
    cert_dir=cert_dir,
    socket_dir=socket_dir,
    server_name=args.server_name,
    worker_connections=args.worker_connections,
    worker_rlimit_nofile=args.worker_rlimit_nofile,
)
print(conf)
