# Compila os binários da release em dist/ (CGO_ENABLED=0, versão embutida) e grava SHA256SUMS.
# Uso: .\scripts\release.ps1 -Versao 1.0.0
param([Parameter(Mandatory = $true)][string]$Versao)
$ErrorActionPreference = 'Stop'
if ($Versao -notmatch '^\d+\.\d+\.\d+$') { throw "versão precisa ser semver (ex.: 1.0.0): $Versao" }
$raiz = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $raiz 'dist'
New-Item -ItemType Directory -Force $dist | Out-Null
$alvos = @(
  @{ os = 'windows'; arch = 'amd64'; nome = 'central-windows-amd64.exe' },
  @{ os = 'linux';   arch = 'amd64'; nome = 'central-linux-amd64' },
  @{ os = 'darwin';  arch = 'amd64'; nome = 'central-darwin-amd64' },
  @{ os = 'darwin';  arch = 'arm64'; nome = 'central-darwin-arm64' }
)
$ldflags = "-s -w -X github.com/JoaoVitorSantDrade/central/internal/version.Versao=$Versao"
$env:CGO_ENABLED = '0'
$somas = @()
foreach ($a in $alvos) {
  $env:GOOS = $a.os; $env:GOARCH = $a.arch
  $saida = Join-Path $dist $a.nome
  & go build -trimpath -ldflags $ldflags -o $saida ./cmd/central
  if ($LASTEXITCODE -ne 0) { throw "build $($a.os)/$($a.arch) falhou" }
  $h = (Get-FileHash -Algorithm SHA256 $saida).Hash.ToLower()
  $somas += "$h  $($a.nome)"
  Write-Output "$($a.nome)  $([math]::Round((Get-Item $saida).Length / 1MB, 1)) MB"
}
Remove-Item Env:GOOS, Env:GOARCH
foreach ($s in 'install.ps1', 'install.sh') {
  Copy-Item (Join-Path $PSScriptRoot $s) (Join-Path $dist $s) -Force
  $somas += "$((Get-FileHash -Algorithm SHA256 (Join-Path $dist $s)).Hash.ToLower())  $s"
}
[IO.File]::WriteAllText((Join-Path $dist 'SHA256SUMS'), ($somas -join "`n") + "`n")
Write-Output "SHA256SUMS gravado em $dist"
