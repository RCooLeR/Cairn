package providers

import "strings"

const dockerAptSourceListPath = "/etc/apt/sources.list.d/docker.list"

func dockerAptSourceCleanupCommand() string {
	return "rm -f " + dockerAptSourceListPath
}

func dockerAptSourceWriteCommand() string {
	return strings.Join([]string{
		`. /etc/os-release`,
		`codename="${VERSION_CODENAME:-${UBUNTU_CODENAME:-}}"`,
		`if [ -z "$codename" ]; then echo "Could not determine Ubuntu codename from /etc/os-release." >&2; exit 1; fi`,
		`echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${codename} stable" > /etc/apt/sources.list.d/docker.list`,
	}, " && ")
}
