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

function Resolve-CacheDir {
	param([Parameter(Mandatory = $true)][string]$ArchName)

	$localAppData = $env:LOCALAPPDATA
	if (-not $localAppData) {
		$localAppData = [Environment]::GetFolderPath("LocalApplicationData")
	}
	if (-not $localAppData) {
		throw "LOCALAPPDATA is required"
	}

	return Join-Path (Join-Path (Join-Path (Join-Path $localAppData "llmgate") "cache") $script:Channel) "windows-$ArchName"
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

function Read-FirstWord {
	param([Parameter(Mandatory = $true)][string]$Path)
	if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
		return $null
	}
	$line = Get-Content -LiteralPath $Path -TotalCount 1
	if (-not $line) {
		return $null
	}
	return ($line -split "\s+")[0]
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

function Read-CurrentSha {
	return Read-FirstWord -Path $script:CurrentPath
}

function Test-CacheEntry {
	param([string]$ArchiveSha)

	if (-not (Test-Sha256Hex -Value $ArchiveSha)) {
		return $false
	}

	$entryDir = Join-Path $script:CacheDir $ArchiveSha
	$binaryPath = Join-Path $entryDir "llmgate.exe"
	$archiveShaPath = Join-Path $entryDir "archive.sha256"
	$binaryShaPath = Join-Path $entryDir "binary.sha256"

	if (-not (Test-Path -LiteralPath $binaryPath -PathType Leaf)) {
		return $false
	}
	if (-not (Test-Path -LiteralPath $archiveShaPath -PathType Leaf)) {
		return $false
	}
	if (-not (Test-Path -LiteralPath $binaryShaPath -PathType Leaf)) {
		return $false
	}

	$storedArchiveSha = Read-FirstWord -Path $archiveShaPath
	if ($storedArchiveSha -ne $ArchiveSha) {
		return $false
	}

	$expectedBinarySha = Read-FirstWord -Path $binaryShaPath
	if (-not (Test-Sha256Hex -Value $expectedBinarySha)) {
		return $false
	}

	$actualBinarySha = Get-Sha256 -Path $binaryPath
	return $actualBinarySha -eq $expectedBinarySha
}

function Test-CurrentCache {
	$currentSha = Read-CurrentSha
	return (Test-CacheEntry -ArchiveSha $currentSha)
}

function Invoke-CacheEntry {
	param([Parameter(Mandatory = $true)][string]$ArchiveSha)

	$binaryPath = Join-Path (Join-Path $script:CacheDir $ArchiveSha) "llmgate.exe"
	Remove-TempDir
	$appArgs = $script:AppArgs
	& $binaryPath @appArgs
	exit $LASTEXITCODE
}

function Invoke-CurrentCacheWithStatus {
	param([string]$Message)

	$currentSha = Read-CurrentSha
	if (Test-CacheEntry -ArchiveSha $currentSha) {
		if ($Message) {
			Write-Status $Message
		}
		Invoke-CacheEntry -ArchiveSha $currentSha
	}
	return $false
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

function Enable-ExecutableWhenNeeded {
	param([Parameter(Mandatory = $true)][string]$Path)

	try {
		if (-not [System.Runtime.InteropServices.RuntimeInformation]::IsOSPlatform([System.Runtime.InteropServices.OSPlatform]::Windows)) {
			$chmod = Get-Command chmod -ErrorAction SilentlyContinue
			if ($chmod) {
				& $chmod 0755 $Path
			}
		}
	} catch {
	}
}

function Update-Cache {
	param([Parameter(Mandatory = $true)][string]$ExpectedArchiveSha)

	$script:UpdateError = "unknown update error"
	$archivePath = Join-Path $script:TempDir $script:ArchiveName
	$extractDir = Join-Path $script:TempDir "extract"
	$stageDir = Join-Path $script:CacheDir ".stage-$ExpectedArchiveSha-$PID"
	$entryDir = Join-Path $script:CacheDir $ExpectedArchiveSha

	Remove-Item -LiteralPath $extractDir, $stageDir -Recurse -Force -ErrorAction SilentlyContinue
	New-Item -ItemType Directory -Path $extractDir, $stageDir -Force | Out-Null

	try {
		Download-File -Uri "$script:ReleaseUrl/$script:ArchiveName" -OutFile $archivePath
	} catch {
		$script:UpdateError = "could not download $script:ArchiveName"
		Remove-Item -LiteralPath $stageDir -Recurse -Force -ErrorAction SilentlyContinue
		return $false
	}

	$actualArchiveSha = Get-Sha256 -Path $archivePath
	if ($actualArchiveSha -ne $ExpectedArchiveSha) {
		$script:UpdateError = "checksum mismatch for $script:ArchiveName"
		Remove-Item -LiteralPath $stageDir -Recurse -Force -ErrorAction SilentlyContinue
		return $false
	}

	try {
		Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDir -Force
	} catch {
		$script:UpdateError = "could not unpack $script:ArchiveName"
		Remove-Item -LiteralPath $stageDir -Recurse -Force -ErrorAction SilentlyContinue
		return $false
	}

	$extractedBinary = Join-Path $extractDir "llmgate.exe"
	if (-not (Test-Path -LiteralPath $extractedBinary -PathType Leaf)) {
		$script:UpdateError = "archive did not contain llmgate.exe"
		Remove-Item -LiteralPath $stageDir -Recurse -Force -ErrorAction SilentlyContinue
		return $false
	}

	Enable-ExecutableWhenNeeded -Path $extractedBinary
	$binarySha = Get-Sha256 -Path $extractedBinary

	try {
		Copy-Item -LiteralPath $extractedBinary -Destination (Join-Path $stageDir "llmgate.exe") -Force
		Enable-ExecutableWhenNeeded -Path (Join-Path $stageDir "llmgate.exe")
		Set-Content -LiteralPath (Join-Path $stageDir "archive.sha256") -Value "$ExpectedArchiveSha  $script:ArchiveName" -Encoding ASCII
		Set-Content -LiteralPath (Join-Path $stageDir "binary.sha256") -Value "$binarySha  llmgate.exe" -Encoding ASCII

		Remove-Item -LiteralPath $entryDir -Recurse -Force -ErrorAction SilentlyContinue
		Move-Item -LiteralPath $stageDir -Destination $entryDir

		$currentTmp = Join-Path $script:CacheDir ".current.$PID"
		Set-Content -LiteralPath $currentTmp -Value $ExpectedArchiveSha -Encoding ASCII
		Move-Item -LiteralPath $currentTmp -Destination $script:CurrentPath -Force
		return $true
	} catch {
		$script:UpdateError = "could not replace cache entry"
		Remove-Item -LiteralPath $stageDir -Recurse -Force -ErrorAction SilentlyContinue
		return $false
	}
}

try {
	try {
		[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
	} catch {
	}

	$archName = Resolve-RunArch
	$script:ArchiveName = "$PackagePrefix-windows-$archName.zip"
	$script:CacheDir = Resolve-CacheDir -ArchName $archName
	$script:CurrentPath = Join-Path $script:CacheDir "current"
	$script:LockDir = Join-Path $script:CacheDir ".lock"

	New-Item -ItemType Directory -Force -Path $script:CacheDir | Out-Null

	$script:TempDir = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
	New-Item -ItemType Directory -Path $script:TempDir | Out-Null
	$checksumsPath = Join-Path $script:TempDir "checksums.txt"

	try {
		Download-File -Uri "$ReleaseUrl/checksums.txt" -OutFile $checksumsPath
	} catch {
		Invoke-CurrentCacheWithStatus -Message "Could not check for updates; running cached llmgate." | Out-Null
		Fail-Run "could not check for updates and no valid cached llmgate is available"
	}

	$expectedArchiveSha = Find-ExpectedChecksum -ChecksumsPath $checksumsPath -ArchiveName $script:ArchiveName
	if (-not (Test-Sha256Hex -Value $expectedArchiveSha)) {
		Invoke-CurrentCacheWithStatus -Message "Could not verify latest release; running cached llmgate." | Out-Null
		Fail-Run "checksum entry not found for $script:ArchiveName"
	}

	$currentSha = Read-CurrentSha
	if ($currentSha -eq $expectedArchiveSha -and (Test-CacheEntry -ArchiveSha $currentSha)) {
		Invoke-CacheEntry -ArchiveSha $currentSha
	}

	if (-not (Enter-UpdateLock)) {
		Invoke-CurrentCacheWithStatus -Message "Could not acquire update lock; running cached llmgate." | Out-Null
		Fail-Run "could not acquire update lock and no valid cached llmgate is available"
	}

	$currentSha = Read-CurrentSha
	if ($currentSha -eq $expectedArchiveSha -and (Test-CacheEntry -ArchiveSha $currentSha)) {
		Release-UpdateLock
		Invoke-CacheEntry -ArchiveSha $currentSha
	}

	if (Test-CurrentCache) {
		Write-Status "Updating llmgate..."
	} else {
		Write-Status "Downloading llmgate..."
	}

	if (-not (Update-Cache -ExpectedArchiveSha $expectedArchiveSha)) {
		Release-UpdateLock
		Invoke-CurrentCacheWithStatus -Message "Could not update llmgate; running cached llmgate." | Out-Null
		Fail-Run "could not update llmgate: $script:UpdateError"
	}

	Release-UpdateLock

	if (Test-CacheEntry -ArchiveSha $expectedArchiveSha) {
		Invoke-CacheEntry -ArchiveSha $expectedArchiveSha
	}

	Fail-Run "updated cache entry could not be verified"
} catch {
	Release-UpdateLock
	Remove-TempDir
	Fail-Run $_.Exception.Message
}
