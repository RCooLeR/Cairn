param(
  [string]$Root = "",
  [string]$Image = "debian:stable-slim"
)

$ErrorActionPreference = "Stop"

if (![System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Linux)) {
  throw "Debian container deb smoke must run on Linux so Docker bind mounts use Linux paths."
}

if ([string]::IsNullOrWhiteSpace($Root)) {
  $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
  $Root = (Resolve-Path (Join-Path $scriptDir "..")).Path
} else {
  $Root = (Resolve-Path $Root).Path
}

$deb = Get-ChildItem -Path (Join-Path $Root "bin") -Filter "*.deb" -File | Select-Object -First 1
if (!$deb) {
  throw "No Debian package found under bin/."
}

function Test-DockerAccess([string]$File, [string[]]$PrefixArgs) {
  & $File @PrefixArgs "info" *> $null
  return $LASTEXITCODE -eq 0
}

$dockerFile = "docker"
$dockerPrefix = @()
if (!(Test-DockerAccess $dockerFile $dockerPrefix)) {
  $sudo = Get-Command sudo -ErrorAction SilentlyContinue
  if ($sudo -and (Test-DockerAccess "sudo" @("docker"))) {
    $dockerFile = "sudo"
    $dockerPrefix = @("docker")
  } else {
    throw "Docker daemon is not reachable for Debian container deb smoke."
  }
}

function Invoke-Docker([string[]]$Arguments) {
  & $script:dockerFile @script:dockerPrefix @Arguments
  if ($LASTEXITCODE -ne 0) {
    throw "docker failed with exit code $LASTEXITCODE"
  }
}

$containerScript = @'
set -eu

if [ ! -f /etc/debian_version ]; then
  echo "Expected a Debian image." >&2
  exit 1
fi

if [ ! -f "$CAIRN_DEB" ]; then
  echo "Mounted deb package not found: $CAIRN_DEB" >&2
  exit 1
fi

apt-get update
apt-get install -y --no-install-recommends ca-certificates desktop-file-utils shared-mime-info

package_name="$(dpkg-deb --field "$CAIRN_DEB" Package)"
depends="$(dpkg-deb --field "$CAIRN_DEB" Depends || true)"
case "$depends" in
  *docker*|*docker-ce*|*docker.io*|*containerd*)
    echo "Deb package must not depend on Docker packages: $depends" >&2
    exit 1
    ;;
esac

docker_group_before="$(getent group docker || true)"

apt-get install -y --no-install-recommends "$CAIRN_DEB"
status="$(dpkg-query -W -f='${Status} ${Version}' "$package_name")"
case "$status" in
  "install ok installed "*)
    ;;
  *)
    echo "Package status after install was unexpected: $status" >&2
    exit 1
    ;;
esac

test -x /usr/bin/cairn
test -f /usr/share/applications/cairn.desktop
desktop-file-validate /usr/share/applications/cairn.desktop
test -f /usr/share/icons/hicolor/512x512/apps/cairn.png

ldd /usr/bin/cairn >/tmp/cairn-ldd.txt
if grep -q "not found" /tmp/cairn-ldd.txt; then
  cat /tmp/cairn-ldd.txt >&2
  exit 1
fi

docker_group_after_install="$(getent group docker || true)"
if [ "$docker_group_after_install" != "$docker_group_before" ]; then
  echo "Deb install changed the docker group." >&2
  exit 1
fi

apt-get remove -y "$package_name"

if dpkg-query -W -f='${db:Status-Abbrev} ${Version}' "$package_name" >/tmp/cairn-package-state.txt 2>/dev/null; then
  package_state="$(cat /tmp/cairn-package-state.txt)"
  package_status="$(printf '%s' "$package_state" | cut -c 2)"
  case "$package_status" in
    n|c)
      ;;
    *)
      echo "Package still appears active after remove: $package_state" >&2
      exit 1
      ;;
  esac
fi

test ! -e /usr/bin/cairn
test ! -e /usr/share/applications/cairn.desktop
test ! -e /usr/share/icons/hicolor/512x512/apps/cairn.png

docker_group_after_remove="$(getent group docker || true)"
if [ "$docker_group_after_remove" != "$docker_group_before" ]; then
  echo "Deb remove changed the docker group." >&2
  exit 1
fi

echo "Debian container deb install/uninstall smoke passed for $package_name."
'@

Write-Host "Running Debian container deb smoke with $Image and $($deb.Name)."
$mount = "${Root}:/work:ro"
$debInContainer = "/work/bin/$($deb.Name)"
Invoke-Docker @(
  "run",
  "--rm",
  "--pull", "missing",
  "--network", "bridge",
  "-v", $mount,
  "-e", "CAIRN_DEB=$debInContainer",
  $Image,
  "sh", "-ceu", $containerScript
)
