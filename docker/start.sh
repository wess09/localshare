#!/bin/bash

if [[ -z "${APP_SERVER_NAME}" ]]; then
  echo "APP_SERVER_NAME env is required!"
  exit 1
fi

ulimit -n "${NOFILE_LIMIT:-200000}" 2>/dev/null || true

LOCALSHARE_ROLE="${LOCALSHARE_ROLE:-standalone}"

if [[ -f /config/cert.pem && -f /config/cert.key ]]; then
  /localshare/localshare gen-nginx --role="${LOCALSHARE_ROLE}" --cert-dir=/config "${APP_SERVER_NAME}" > /etc/nginx/conf.d/localshare.conf
  HTTPS_ARGS="--https"
else
  /localshare/localshare gen-nginx --role="${LOCALSHARE_ROLE}" "${APP_SERVER_NAME}" > /etc/nginx/conf.d/localshare.conf
  HTTPS_ARGS=""
fi

nginx &

exec /localshare/localshare --config-dir=/config ${HTTPS_ARGS} "${APP_SERVER_NAME}"
