# Cairn Feature Specification

## Priority levels

```text
P0 = required for first usable MVP
P1 = required for v1
P2 = important after v1
P3 = future/nice-to-have
```

---

## P0 features — first usable MVP

### Docker connection

```text
Detect Docker daemon
Detect Docker version
Detect Compose version
Show provider health
Show current Docker context
```

### Containers

```text
List containers
Show status, image, ports, uptime, project/service labels
Start container
Stop container
Restart container
View logs
Open terminal
Inspect container JSON
```

### Images

```text
List images
Show repo/tag/ID/size/created date
Show which containers use image
Pull image
Remove image with confirmation
```

### Volumes

```text
List volumes
Show driver, created date, usage relationship
Inspect volume
Delete volume with confirmation
```

### Networks

```text
List networks
Show driver, subnet, connected containers
Inspect network
```

### Compose project grouping

```text
Group containers by com.docker.compose.project
Group containers by com.docker.compose.service
Show project cards
Show service list
```

### Basic dashboard

```text
Running containers
Stopped containers
Unhealthy containers
Project count
Image count
Volume count
Docker disk usage
```

---

## P1 features — v1 product

### Compose project management

```text
Project start
Project stop
Project restart
Project redeploy
Project pull images
Project logs
Project terminal
Compose config viewer
Import compose project folder
```

### Stats and charts

```text
Live CPU chart
Live memory chart
Network RX/TX chart
Top containers by CPU
Top containers by memory
Project aggregate stats
```

### Updates

```text
Check service image updates
Check base image updates for locally built services
Show separate image/base/rebuild badges
Service-level pull/recreate update
Service-level rebuild/redeploy update
Project-level mixed update plan
Health monitoring after update
Update history
Ignore update
```

### Image lineage

```text
Track image lineage per container
Track image lineage per Compose service
Track image lineage per Compose project
Parse Dockerfile FROM references
Support multi-stage Dockerfiles
Store base image digest history
Show confidence level for lineage results
Explain unknown base image cases
```

### Terminals

```text
Host terminal
Backend terminal
Container terminal
Command cheatsheet
Command palette
Risk labels
```

### Providers

```text
Linux native provider
Windows WSL Ubuntu provider
macOS Colima provider
Existing context provider
```

---

## P2 features — post-v1 improvements

```text
Volume backup/restore
Volume browser
Network topology view
Remote Docker SSH provider
App tray controls
Desktop notifications
Docker disk cleanup wizard
Compose YAML editor
.env editor
Log export
Saved command snippets
```

---

## P3 features — future

```text
Kubernetes view
Registry browser
Team mode
Cloud sync
Plugin system
Advanced alerting
Rules-based automation
```

---

## Differentiators against Docker Desktop

```text
Compose-first project dashboard
Better per-project charts
Better volume backup/restore
Built-in host/backend/container terminal
Command cheatsheet with risk labels
One-click image updates with health monitoring
Clean home-lab/self-hosted workflow
No custom runtime lock-in
```
