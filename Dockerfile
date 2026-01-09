FROM python:3.11-slim-bookworm

LABEL org.opencontainers.image.source="https://github.com/wess09/localshare"

WORKDIR /localshare

ADD ./ .

# 安装 Nginx
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    nginx \
    ca-certificates && \
    rm -rf /var/lib/apt/lists/* && \
    ln -sf /dev/stdout /var/log/nginx/access.log && \
    ln -sf /dev/stderr /var/log/nginx/error.log

EXPOSE 1022
EXPOSE 80
EXPOSE 443

RUN pip install --no-cache-dir -r requirements.txt

RUN ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo 'Asia/Shanghai' >/etc/timezone

CMD ["/localshare/docker/start.sh"]