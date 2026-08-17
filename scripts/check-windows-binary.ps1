param(
  [Parameter(Mandatory = $true)]
  [string]$Path,
  [Parameter(Mandatory = $true)]
  [string]$ExpectedVersion,
  [ValidateSet("amd64", "arm64")]
  [string]$ExpectedArchitecture = "amd64",
  [string]$ExpectedProductName = "Cairn",
  [string]$ExpectedCompanyName = "Cairn",
  [string]$ExpectedIdentityName = "app.cairn.desktop",
  [string]$ExpectedCommit = "",
  [string]$ExpectedBuildDate = ""
)

$ErrorActionPreference = "Stop"

function Normalize-Semver([string]$Value) {
  $normalized = $Value.Trim()
  if ($normalized.StartsWith("v")) {
    $normalized = $normalized.Substring(1)
  }
  if ($normalized -notmatch "^(?<core>\d+\.\d+\.\d+)(?:[+-][0-9A-Za-z.-]+)?$") {
    throw "ExpectedVersion '$Value' must be a semantic version such as 1.2.3 or 1.2.3-rc.1."
  }
  return @{
    Semver = $normalized
    FileVersion = "$($Matches.core).0"
  }
}

function Read-PeMachine([string]$ExecutablePath) {
  $stream = [System.IO.File]::Open(
    $ExecutablePath,
    [System.IO.FileMode]::Open,
    [System.IO.FileAccess]::Read,
    [System.IO.FileShare]::Read
  )
  try {
    $reader = [System.IO.BinaryReader]::new($stream)
    if ($reader.ReadUInt16() -ne 0x5A4D) {
      throw "'$ExecutablePath' does not have a valid DOS/PE header."
    }
    $stream.Position = 0x3C
    $peOffset = $reader.ReadUInt32()
    if ($peOffset -gt ($stream.Length - 6)) {
      throw "'$ExecutablePath' has an invalid PE header offset."
    }
    $stream.Position = $peOffset
    if ($reader.ReadUInt32() -ne 0x00004550) {
      throw "'$ExecutablePath' does not have a valid PE signature."
    }
    return $reader.ReadUInt16()
  } finally {
    $stream.Dispose()
  }
}

