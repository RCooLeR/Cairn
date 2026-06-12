# Cairn Platform Providers

## 1. Provider strategy

Cairn should provide one UI and one core backend, but different platform providers.

```text
Windows -> WSL Ubuntu provider
Linux   -> Native Docker provider
macOS   -> Colima/existing context provider
Any OS  -> Existing Docker context provider
Any OS  -> Remote SSH Docker provider
```

Each provider must implement:

```text
Detect
Install
Start
Stop
Restart
DockerHost
DockerContext
RunDocker
RunCompose
OpenHostTerminal
OpenContainerTerminal
MapPathToBackend
MapPathToHost
```

---

## 2. Windows WSL Ubuntu provider

### 2.1 Goals

Use Ubuntu on WSL2 as the Linux backend and install official Docker packages inside it.

Cairn should not manage a custom VM runtime on Windows.

### 2.2 Detection checks

```text
Is WSL installed?
Is WSL2 supported?
Does Ubuntu exist?
Is Ubuntu running as WSL2?
Is systemd available?
Is Docker installed inside Ubuntu?
Is Docker service running?
Is docker compose available?
Can docker run a test container?
```

### 2.3 Install flow

```text
1. Check WSL availability.
2. Install WSL if missing, with user confirmation.
3. Check for Ubuntu distro.
4. Install Ubuntu if missing, with user confirmation.
5. Verify Ubuntu uses WSL2.
6. Enable/verify systemd.
7. Install Docker official apt repository.
8. Install Docker Engine, CLI, Compose plugin, Buildx plugin.
9. Enable and start Docker service.
10. Verify docker version and docker compose version.
```

### 2.4 Command execution

Docker commands can be executed as:

```text
wsl.exe -d Ubuntu -- docker <args>
wsl.exe -d Ubuntu -- docker compose <args>
```

For paths:

```text
C:\Users\dev\project -> /mnt/c/Users/dev/project
```

For best performance, recommend WSL-native project paths:

```text
/home/dev/projects/my-project
```

### 2.5 Known edge cases

```text
Ubuntu not initialized yet
Multiple Ubuntu distros installed
User has no sudo password configured
systemd not enabled
Docker group membership requires WSL restart
Windows path performance problems
VPN/firewall issues with published ports
```

---

## 3. Linux native provider

### 3.1 Goals

Use official Docker Engine packages installed natively on Linux.

### 3.2 Initial distro support

```text
Ubuntu
Debian
```

### 3.3 Future distro support

```text
Fedora
Arch
openSUSE
Rocky Linux
AlmaLinux
```

### 3.4 Detection checks

```text
Is docker CLI available?
Is dockerd running?
Is Docker socket reachable?
Is docker compose available?
Is current user allowed to access Docker?
Is systemd available?
Can Docker run a test container?
```

### 3.5 Install packages

For Ubuntu/Debian style systems:

```text
docker-ce
docker-ce-cli
containerd.io
docker-buildx-plugin
docker-compose-plugin
```

### 3.6 Permission modes

```text
Use sudo for Docker actions
Add user to docker group after warning
Use rootless Docker if already configured
```

The app should not silently add the user to the Docker group.

---

## 4. macOS provider

### 4.1 Goals

Use an existing Docker-compatible backend on macOS. Default to Colima for open-source install flow.

Cairn should not build a custom macOS Linux VM runtime in v1.

### 4.2 Detection checks

```text
Is Homebrew installed?
Is docker CLI installed?
Is Docker Compose available?
Is Colima installed?
Is Colima running?
Is Docker context set to Colima?
Can Docker daemon be reached?
Can Docker run a test container?
```

### 4.3 Install flow

```text
1. Check for Homebrew.
2. Install or guide user to install Homebrew if missing.
3. Install docker CLI if missing.
4. Install docker-compose support if missing.
5. Install Colima if missing.
6. Start Colima.
7. Select/use Colima Docker context.
8. Verify docker and docker compose.
```

### 4.4 Supported existing backends

Cairn should be able to connect to existing contexts from:

```text
Docker Desktop
Colima
OrbStack
Rancher Desktop
Remote Docker hosts
```

Cairn should not force a user to switch if their current Docker context already works.

---

## 5. Existing Docker context provider

This provider is available on every platform.

Use cases:

```text
User already has Docker installed
User uses Docker Desktop but wants Cairn UI
User uses Colima/OrbStack/Rancher Desktop
User uses a remote Docker context
```

Responsibilities:

```text
List Docker contexts
Ping selected context
Use selected context for Docker API
Run compose commands against selected context
Store selected context as provider setting
```

---

## 6. Remote SSH provider

Use Docker contexts for remote hosts.

Features:

```text
Add remote host
Validate SSH access
Create Docker context
Manage containers/projects remotely
Show remote provider status
```

Out of scope for first MVP, but valuable for home-lab users.
