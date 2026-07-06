#!/bin/bash

if [[ -z "${APP_SERVER_NAME}" ]]; then
  echo "APP_SERVER_NAME env is required!"
  exit 1
fi

ulimit -n "${NOFILE_LIMIT:-200000}" 2>/dev/null || true

if [[ -f /config/cert.pem && -f /config/cert.key ]]; then
  python /localshare/docker/nginx_conf_gen.py --cert-dir=/config "${APP_SERVER_NAME}" > /etc/nginx/conf.d/localshare.conf
  HTTPS_ARGS="--https"
else
  python /localshare/docker/nginx_conf_gen.py "${APP_SERVER_NAME}" > /etc/nginx/conf.d/localshare.conf
  HTTPS_ARGS=""
fi

nginx &

exec python /localshare/main.py --config-dir=/config ${HTTPS_ARGS} "${APP_SERVER_NAME}"
