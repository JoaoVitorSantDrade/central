#!/bin/sh
# Instala a Central no Linux ou no macOS: baixa o binário da última release, confere o SHA256 e roda "central instalar".
# Uso:  curl -fsSL https://github.com/JoaoVitorSantDrade/central/releases/latest/download/install.sh | sh
set -eu
repo="JoaoVitorSantDrade/central"
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "sistema não suportado: $(uname -s) (no Windows, use install.ps1)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "arquitetura não suportada: $(uname -m)" >&2; exit 1 ;;
esac
nome="central-$os-$arch"
base="https://github.com/$repo/releases/latest/download"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
echo "Baixando $nome..."
curl -fsSL "$base/$nome" -o "$tmp/central"
curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS"
esperada="$(awk -v n="$nome" '$2 == n { print $1 }' "$tmp/SHA256SUMS")"
[ -n "$esperada" ] || { echo "SHA256SUMS não lista $nome" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then baixada="$(sha256sum "$tmp/central" | awk '{print $1}')"
else baixada="$(shasum -a 256 "$tmp/central" | awk '{print $1}')"; fi
[ "$esperada" = "$baixada" ] || { echo "SHA256 não confere ($baixada, esperado $esperada)" >&2; exit 1; }
chmod +x "$tmp/central"
"$tmp/central" instalar
echo
echo "Pronto. Rode: central versao"
