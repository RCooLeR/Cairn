package providers

import "strings"

const dockerAptSourceListPath = "/etc/apt/sources.list.d/docker.list"

func dockerAptSourceCleanupCommand() string {
	return "rm -f " + dockerAptSourceListPath
}

func dockerAptSourceWriteCommand() string {
	return strings.Join([]string{
		`codename="$(sed -n 's/^VERSION_CODENAME=//p' /etc/os-release 2>/dev/null | tr -d '"' | head -n 1)"`,
		`if [ -z "$codename" ]; then codename="$(sed -n 's/^UBUNTU_CODENAME=//p' /etc/os-release 2>/dev/null | tr -d '"' | head -n 1)"; fi`,
		`if [ -z "$codename" ] && command -v lsb_release >/dev/null 2>&1; then codename="$(lsb_release -cs)"; fi`,
		`if [ -z "$codename" ]; then version_id="$(sed -n 's/^VERSION_ID=//p' /etc/os-release 2>/dev/null | tr -d '"' | head -n 1)"; case "$version_id" in 25.10) codename=questing ;; 25.04) codename=plucky ;; 24.10) codename=oracular ;; 24.04) codename=noble ;; 23.10) codename=mantic ;; 23.04) codename=lunar ;; 22.10) codename=kinetic ;; 22.04) codename=jammy ;; 20.04) codename=focal ;; 18.04) codename=bionic ;; 16.04) codename=xenial ;; esac; fi`,
		`if [ -z "$codename" ]; then echo "Could not determine Ubuntu codename from /etc/os-release or lsb_release." >&2; exit 1; fi`,
		`echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${codename} stable" > /etc/apt/sources.list.d/docker.list`,
	}, " && ")
}
