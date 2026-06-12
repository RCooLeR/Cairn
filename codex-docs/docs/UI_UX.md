# Cairn UI/UX Specification

## 1. Design goals

Cairn should feel:

```text
Clean
Fast
Modern
Calm
Technical
Trustworthy
Project-first
```

The app should not feel like an overloaded enterprise platform. It should feel like a polished local control center.

---

## 2. Navigation

Primary sidebar:

```text
Dashboard
Projects
Containers
Images
Volumes
Networks
Logs
Terminal
Updates
Settings
```

Footer/status area:

```text
Active provider
Docker status
Current context
Resource activity
Update check status
```

---

## 3. Dashboard

### Purpose

Give users immediate situational awareness.

### Layout

Top status row:

```text
Docker status
Provider status
Running containers
Projects
Updates available
Disk usage
```

Main content:

```text
Resource charts
Project health grid
Top containers
Recent Docker events
Quick actions
```

### Quick actions

```text
Open terminal
Check updates
Import project
Restart Docker
Prune unused resources
```

---

## 4. Projects page

### Project cards

Each project card should show:

```text
Project name
Running/total services
Health
CPU
RAM
Network
Update badge
Published ports
Working directory
```

### Project actions

```text
Start
Stop
Restart
Redeploy
Pull images
Logs
Terminal
Open folder
```

### Filters

```text
All
Running
Stopped
Unhealthy
Updates available
High CPU
Recently changed
```

---

## 5. Project detail

Tabs:

```text
Overview
Services
Containers
Logs
Stats
Compose
Environment
Volumes
Networks
Updates
Backups
Events
```

### Overview tab

```text
Project health summary
Service cards
Resource chart
Published ports
Recent logs
Update status
```

### Services tab

```text
Service name
Image
Scale
Status
Health
CPU
RAM
Ports
Actions
```

### Compose tab

```text
Raw compose file viewer
Resolved docker compose config output
Environment file viewer
Validation result
```

---

## 6. Containers page

Table columns:

```text
Name
Status
Project
Service
Image
Ports
CPU
Memory
Network
Uptime
Health
Actions
```

Row actions:

```text
Logs
Terminal
Restart
Stop
Inspect
Remove
```

Use strong confirmation for remove and kill actions.

---

## 7. Logs page

Modes:

```text
All logs
Project logs
Service logs
Container logs
```

Controls:

```text
Search
Filter by container
Filter by level
Follow tail
Pause/resume
Timestamp toggle
Export
```

The log viewer should be fast with large streams and should not freeze the UI.

---

## 8. Terminal page

Modes:

```text
Host terminal
Backend terminal
Project terminal
Container terminal
```

Side panel:

```text
Docker command cheatsheet
Recent commands
Saved snippets
Risk labels
Copy command
Run command
```

Risk labels:

```text
Safe
Needs confirmation
Destructive
Dangerous
```

---

## 9. Updates page

Purpose: safe image update control.

Cards/table rows show:

```text
Project
Service
Current image
Current digest
Remote digest
Update status
Last checked
Action buttons
```

Confirmation modal should show:

```text
What will change
Commands that will run
Backup option
Health check option
Rollback option
```

---

## 10. Visual style

Recommended visual direction:

```text
Dark-first, light mode optional
Soft panels/cards
Subtle borders
Rounded corners
Clear typography
Charts with restrained colors
Badges for status and health
```

Status indicators:

```text
Green: healthy/running/up-to-date
Yellow: warning/checking/update available
Red: failed/unhealthy/error
Gray: stopped/unknown
Blue: informational/actionable
```

---

## 11. Empty states

Every major page should have a useful empty state:

```text
No Docker detected -> Set up provider
No projects detected -> Import compose project
No containers -> Run hello-world / import project
No updates -> All images up to date
No logs -> Start a container or select another target
```

---

## 12. Trust UX

Cairn should show real commands before major operations:

```text
docker compose pull api
docker compose up -d api
```

For destructive operations, confirmation text should be specific:

```text
Delete volume "postgres_data"?
This can permanently remove database files.
Type the volume name to confirm.
```


---

## Image lineage and base image update UI

Cairn should expose base image tracking as a visible workflow, not a hidden implementation detail.

### Project cards

Project cards should show separate update badges:

```text
Image update
Base update
Rebuild needed
Pinned digest
Unknown base
```

Example:

```text
apps
5 / 5 services
Running
2 image updates · 1 base update · 1 rebuild needed
```

### Updates page

Add filters:

```text
All
Image updates
Base image updates
Rebuild required
Pinned images
Unknown base
Ignored
Errors
```

Table columns:

```text
Project
Service
Container
Update type
Current image
Base image
Local digest
Remote digest
Confidence
Recommended action
```

### Container detail page

Add an **Image Lineage** card:

```text
Running image
Service image digest
Dockerfile path
Base image
Base digest at build
Current remote base digest
Status
Recommended action
```

### Project detail Updates tab

Group updates by command type:

```text
Pull & recreate
Rebuild & redeploy
Manual attention required
```

Cairn should clearly explain whether the action will run `docker compose pull` or `docker compose build --pull`.
