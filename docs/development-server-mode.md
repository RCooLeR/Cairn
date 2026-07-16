# Development-only server mode

Cairn is a desktop application. Its bound Wails services can control Docker, open terminals, read and edit project files, use registry credentials, and execute destructive operations. They are not an authenticated or authorized remote API.

For that reason, the normal Wails `server` build tag is intentionally blocked, the server-container image is removed, and stable build, run, CI, and release surfaces do not expose server mode. Do not deploy or distribute a server build.

## Explicit local development use

The server transport remains available only for narrowly scoped development or security testing. Use a disposable Docker backend, do not handle real credentials or data, and do not leave the process running. Loopback does not make the transport safe from untrusted local software or browser origins.

An explicit development build requires both build tags and a runtime acknowledgement:

```powershell
$env:CAIRN_ENABLE_UNSAFE_SERVER_DEVELOPMENT = "I_ACKNOWLEDGE_THIS_HAS_NO_AUTHENTICATION"
go run '-tags=server,cairn_server_dev' .
```

```bash
CAIRN_ENABLE_UNSAFE_SERVER_DEVELOPMENT=I_ACKNOWLEDGE_THIS_HAS_NO_AUTHENTICATION \
  go run -tags=server,cairn_server_dev .
```

The application forces `WAILS_SERVER_HOST` to `127.0.0.1` and rejects wildcard or non-loopback values. The acknowledgement is a development tripwire, not authentication.

Running `wails3 update build-assets` may regenerate upstream server tasks and `build/docker/Dockerfile.server`. Remove those generated surfaces before committing; `scripts/check-server-mode-containment.ps1` and CI fail if they return.
