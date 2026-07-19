# localshare

Expose your local HTTP service to the public network with an SSH reverse tunnel.

This version rewrites the runtime in Go. The public client experience stays the
same, while the server now uses PostgreSQL + Ent for persistent cluster state.

## Features

- 🚀 **HTTP/3 Support**: Configure on your external Nginx for faster connections (see [build.md](./build.md))
- 🔗 **Persistent URLs**: Same SSH account always gets the same hash-based URL suffix
- 🔒 **Secure**: SSH tunnel based, no need to expose your local service directly
- ⚡ **Easy to use**: Just one command to expose your local service

## Quickstart

```bash
ssh -R /:localhost:80 -p 1022 app.pywebio.online
```

This command will give you an entrypoint, which you can use to access your `http://localhost:80` in public network.
To expose other local http service, change the `localhost:80` part of the command to your local http service address.

**Note**: The URL suffix is generated from the SSH username with a deterministic
SHA-256 hash, so the same username keeps the same URL without migrating old
runtime state.

---

If you want to build your own tunnel service, refer to [build.md](./build.md).