if (-not ("Cairn.Build.WindowsResourceReader" -as [type])) {
  Add-Type -TypeDefinition @'
using System;
using System.IO;

namespace Cairn.Build
{
    public static class WindowsResourceReader
    {
        private struct Section
        {
            public uint VirtualSize;
            public uint VirtualAddress;
            public uint RawSize;
            public uint RawAddress;
        }

        public static byte[] ReadFirst(string fileName, int resourceTypeId)
        {
            byte[] data = File.ReadAllBytes(fileName);
            Require(data, 0, 64);
            if (U16(data, 0) != 0x5A4D)
            {
                throw new InvalidDataException("Missing DOS signature.");
            }

            int peOffset = checked((int)U32(data, 0x3C));
            Require(data, peOffset, 24);
            if (U32(data, peOffset) != 0x00004550)
            {
                throw new InvalidDataException("Missing PE signature.");
            }

            int coffOffset = peOffset + 4;
            int sectionCount = U16(data, coffOffset + 2);
            int optionalSize = U16(data, coffOffset + 16);
            int optionalOffset = coffOffset + 20;
            Require(data, optionalOffset, optionalSize);

            int dataDirectoryOffset;
            ushort magic = U16(data, optionalOffset);
            if (magic == 0x10B)
            {
                dataDirectoryOffset = optionalOffset + 96;
            }
            else if (magic == 0x20B)
            {
                dataDirectoryOffset = optionalOffset + 112;
            }
            else
            {
                throw new InvalidDataException("Unsupported PE optional-header magic.");
            }

            // IMAGE_DIRECTORY_ENTRY_RESOURCE is data-directory entry 2.
            Require(data, dataDirectoryOffset + 16, 8);
            uint resourceRva = U32(data, dataDirectoryOffset + 16);
            uint resourceSize = U32(data, dataDirectoryOffset + 20);
            if (resourceRva == 0 || resourceSize == 0)
            {
                return null;
            }

            int sectionOffset = optionalOffset + optionalSize;
            Section[] sections = new Section[sectionCount];
            for (int i = 0; i < sectionCount; i++)
            {
                int current = checked(sectionOffset + (i * 40));
                Require(data, current, 40);
                sections[i].VirtualSize = U32(data, current + 8);
                sections[i].VirtualAddress = U32(data, current + 12);
                sections[i].RawSize = U32(data, current + 16);
                sections[i].RawAddress = U32(data, current + 20);
            }

            int resourceBase = RvaToOffset(resourceRva, sections);
            Require(data, resourceBase, 16);
            int rootEntries = U16(data, resourceBase + 12) +
                U16(data, resourceBase + 14);
            for (int i = 0; i < rootEntries; i++)
            {
                int entry = checked(resourceBase + 16 + (i * 8));
                Require(data, entry, 8);
                uint name = U32(data, entry);
                if ((name & 0x80000000U) != 0 ||
                    (name & 0xFFFFU) != (uint)resourceTypeId)
                {
                    continue;
                }

                uint child = U32(data, entry + 4);
                if ((child & 0x80000000U) == 0)
                {
                    return ReadDataEntry(
                        data,
                        resourceBase,
                        child,
                        sections);
                }
                return ReadDirectory(
                    data,
                    resourceBase,
                    child & 0x7FFFFFFFU,
                    sections,
                    0);
            }
            return null;
        }

        private static byte[] ReadDirectory(
            byte[] data,
            int resourceBase,
            uint directoryRelativeOffset,
            Section[] sections,
            int depth)
        {
            if (depth > 8)
            {
                throw new InvalidDataException("PE resource tree is too deep.");
            }

            int directory = checked(resourceBase +
                (int)directoryRelativeOffset);
            Require(data, directory, 16);
            int entries = U16(data, directory + 12) +
                U16(data, directory + 14);
            for (int i = 0; i < entries; i++)
            {
                int entry = checked(directory + 16 + (i * 8));
                Require(data, entry, 8);
                uint child = U32(data, entry + 4);
                byte[] result;
                if ((child & 0x80000000U) != 0)
                {
                    result = ReadDirectory(
                        data,
                        resourceBase,
                        child & 0x7FFFFFFFU,
                        sections,
                        depth + 1);
                }
                else
                {
                    result = ReadDataEntry(
                        data,
                        resourceBase,
                        child,
                        sections);
                }
                if (result != null)
                {
                    return result;
                }
            }
            return null;
        }

        private static byte[] ReadDataEntry(
            byte[] data,
            int resourceBase,
            uint dataRelativeOffset,
            Section[] sections)
        {
            int entry = checked(resourceBase + (int)dataRelativeOffset);
            Require(data, entry, 16);
            uint dataRva = U32(data, entry);
            int size = checked((int)U32(data, entry + 4));
            if (size == 0)
            {
                return null;
            }
            int dataOffset = RvaToOffset(dataRva, sections);
            Require(data, dataOffset, size);
            byte[] result = new byte[size];
            Buffer.BlockCopy(data, dataOffset, result, 0, size);
            return result;
        }

        private static int RvaToOffset(uint rva, Section[] sections)
        {
            for (int i = 0; i < sections.Length; i++)
            {
                Section section = sections[i];
                uint span = Math.Max(section.VirtualSize, section.RawSize);
                ulong end = (ulong)section.VirtualAddress + span;
                if (rva >= section.VirtualAddress && (ulong)rva < end)
                {
                    return checked((int)(
                        section.RawAddress +
                        (rva - section.VirtualAddress)));
                }
            }
            throw new InvalidDataException(
                "PE resource RVA does not map to a file section.");
        }

        private static ushort U16(byte[] data, int offset)
        {
            Require(data, offset, 2);
            return (ushort)(data[offset] | (data[offset + 1] << 8));
        }

        private static uint U32(byte[] data, int offset)
        {
            Require(data, offset, 4);
            return (uint)(
                data[offset] |
                (data[offset + 1] << 8) |
                (data[offset + 2] << 16) |
                (data[offset + 3] << 24));
        }

        private static void Require(byte[] data, int offset, int size)
        {
            if (offset < 0 || size < 0 ||
                (long)offset + size > data.LongLength)
            {
                throw new InvalidDataException(
                    "PE structure points outside the executable.");
            }
        }
    }
}
'@
}

function Require-Resource(
  [string]$ExecutablePath,
  [int]$TypeId,
  [string]$Description
) {
  $bytes = [Cairn.Build.WindowsResourceReader]::ReadFirst($ExecutablePath, $TypeId)
  if ($null -eq $bytes -or $bytes.Length -eq 0) {
    throw "Windows executable is missing embedded $Description (resource type $TypeId): $ExecutablePath"
  }
  return $bytes
}

function Decode-Manifest([byte[]]$Bytes) {
  if ($Bytes.Length -ge 2 -and $Bytes[0] -eq 0xFF -and $Bytes[1] -eq 0xFE) {
    return [System.Text.Encoding]::Unicode.GetString($Bytes).TrimEnd([char]0)
  }
  if ($Bytes.Length -ge 2 -and $Bytes[0] -eq 0xFE -and $Bytes[1] -eq 0xFF) {
    return [System.Text.Encoding]::BigEndianUnicode.GetString($Bytes).TrimEnd([char]0)
  }
  return [System.Text.Encoding]::UTF8.GetString($Bytes).TrimEnd([char]0)
}

