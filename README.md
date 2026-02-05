# localshare

Expose your local http service to public network with ssh tunnel

## Features

- 🚀 **HTTP/3 Support**: Configure on your external Nginx for faster connections (see [build.md](./build.md))
- 🔗 **Persistent URLs**: Same SSH account always gets the same URL suffix
- 🔒 **Secure**: SSH tunnel based, no need to expose your local service directly
- ⚡ **Easy to use**: Just one command to expose your local service

## Quickstart

```bash
ssh -R /:localhost:80 -p 1022 app.pywebio.online
```

This command will give you an entrypoint, which you can use to access your `http://localhost:80` in public network.
To expose other local http service, change the `localhost:80` part of the command to your local http service address.

**Note**: If you use the same SSH username (8+ characters), you will always get the same URL suffix, making it easy to remember and share.

---

If you want build your own tunnel service, refer to this [doc](./build.md).
f
