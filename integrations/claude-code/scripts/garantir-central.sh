#!/bin/sh
# Garante a Central instalada para o plugin: se o binário não está na pasta de
# instalação, baixa o da última release, confere o SHA256 e roda
# "central instalar --sem-plugin sim". Com --atualizar, também atualiza um
# binário mais antigo que a versão do plugin. Imprime o caminho do binário no
# stdout; todo o resto vai para o stderr.
#
# Variáveis (para testes): CENTRAL_INSTALAR_EM (pasta de instalação) e
# CENTRAL_RELEASE_BASE (onde baixar; padrão: a última release no GitHub).
set -eu
aqui="$(cd "$(dirname "$0")" && pwd)"
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) so=windows; ext=.exe ;;
  Linux) so=linux; ext= ;;
  Darwin) so=darwin; ext= ;;
  *) echo "central: sistema não suportado: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "central: arquitetura não suportada: $(uname -m)" >&2; exit 1 ;;
esac
if [ -n "${CENTRAL_INSTALAR_EM:-}" ]; then
  pasta="$CENTRAL_INSTALAR_EM"
elif [ "$so" = windows ]; then
  pasta="${LOCALAPPDATA:?LOCALAPPDATA não definida}/Programs/central"
else
  pasta="$HOME/.local/bin"
fi
if [ "$so" = windows ] && command -v cygpath >/dev/null 2>&1; then
  pasta="$(cygpath -u "$pasta")"
fi
alvo="$pasta/central$ext"

versao_de() { "$1" versao 2>/dev/null | sed -n 's/.*"versao":"\([0-9.]*\)".*/\1/p'; }
# menor A B: verdadeiro se a versão A é menor que a B (semver X.Y.Z).
menor() {
  [ "$1" = "$2" ] && return 1
  [ "$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1,1n -k2,2n -k3,3n | head -1)" = "$1" ]
}

instalar() {
  base="${CENTRAL_RELEASE_BASE:-https://github.com/JoaoVitorSantDrade/central/releases/latest/download}"
  nome="central-$so-$arch$ext"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  echo "central: instalando a Central ($nome) para o plugin..." >&2
  curl -fsSL "$base/$nome" -o "$tmp/central$ext"
  curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS"
  esperada="$(awk -v n="$nome" '$2 == n { print $1 }' "$tmp/SHA256SUMS")"
  [ -n "$esperada" ] || { echo "central: o SHA256SUMS não lista $nome" >&2; exit 1; }
  if command -v sha256sum >/dev/null 2>&1; then baixada="$(sha256sum "$tmp/central$ext" | awk '{print $1}')"
  else baixada="$(shasum -a 256 "$tmp/central$ext" | awk '{print $1}')"; fi
  [ "$esperada" = "$baixada" ] || { echo "central: SHA256 não confere ($baixada, esperado $esperada)" >&2; exit 1; }
  chmod +x "$tmp/central$ext"
  "$tmp/central$ext" instalar --sem-plugin sim >&2
}

if [ ! -x "$alvo" ]; then
  instalar
elif [ "${1:-}" = --atualizar ]; then
  quer="$(sed -n 's/.*"version": *"\([0-9.]*\)".*/\1/p' "$aqui/../.claude-plugin/plugin.json" | head -1)"
  tem="$(versao_de "$alvo")"
  if [ -n "$quer" ] && [ -n "$tem" ] && menor "$tem" "$quer"; then
    echo "central: atualizando a Central $tem para a do plugin ($quer)..." >&2
    "$alvo" atualizar >&2 || echo "central: não consegui atualizar agora; sigo com a $tem" >&2
  fi
fi
[ -x "$alvo" ] || { echo "central: a instalação não deixou $alvo" >&2; exit 1; }
printf '%s\n' "$alvo"
