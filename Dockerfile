FROM python:3.9-buster

LABEL org.opencontainers.image.source="https://github.com/hajiming/localshare"

WORKDIR /localshare

ADD ./ .

# 直接使用 Debian 官方源安装 Nginx
RUN apt-get update && \
    apt-get install -y nginx && \
    rm -rf /var/lib/apt/lists/* && \
    ln -sf /dev/stdout /var/log/nginx/access.log && \
    ln -sf /dev/stderr /var/log/nginx/error.log

# for ssh
EXPOSE 1022

EXPOSE 80

EXPOSE 443

RUN pip install --no-cache-dir -r requirements.txt

RUN ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
RUN echo 'Asia/Shanghai' >/etc/timezone


CMD ["/localshare/docker/start.sh"]