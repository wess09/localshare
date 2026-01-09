map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

server {
    server_name {{ server_name }};
    listen 80;
    {% if cert_dir %}
    listen 443 ssl;

    ssl_certificate   {{ cert_dir }}/cert.pem;
    ssl_certificate_key  {{ cert_dir }}/cert.key;
    {% endif %}

    # 路径模式匹配
    location ~ ^/([a-z0-9]+)(/.*)?$ {
        set $path_suffix $1;
        
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        
        # 转发到对应的 Unix Socket,同时传递子路径
        proxy_pass http://unix:{{ socket_dir }}/${path_suffix}.sock:$2;
    }

    location / {
        return 302 https://github.com/wang0618/localshare;
    }
}