function Read-VersionString(
  [byte[]]$VersionResource,
  [string]$Name
) {
  $decoded = [System.Text.Encoding]::Unicode.GetString($VersionResource)
  $pattern = [regex]::Escape($Name) + "\x00+(?<value>[^\x00]+)\x00"
  $match = [regex]::Match($decoded, $pattern)
  if (!$match.Success) {
    throw "Embedded VersionInfo is missing '$Name'."
  }
  return $match.Groups["value"].Value
}

$resolvedPath = (Resolve-Path -LiteralPath $Path).Path
$version = Normalize-Semver $ExpectedVersion

$expectedMachine = switch ($ExpectedArchitecture) {
  "amd64" { 0x8664 }
  "arm64" { 0xAA64 }
}
$actualMachine = Read-PeMachine $resolvedPath
if ($actualMachine -ne $expectedMachine) {
  throw (
    "Windows executable machine is 0x{0:X4}, want 0x{1:X4} ({2}): {3}" -f
    $actualMachine,
    $expectedMachine,
    $ExpectedArchitecture,
    $resolvedPath
  )
}

# Windows resource type IDs:
# RT_ICON=3, RT_GROUP_ICON=14, RT_VERSION=16, RT_MANIFEST=24.
$null = Require-Resource $resolvedPath 3 "icon image"
$null = Require-Resource $resolvedPath 14 "icon group"
$versionBytes = Require-Resource $resolvedPath 16 "VersionInfo"
$manifestBytes = Require-Resource $resolvedPath 24 "application manifest"

$fileVersion = Read-VersionString $versionBytes "FileVersion"
$productVersion = Read-VersionString $versionBytes "ProductVersion"
$productName = Read-VersionString $versionBytes "ProductName"
$companyName = Read-VersionString $versionBytes "CompanyName"
$fileDescription = Read-VersionString $versionBytes "FileDescription"
if ($fileVersion -cne $version.FileVersion) {
  throw "FileVersion is '$fileVersion', want '$($version.FileVersion)'."
}
if ($productVersion -cne $version.Semver) {
  throw "ProductVersion is '$productVersion', want '$($version.Semver)'."
}
if ($productName -cne $ExpectedProductName) {
  throw "ProductName is '$productName', want '$ExpectedProductName'."
}
if ($companyName -cne $ExpectedCompanyName) {
  throw "CompanyName is '$companyName', want '$ExpectedCompanyName'."
}
if ([string]::IsNullOrWhiteSpace($fileDescription)) {
  throw "FileDescription is missing from the embedded VersionInfo."
}

$manifest = Decode-Manifest $manifestBytes
try {
  $manifestXml = [xml]$manifest
} catch {
  throw "Embedded application manifest is not valid XML: $($_.Exception.Message)"
}

$namespaceManager = [System.Xml.XmlNamespaceManager]::new($manifestXml.NameTable)
$namespaceManager.AddNamespace("asmv1", "urn:schemas-microsoft-com:asm.v1")
$namespaceManager.AddNamespace("asmv3", "urn:schemas-microsoft-com:asm.v3")
$identity = $manifestXml.SelectSingleNode("/asmv1:assembly/asmv1:assemblyIdentity", $namespaceManager)
if ($null -eq $identity) {
  throw "Embedded application manifest is missing assemblyIdentity."
}
if ([string]$identity.name -cne $ExpectedIdentityName) {
  throw "Manifest identity is '$($identity.name)', want '$ExpectedIdentityName'."
}
if ([string]$identity.version -cne $version.FileVersion) {
  throw "Manifest version is '$($identity.version)', want '$($version.FileVersion)'."
}

$executionLevel = $manifestXml.SelectSingleNode(
  "//asmv3:requestedExecutionLevel",
  $namespaceManager
)
if ($null -eq $executionLevel -or [string]$executionLevel.level -cne "asInvoker") {
  throw "Embedded application manifest must request execution level 'asInvoker'."
}

$dpiAwareness = $manifestXml.SelectSingleNode(
  "//*[local-name()='dpiAwareness']",
  $namespaceManager
)
if ($null -eq $dpiAwareness -or $dpiAwareness.InnerText -notmatch "(^|,)permonitorv2(,|$)") {
  throw "Embedded application manifest must declare per-monitor-v2 DPI awareness."
}

& (Join-Path $PSScriptRoot "check-binary-build-metadata.ps1") `
  -Path $resolvedPath `
  -ExpectedCommit $ExpectedCommit `
  -ExpectedBuildDate $ExpectedBuildDate

Write-Host (
  "Windows binary resources passed: {0} ({1}, FileVersion {2}, ProductVersion {3}, icon + manifest embedded)." -f
  $resolvedPath,
  $ExpectedArchitecture,
  $fileVersion,
  $productVersion
)
