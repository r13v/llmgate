[CmdletBinding()]
param(
	[switch]$DryRun
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

$ReleaseUrl = if ($env:LLMGATE_RELEASE_URL) {
	$env:LLMGATE_RELEASE_URL.TrimEnd("/")
} else {
	"https://github.com/r13v/llmgate/releases/download/main"
}
$PackagePrefix = if ($env:LLMGATE_PACKAGE_PREFIX) { $env:LLMGATE_PACKAGE_PREFIX } else { "llmgate-main" }

function Resolve-InstallOS {
	if ($env:LLMGATE_OS) {
		$os = $env:LLMGATE_OS.ToLowerInvariant()
		if ($os -ne "windows") {
			throw "unsupported LLMGATE_OS: $($env:LLMGATE_OS)"
		}
		return $os
	}
	return "windows"
}

function Resolve-InstallArch {
	if ($env:LLMGATE_ARCH) {
		$arch = $env:LLMGATE_ARCH.ToLowerInvariant()
		if ($arch -eq "amd64" -or $arch -eq "arm64") {
			return $arch
		}
		throw "unsupported LLMGATE_ARCH: $($env:LLMGATE_ARCH)"
	}

	$archName = $env:PROCESSOR_ARCHITECTURE
	try {
		$archName = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
	} catch {
	}

	switch -Regex ($archName) {
		"^(X64|AMD64)$" { return "amd64" }
		"^(Arm64|ARM64|AARCH64)$" { return "arm64" }
		default { throw "unsupported architecture: $archName" }
	}
}

function Resolve-InstallDir {
	if ($env:LLMGATE_INSTALL_DIR) {
		return $env:LLMGATE_INSTALL_DIR
	}

	$localAppData = $env:LOCALAPPDATA
	if (-not $localAppData) {
		$localAppData = [Environment]::GetFolderPath("LocalApplicationData")
	}
	if (-not $localAppData) {
		throw "LOCALAPPDATA is required"
	}

	return Join-Path (Join-Path (Join-Path $localAppData "Programs") "llmgate") "bin"
}

function Download-File {
	param(
		[Parameter(Mandatory = $true)][string]$Uri,
		[Parameter(Mandatory = $true)][string]$OutFile
	)

	$params = @{
		Uri = $Uri
		OutFile = $OutFile
	}
	if ((Get-Command Invoke-WebRequest).Parameters.ContainsKey("UseBasicParsing")) {
		$params.UseBasicParsing = $true
	}
	Invoke-WebRequest @params
}

function Find-ExpectedChecksum {
	param(
		[Parameter(Mandatory = $true)][string]$ChecksumsPath,
		[Parameter(Mandatory = $true)][string]$ArchiveName
	)

	$escapedArchive = [Regex]::Escape($ArchiveName)
	foreach ($line in Get-Content -LiteralPath $ChecksumsPath) {
		if ($line -match "^\s*([A-Fa-f0-9]{64})\s+$escapedArchive\s*$") {
			return $Matches[1].ToLowerInvariant()
		}
	}
	throw "checksum entry not found for $ArchiveName"
}

function Add-ToUserPath {
	param(
		[Parameter(Mandatory = $true)][string]$InstallDir
	)

	$currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
	$trimmedInstallDir = $InstallDir.TrimEnd([char[]]@("\", "/"))
	$parts = @()
	if ($currentPath) {
		$parts = $currentPath -split ";" | Where-Object { $_ }
	}

	foreach ($part in $parts) {
		if ([string]::Equals($part.TrimEnd([char[]]@("\", "/")), $trimmedInstallDir, [StringComparison]::OrdinalIgnoreCase)) {
			return
		}
	}

	$newPath = if ($currentPath) { "$currentPath;$InstallDir" } else { $InstallDir }
	[Environment]::SetEnvironmentVariable("Path", $newPath, "User")
	$env:Path = "$env:Path;$InstallDir"
	Write-Host "Added $InstallDir to the user PATH. Open a new terminal before running llmgate."
}

try {
	try {
		[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
	} catch {
	}

	$osName = Resolve-InstallOS
	$archName = Resolve-InstallArch
	$installDir = Resolve-InstallDir
	$archiveName = "$PackagePrefix-$osName-$archName.zip"

	if ($DryRun) {
		Write-Host "dry_run=1"
		Write-Host "release_url=$ReleaseUrl"
		Write-Host "archive=$archiveName"
		Write-Host "install_dir=$installDir"
		Write-Host "add_to_path=$($env:LLMGATE_ADD_TO_PATH -eq '1')"
		exit 0
	}

	$tempDir = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
	New-Item -ItemType Directory -Path $tempDir | Out-Null
	try {
		$checksumsPath = Join-Path $tempDir "checksums.txt"
		$archivePath = Join-Path $tempDir $archiveName

		Write-Host "Downloading checksums from $ReleaseUrl/checksums.txt"
		Download-File -Uri "$ReleaseUrl/checksums.txt" -OutFile $checksumsPath

		Write-Host "Downloading $archiveName"
		Download-File -Uri "$ReleaseUrl/$archiveName" -OutFile $archivePath

		$expectedHash = Find-ExpectedChecksum -ChecksumsPath $checksumsPath -ArchiveName $archiveName
		$actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
		if ($actualHash -ne $expectedHash) {
			throw "checksum mismatch for $archiveName"
		}

		Expand-Archive -LiteralPath $archivePath -DestinationPath $tempDir -Force
		$binaryPath = Join-Path $tempDir "llmgate.exe"
		if (-not (Test-Path -LiteralPath $binaryPath -PathType Leaf)) {
			throw "archive did not contain llmgate.exe"
		}

		New-Item -ItemType Directory -Force -Path $installDir | Out-Null
		$targetPath = Join-Path $installDir "llmgate.exe"
		Copy-Item -LiteralPath $binaryPath -Destination $targetPath -Force

		Write-Host "llmgate installed to $targetPath"
		if ($env:LLMGATE_ADD_TO_PATH -eq "1") {
			Add-ToUserPath -InstallDir $installDir
		} else {
			Write-Host "Set LLMGATE_ADD_TO_PATH=1 before running the installer to add llmgate to the user PATH."
		}
	} finally {
		Remove-Item -LiteralPath $tempDir -Recurse -Force -ErrorAction SilentlyContinue
	}
} catch {
	Write-Error "llmgate install failed: $($_.Exception.Message)"
	exit 1
}
