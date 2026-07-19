package transport

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/template"
)

type nginxConfig struct {
	CertDir    string
	SocketDir  string
	Role       string
	ServerName string
}

func RunNginxGen(args []string) error {
	fs := flag.NewFlagSet("gen-nginx", flag.ContinueOnError)
	certDir := fs.String("cert-dir", "", "certificate directory")
	socketDir := fs.String("socket-dir", "/tmp/localshare", "socket directory")
	role := fs.String("role", "standalone", "role")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("server_name is required")
	}
	cfg := nginxConfig{
		CertDir:    strings.TrimRight(*certDir, "/"),
		SocketDir:  strings.TrimRight(*socketDir, "/"),
		Role:       *role,
		ServerName: fs.Arg(0),
	}
	tpl, err := template.New("nginx").Parse(nginxTemplate)
	if err != nil {
		return err
	}
	return tpl.Execute(os.Stdout, cfg)
}

const nginxTemplate = `map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

server {
    server_name {{ .ServerName }};
    listen 80 default_server;
{{- if .CertDir }}
    listen 443 ssl default_server;
    ssl_certificate {{ .CertDir }}/cert.pem;
    ssl_certificate_key {{ .CertDir }}/cert.key;
{{- end }}

    error_page 502 /502.html;
    location = /502.html {
        root /localshare/docker;
        internal;
    }

    location = /signal {
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_pass http://127.0.0.1:8080/signal;
    }

    location ^~ /p2p/ {
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_pass http://127.0.0.1:8080$request_uri;
    }

    location ^~ /admin {
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_pass http://127.0.0.1:8080$request_uri;
    }

    location ^~ /ws/ {
        set $target_sock $cookie_localshare_sock;
        if ($target_sock !~ ^[a-z0-9]+$) {
            return 404;
        }
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_pass http://unix:{{ .SocketDir }}/$target_sock.sock:$request_uri;
    }

    location ^~ /api/ {
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_pass http://127.0.0.1:8080$request_uri;
    }

{{- if eq .Role "master" }}
    location ^~ /__cluster_route__/ {
        internal;
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_pass http://127.0.0.1:8080$uri$is_args$args;
    }
{{- end }}

    location = /__route_unavailable__ {
        internal;
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_pass http://127.0.0.1:8080/__route_unavailable__;
    }

    location ~ ^/(?<sock_name>[a-z0-9]+)(?<rest_uri>/.*)?$ {
        set $target_sock $sock_name;
        set $forward_uri $rest_uri;
        if ($forward_uri = "") {
            set $forward_uri /;
        }
        add_header Set-Cookie "localshare_sock=$target_sock; Path=/; HttpOnly; SameSite=Lax" always;
{{- if eq .Role "master" }}
        proxy_intercept_errors on;
        error_page 502 504 = /__cluster_route__/$sock_name$rest_uri$is_args$args;
{{- else }}
        proxy_intercept_errors on;
        error_page 502 504 = /__route_unavailable__;
{{- end }}
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_pass http://unix:{{ .SocketDir }}/$target_sock.sock:$forward_uri;
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
`
