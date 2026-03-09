map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

server {
    server_name {{ server_name }};
    listen 80 default_server;
    {% if cert_dir %}
    listen 443 ssl default_server;

    ssl_certificate   {{ cert_dir }}/cert.pem;
    ssl_certificate_key  {{ cert_dir }}/cert.key;
    {% endif %}

    # 自定义 502 错误页面
    error_page 502 /502.html;
    location = /502.html {
        root /localshare/docker;
        internal;
    }

    # 路径匹配：捕获 /sockname 以及可选的后续路径
    # 使用命名捕获组 (?<name>...) 更安全
    location ~ ^/(?<sock_name>[a-z0-9]+)(?<rest_uri>/.*)?$ {
        # 必须重新设置变量，否则在 proxy_pass 中直接用正则组不安全
        set $target_sock $sock_name;

        # 处理子路径：如果有 /config，就转发 /config；如果没有，转发 /
        set $forward_uri $rest_uri;
        if ($forward_uri = "") {
            set $forward_uri /;
        }

        add_header Set-Cookie "localshare_sock=$target_sock; Path=/; HttpOnly; SameSite=Lax" always;

        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

        # 转发到 Unix Socket
        proxy_pass http://unix:{{ socket_dir }}/$target_sock.sock:$forward_uri;
    }

    location ^~ /pywebio_static/ {
        alias /localshare/pywebio_static/;
    }

    location ^~ /static/ {
        alias /localshare/static/;
    }

    location / {
        return 302 https://yc.nanoda.work;
    }
}