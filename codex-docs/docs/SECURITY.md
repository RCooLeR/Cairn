# Cairn Security Specification

## 1. Security principles

Cairn manages Docker, and Docker access can be equivalent to high system privileges. Cairn must be safe by default and transparent about what it runs.

Rules:

```text
Do not expose Docker over unauthenticated TCP by default.
Do not silently add users to docker group.
Do not silently delete containers, images, volumes, or networks.
Do not silently prune resources.
Do not store registry passwords in plaintext.
Do not auto-update containers by default.
Show real commands before major operations.
Write an audit log for meaningful actions.
```

---

## 2. Destructive action policy

Actions that always require confirmation:

```text
Delete volume
Delete running container
Remove image used by containers
Prune images
Prune volumes
Prune system
docker compose down --volumes
Force recreate
Reset provider/backend
Uninstall Docker/backend packages
```

Strong confirmation required:

```text
Delete volume with data
Prune volumes
compose down --volumes
Reset Docker environment
```

Strong confirmation can require typing the target name.

---

## 3. Command transparency

Before operations that shell out to Docker/Compose, show:

```text
Command
Working directory
Provider/context
Expected effect
Risk label
```

Example:

```text
Command:
  docker compose down --volumes

Risk:
  Destructive - removes containers and named volumes for this project.
```

---

## 4. Credentials

Cairn should prefer the existing Docker credential helper where possible.

Do not store:

```text
Plaintext registry passwords
Plaintext SSH private keys
Plaintext tokens
```

Use OS credential storage where available:

```text
Windows Credential Manager
macOS Keychain
Linux Secret Service/libsecret where available
```

---

## 5. Terminal safety

Container terminals can run as root. Cairn should show:

```text
Container name
Shell path
User
Working directory
Risk badge when running as root
```

Do not silently escalate privileges.

---

## 6. Network exposure

Cairn should not configure Docker daemon TCP exposure by default.

Avoid:

```text
tcp://0.0.0.0:2375
```

If the user explicitly configures remote daemon access, show warnings and prefer secure transport.

---

## 7. Audit log

Record:

```text
Timestamp
Action
Provider/context
Target type
Target ID/name
Command run
Result
Exit code
Duration
Error message
```

Do not store full secrets in audit logs.

---

## 8. Update safety

Cairn must not auto-update containers by default.

Before update:

```text
Show current image
Show remote image/digest
Show commands to run
Offer backup
Record rollback metadata
```

After update:

```text
Watch health status
Watch restart loop
Show result
Offer rollback/manual instructions
```
