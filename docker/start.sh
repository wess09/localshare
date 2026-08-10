#!/bin/bash

if [[ -z "${APP_SERVER_NAME}" ]]; then
  echo "APP_SERVER_NAME env is required!"
  exit 1
fi

ulimit -n "${NOFILE_LIMIT:-200000}" 2>/dev/null || true

# 打印实际生效的 nofile 限制，便于排查 fd 耗尽类问题
echo "nofile soft=$(ulimit -Sn) hard=$(ulimit -Hn)"

if [[ -f /config/cert.pem && -f /config/cert.key ]]; then
  python /localshare/docker/nginx_conf_gen.py --cert-dir=/config "${APP_SERVER_NAME}" > /etc/nginx/nginx.conf
  HTTPS_ARGS="--https"
else
  python /localshare/docker/nginx_conf_gen.py "${APP_SERVER_NAME}" > /etc/nginx/nginx.conf
  HTTPS_ARGS=""
fi

# 移除旧的 server 片段配置，避免与新的完整 nginx.conf 里的 server 块冲突
rm -f /etc/nginx/conf.d/localshare.conf

# 校验生成的主配置，语法错误时立即失败，避免带病启动
nginx -t -c /etc/nginx/nginx.conf || exit 1

nginx &

exec python /localshare/main.py --config-dir=/config ${HTTPS_ARGS} "${APP_SERVER_NAME}"
