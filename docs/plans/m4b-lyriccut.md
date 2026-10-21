# Central M4b (extensões do LyricCut e migração) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (execução nativa). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** O LyricCut passa a rodar com a Central (spec §9, extensões do LyricCut, e §13, migração). O critério do M4 é: **Digimon e um vídeo-meta novo adicionados sem editar arquivo à mão**.

**Spec:** `docs/spec.md` §9 e §13.

**Repositórios:**
- **Central** (`<repo>`): a Task 1, na branch `m4b-fatos` → PR em `main`.
- **CortesEditor** (worktree `claude/agent-communication-system-6c9c42`): as Tasks 2–6.
  - O PR vai para `develop`, para o humano revisar.
  - Este plano **não** faz o merge, porque a troca do manual e a abertura da sprint com a Central são do humano (manual do time §2 e spec §13 item 3).

## Global Constraints
- Nada de mídia entra no git: nem vídeos, nem quadros, nem áudio. Os testes do LyricCut usam mídia sintética gerada com o ffmpeg do projeto, numa pasta temporária, apontada por `LYRICCUT_MAIN`.
- As extensões rodam com o Python embutido: `resources/runtime/python.exe -X utf8 -I`. Elas acham o checkout principal (onde ficam `artifacts/`) pelo `git rev-parse --git-common-dir`, como o `goal_videos.py`, e `LYRICCUT_MAIN` sobrescreve.
- O catálogo continua sendo `.claude/skills/goal-videos/videos.json`, com uma seção nova `base_sets`. A mudança é aditiva: `goal_videos.py` ignora chaves que não conhece. O catálogo gravado é sempre o do checkout em que a extensão roda.
- Mover não recodifica: é `os.replace`, ou cópia seguida de remoção entre discos. Um destino que já existe dá erro, nunca sobrescreve.

## Tasks

### Task 1 (Central): fatos na resposta `ext/v1`
- A resposta ganha o campo opcional `"fatos":[{"chave","valor","motivo"}]`. A Central grava cada um como fato do projeto (quem = vaga, `HUMANO` ou `CENTRAL`), na mesma transação dos eventos. Um fato inválido invalida a resposta (código `extensao`).
- `core`: extrair `insertFact(tx…)` do `SetFact`, que continua exigindo vaga. `ExtEvents` recebe os fatos.
- `docs/ext-v1.md`: o campo novo é aditivo e não muda a versão. As extensões leem `GET /v1/projetos/{id}/fatos` em `CENTRAL_ADDR`, rota de leitura que não precisa de pasta.
- [ ] Testes: `TestExtEventsGravaFatos` (core) e `TestExtensaoGravaFatos` (serviço, com o eco ganhando `args.fato`).

### Task 2 (LyricCut): extensão `exemplos`
`.central/ext/exemplos.py`, com as ações:
- `listar`: dados de galeria com os vídeos-meta (o primeiro quadro-chave da cache, quando existe) e os conjuntos de base (miniatura gerada uma vez em `artifacts/base-thumbs/<id>.jpg`). Os selos dizem o perfil, o idioma e "não catalogado".
- `status`: tabela do que está nas pastas e falta no catálogo, e vice-versa.
- `importar`, chamada pela pasta de entrada (`artifacts/entrada`, com `{arquivos}`):
  - classifica os arquivos por nome base: um vídeo sozinho é um vídeo-meta; vídeo + áudio + `.txt` é um conjunto de base; um grupo incompleto pede formulário avisando o que falta;
  - sem os campos, devolve `formulario`: nome, perfil e o que ensina, para o vídeo-meta; nome e idioma, para o conjunto;
  - com os campos, move os arquivos para `artifacts/goal_videos/` ou `artifacts/base_{videos,music,lyrics}/` e grava o catálogo;
  - para o vídeo-meta, roda `goal_videos.py preprocess --video <id>`;
  - emite `exemplo-novo` (ref = id, `dados.resumo`).
- `catalogar --id <id> [--tipo conjunto|meta] [campos]`: cataloga o que já está nas pastas sem mover nada (o caso do Digimon). Também é chamada pela aba, quando o humano responde ao formulário de "não catalogado".
- [ ] Testes `tests/test_central_exemplos.py`: classificação; formulário sem campos; importar conjunto; importar vídeo-meta (preprocess substituído por `LYRICCUT_GV_PREPROCESS=0`); destino que já existe dá erro; `catalogar`; `listar` com o não catalogado.

### Task 3 (LyricCut): extensão `e2e`
`.central/ext/e2e.py`, com as ações:
- `registrar --commit <hash> --nivel N --gates <json> [--until --from --nota --pasta --sprint --duracao-min]`: valida e devolve `fatos:[{chave:"e2e.<commit7>-<nivel>", valor, motivo}]`, no mesmo formato dos registros importados de `$LOCKS\e2e`. Emite `e2e-registrado`.
- `achar --commit <hash>`: lê os fatos `e2e.<commit7>-*` pela API e devolve os registros.
- `gates` (aba `serie`): uma série por gate numérico no tempo (x = quando o fato foi gravado), com o rótulo do commit e da sprint.
- [ ] Testes `tests/test_central_e2e.py`: validação de `registrar` (hash, nível, gates) e o formato do fato; `achar` e `gates` com uma API falsa (um servidor HTTP local de teste).

### Task 4 (LyricCut): `e2e_cyberpunk.py --conjunto <id>`
- Os caminhos do vídeo, da música e da letra vêm do conjunto em `base_sets` (padrão `cyberpunk`). O vídeo-meta vem de `meta` do conjunto, quando existe.
- O catálogo do Cyberpunk entra com os caminhos atuais, para o comportamento padrão não mudar.
- [ ] Teste `tests/test_central_conjunto.py`: resolução do conjunto (padrão, Digimon e desconhecido com erro claro), sem rodar o e2e.

### Task 5 (LyricCut): `central.toml` e `.central/`
`central.toml` na raiz:
- `[projeto]`;
- papéis com os nomes atuais do manual;
- áreas;
- `[recursos.gpu]` com o medidor `nvidia-smi`;
- `[itens.arquivos]` para `backlog/`;
- `[sprint]` e `[processo]` com os valores do manual;
- `[brief]` e `[[leitura]]` (o mapa de leitura);
- as duas extensões e `[acordar]` (`exemplo-novo` → PESQUISADOR e PLANEJADOR; `e2e-registrado` → INTEGRADOR).

Cartões e núcleo:
- `.central/papeis/*.md` e `.central/nucleo.md` são rascunhos curtos que só ensinam a CLI.
- A reescrita do manual é decisão do humano: os rascunhos vão no PR, marcados como proposta.

Verificação:
- `central doctor` fica verde na worktree;
- `central brief` de cada papel fica abaixo de 15 mil tokens (critério do M2), medido e registrado.

### Task 6: aceitação do M4 e PR
- **Numa pasta sintética** (via `LYRICCUT_MAIN`): soltar um vídeo-meta sintético novo e um conjunto sintético na pasta de entrada pela API de upload do painel, responder os formulários pela API de pendências e conferir os arquivos movidos, o catálogo e o evento `exemplo-novo` acordando o PESQUISADOR.
- **No catálogo real:** catalogar o Digimon com `central exemplos catalogar` (sem mover nada, só o `videos.json` desta branch) e conferir no painel. O vídeo-meta real novo fica para o humano soltar um quando quiser, já que só ele tem um.
- PR do CortesEditor para `develop`, com o plano de troca (spec §13: importar-locks, regra importar, abrir a sprint com a Central) para o humano.
