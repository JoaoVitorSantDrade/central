# Instala a Central no Windows: baixa o binario da ultima release, confere o SHA256 e roda "central instalar".
# Uso:  irm https://github.com/JoaoVitorSantDrade/central/releases/latest/download/install.ps1 | iex
$ErrorActionPreference = 'Stop'
$repo = 'JoaoVitorSantDrade/central'
$arch = if ([Environment]::Is64BitOperatingSystem) { 'amd64' } else { throw 'A Central precisa de Windows 64 bits.' }
$nome = "central-windows-$arch.exe"
$base = "https://github.com/$repo/releases/latest/download"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("central-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  $exe = Join-Path $tmp 'central.exe'
  Write-Host "Baixando $nome..."
  Invoke-WebRequest -UseBasicParsing "$base/$nome" -OutFile $exe
  $somas = (Invoke-WebRequest -UseBasicParsing "$base/SHA256SUMS").Content
  if ($somas -is [byte[]]) { $somas = [Text.Encoding]::UTF8.GetString($somas) }
  $linha = ($somas -split "`n") | Where-Object { $_ -match "\s$([regex]::Escape($nome))\s*$" } | Select-Object -First 1
  if (-not $linha) { throw "SHA256SUMS nao lista $nome" }
  $esperada = ($linha -split '\s+')[0].ToLower()
  $baixada = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLower()
  if ($esperada -ne $baixada) { throw "SHA256 nao confere ($baixada, esperado $esperada)" }
  & $exe instalar
  if ($LASTEXITCODE -ne 0) { throw "central instalar falhou ($LASTEXITCODE)" }
  Write-Host ''
  Write-Host 'Pronto. Abra um terminal novo e rode: central versao'
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
