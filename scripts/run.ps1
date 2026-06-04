$ReleaseUrl = "https://github.com/r13v/llmgate/releases/download/main"
$PackagePrefix = "llmgate-main"
$Channel = "main"
$AppArgs = @($args)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

$TempDir = $null
$LockHeld = $false
$LockDir = $null
$UpdateError = "unknown update error"

function Write-Status {
	param([Parameter(Mandatory = $true)][string]$Message)
	[Console]::Error.WriteLine($Message)
}

function Fail-Run {
	param([Parameter(Mandatory = $true)][string]$Message)
	Write-Status "llmgate run failed: $Message"
	exit 1
}

function Remove-TempDir {
	if ($script:TempDir -and (Test-Path -LiteralPath $script:TempDir)) {
		Remove-Item -LiteralPath $script:TempDir -Recurse -Force -ErrorAction SilentlyContinue
		$script:TempDir = $null
	}
}

function Release-UpdateLock {
	if ($script:LockHeld -and $script:LockDir) {
		Remove-Item -LiteralPath $script:LockDir -Recurse -Force -ErrorAction SilentlyContinue
		$script:LockHeld = $false
	}
}

function Resolve-RunArch {
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

function Resolve-LocalAppData {
	$localAppData = $env:LOCALAPPDATA
	if (-not $localAppData) {
		$localAppData = [Environment]::GetFolderPath("LocalApplicationData")
	}
	if (-not $localAppData) {
		throw "LOCALAPPDATA is required"
	}
	return $localAppData
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

function Get-Sha256 {
	param([Parameter(Mandatory = $true)][string]$Path)
	return (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

function Test-Sha256Hex {
	param([string]$Value)
	return $Value -match "^[A-Fa-f0-9]{64}$"
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
	return $null
}

function Read-InstallMetadata {
	if (-not (Test-Path -LiteralPath $script:MetadataPath -PathType Leaf)) {
		return $null
	}
	try {
		return Get-Content -LiteralPath $script:MetadataPath -Raw | ConvertFrom-Json
	} catch {
		return $null
	}
}

function Test-InstalledCommand {
	if (-not (Test-Path -LiteralPath $script:InstallPath -PathType Leaf)) {
		return $false
	}
	try {
		$item = Get-Item -LiteralPath $script:InstallPath -Force
		if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
			return $false
		}
	} catch {
		return $false
	}

	$metadata = Read-InstallMetadata
	if (-not $metadata) {
		return $false
	}
	if ("$($metadata.product)" -ne "llmgate") {
		return $false
	}
	if ("$($metadata.channel)" -ne $script:Channel) {
		return $false
	}
	if ("$($metadata.install_path)" -ne $script:InstallPath) {
		return $false
	}
	$expectedBinarySha = "$($metadata.binary_sha256)"
	if (-not (Test-Sha256Hex -Value $expectedBinarySha)) {
		return $false
	}
	$actualBinarySha = Get-Sha256 -Path $script:InstallPath
	return $actualBinarySha -eq $expectedBinarySha.ToLowerInvariant()
}

function Test-InstallPathReplaceable {
	try {
		$item = Get-Item -LiteralPath $script:InstallPath -Force -ErrorAction SilentlyContinue
		if ($item) {
			if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
				return $false
			}
			return Test-InstalledCommand
		}
	} catch {
		return $false
	}
	return $true
}

function Get-InstalledArchiveSha {
	$metadata = Read-InstallMetadata
	if (-not $metadata) {
		return $null
	}
	return "$($metadata.archive_sha256)"
}

function Write-PathHint {
	$pathParts = @()
	if ($env:Path) {
		$pathParts = @($env:Path -split [IO.Path]::PathSeparator)
	}
	if ($pathParts -contains $script:InstallDir) {
		return
	}
	Write-Status "llmgate installed at $script:InstallPath"
	Write-Status "Add $script:InstallDir to PATH to run llmgate directly."
}

function Invoke-InstalledCommand {
	Remove-TempDir
	Write-PathHint
	$appArgs = $script:AppArgs
	& $script:InstallPath @appArgs
	exit $LASTEXITCODE
}

function Invoke-InstalledWithStatus {
	param([string]$Message)

	if (Test-InstalledCommand) {
		if ($Message) {
			Write-Status $Message
		}
		Invoke-InstalledCommand
	}
}

function Enter-UpdateLock {
	for ($i = 0; $i -lt 30; $i++) {
		try {
			New-Item -ItemType Directory -Path $script:LockDir -ErrorAction Stop | Out-Null
			$script:LockHeld = $true
			return $true
		} catch {
			Start-Sleep -Seconds 1
		}
	}
	return $false
}

function Write-InstallMetadata {
	param(
		[Parameter(Mandatory = $true)][string]$ArchiveSha,
		[Parameter(Mandatory = $true)][string]$BinarySha
	)

	$metadata = [ordered]@{
		schema_version = 1
		product = "llmgate"
		channel = $script:Channel
		install_path = $script:InstallPath
		archive_name = $script:ArchiveName
		archive_sha256 = $ArchiveSha
		binary_sha256 = $BinarySha
		installed_at = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
	}
	$json = ($metadata | ConvertTo-Json -Depth 2)
	$tmp = Join-Path $script:StateDir "install.json.$PID"
	$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
	[IO.File]::WriteAllText($tmp, $json + [Environment]::NewLine, $utf8NoBom)
	Move-Item -LiteralPath $tmp -Destination $script:MetadataPath -Force
}

function Install-OrUpdate {
	param([Parameter(Mandatory = $true)][string]$ExpectedArchiveSha)

	$script:UpdateError = "unknown update error"
	if (-not (Test-InstallPathReplaceable)) {
		$script:UpdateError = "canonical install path is not owned by llmgate: $script:InstallPath"
		return $false
	}

	$archivePath = Join-Path $script:TempDir $script:ArchiveName
	$extractDir = Join-Path $script:TempDir "extract"
	$stageBinary = Join-Path $script:InstallDir ".llmgate.$PID.exe"

	Remove-Item -LiteralPath $extractDir, $stageBinary -Recurse -Force -ErrorAction SilentlyContinue
	New-Item -ItemType Directory -Path $extractDir, $script:InstallDir, $script:StateDir -Force | Out-Null

	try {
		Download-File -Uri "$script:ReleaseUrl/$script:ArchiveName" -OutFile $archivePath
	} catch {
		$script:UpdateError = "could not download $script:ArchiveName"
		Remove-Item -LiteralPath $stageBinary -Force -ErrorAction SilentlyContinue
		return $false
	}

	$actualArchiveSha = Get-Sha256 -Path $archivePath
	if ($actualArchiveSha -ne $ExpectedArchiveSha) {
		$script:UpdateError = "checksum mismatch for $script:ArchiveName"
		Remove-Item -LiteralPath $stageBinary -Force -ErrorAction SilentlyContinue
		return $false
	}

	try {
		Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDir -Force
	} catch {
		$script:UpdateError = "could not unpack $script:ArchiveName"
		Remove-Item -LiteralPath $stageBinary -Force -ErrorAction SilentlyContinue
		return $false
	}

	$extractedBinary = Join-Path $extractDir "llmgate.exe"
	if (-not (Test-Path -LiteralPath $extractedBinary -PathType Leaf)) {
		$script:UpdateError = "archive did not contain llmgate.exe"
		Remove-Item -LiteralPath $stageBinary -Force -ErrorAction SilentlyContinue
		return $false
	}

	try {
		Copy-Item -LiteralPath $extractedBinary -Destination $stageBinary -Force
		$binarySha = Get-Sha256 -Path $stageBinary
		if (-not (Test-InstallPathReplaceable)) {
			$script:UpdateError = "canonical install path changed before replacement: $script:InstallPath"
			Remove-Item -LiteralPath $stageBinary -Force -ErrorAction SilentlyContinue
			return $false
		}
		Move-Item -LiteralPath $stageBinary -Destination $script:InstallPath -Force
		Write-InstallMetadata -ArchiveSha $ExpectedArchiveSha -BinarySha $binarySha
		return $true
	} catch {
		$script:UpdateError = "could not replace installed llmgate"
		Remove-Item -LiteralPath $stageBinary -Force -ErrorAction SilentlyContinue
		return $false
	}
}

try {
	try {
		[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
	} catch {
	}

	$archName = Resolve-RunArch
	$localAppData = Resolve-LocalAppData
	$script:ArchiveName = "$PackagePrefix-windows-$archName.zip"
	$script:InstallDir = Join-Path (Join-Path $localAppData "Programs") "llmgate"
	$script:InstallPath = Join-Path $script:InstallDir "llmgate.exe"
	$script:StateDir = Join-Path $localAppData "llmgate"
	$script:MetadataPath = Join-Path $script:StateDir "install.json"
	$script:LockDir = Join-Path $script:StateDir ".lock"

	New-Item -ItemType Directory -Force -Path $script:StateDir | Out-Null

	$script:TempDir = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
	New-Item -ItemType Directory -Path $script:TempDir | Out-Null
	$checksumsPath = Join-Path $script:TempDir "checksums.txt"

	try {
		Download-File -Uri "$ReleaseUrl/checksums.txt" -OutFile $checksumsPath
	} catch {
		Invoke-InstalledWithStatus -Message "Could not check for updates; running installed llmgate."
		Fail-Run "could not check for updates and no valid installed llmgate is available"
	}

	$expectedArchiveSha = Find-ExpectedChecksum -ChecksumsPath $checksumsPath -ArchiveName $script:ArchiveName
	if (-not (Test-Sha256Hex -Value $expectedArchiveSha)) {
		Invoke-InstalledWithStatus -Message "Could not verify latest release; running installed llmgate."
		Fail-Run "checksum entry not found for $script:ArchiveName"
	}

	$currentArchiveSha = Get-InstalledArchiveSha
	if ($currentArchiveSha -eq $expectedArchiveSha -and (Test-InstalledCommand)) {
		Invoke-InstalledCommand
	}

	if (-not (Enter-UpdateLock)) {
		Invoke-InstalledWithStatus -Message "Could not acquire update lock; running installed llmgate."
		Fail-Run "could not acquire update lock and no valid installed llmgate is available"
	}

	$currentArchiveSha = Get-InstalledArchiveSha
	if ($currentArchiveSha -eq $expectedArchiveSha -and (Test-InstalledCommand)) {
		Release-UpdateLock
		Invoke-InstalledCommand
	}

	if (Test-InstalledCommand) {
		Write-Status "Updating llmgate..."
	} else {
		Write-Status "Downloading llmgate..."
	}

	if (-not (Install-OrUpdate -ExpectedArchiveSha $expectedArchiveSha)) {
		Release-UpdateLock
		Invoke-InstalledWithStatus -Message "Could not update llmgate; running installed llmgate."
		Fail-Run "could not update llmgate: $script:UpdateError"
	}

	Release-UpdateLock

	if (Test-InstalledCommand) {
		Invoke-InstalledCommand
	}

	Fail-Run "installed llmgate could not be verified"
} catch {
	Release-UpdateLock
	Remove-TempDir
	Fail-Run $_.Exception.Message
}
