# Central V1 (release 1.0.0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (execução nativa). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fechar a v1 (spec §10 e §11): congelar o contrato `/v1` num golden, versionar 1.0.0 e publicar os binários para Windows, Linux e macOS numa release do GitHub.

**Spec:** `docs/spec.md` §10 (build, versão semântica) e §11 (goldens de `/v1` e `ext/v1`).

## Global Constraints
- A partir da 1.0.0, quebrar `/v1`, `ext/v1` ou o formato do `central.toml` exige versão maior e caminho novo (spec §10).
- Build com `CGO_ENABLED=0`, binário único por plataforma, versão embutida por `-ldflags -X`.

## Tasks

### Task 1: golden do contrato `/v1`
- As rotas passam a ser registradas por um registro que guarda método e caminho. O teste compara a lista ordenada com `internal/api/testdata/rotas-v1.golden.txt`.
- Um cenário curto pela API grava, num segundo golden (`respostas-v1.golden.json`), o **formato** (as chaves, recursivamente, com o tipo JSON) de algumas respostas: `saude`, `entrar`, `enviar`, `caixa`, `esperar`, erro, `item criar`, `proxima`, `brief` e `projetos`.
- `ATUALIZAR_GOLDEN=1` regrava os dois. Mudança sem regravar falha o teste com a diferença.
- [ ] Testes: `TestRotasV1Golden` e `TestFormatoDasRespostasV1Golden`.

### Task 2: versão 1.0.0 e documentação
- `version.Versao` passa a ser uma `var` (`-ldflags -X` a sobrescreve), com padrão `1.0.0`. O plugin do Claude Code vai para 1.0.0.
- `CHANGELOG.md` com a 1.0.0 (o que cada marco trouxe).
- README com a seção "Instalar": binário da release, `go install` e plugin.
- [ ] Teste: `TestVersao` continua (semver) e `TestVersaoPorLdflags` (build com `-X` e `central versao`).

### Task 3: release
- `scripts/release.ps1` compila para `windows/amd64`, `linux/amd64`, `darwin/amd64` e `darwin/arm64` em `dist/`, com o `SHA256SUMS`.
- Tag `v1.0.0` e `gh release create` com os binários e as notas do `CHANGELOG.md`.
- [ ] Verificação: o binário Windows da release roda `versao` (1.0.0) e `doctor`.
