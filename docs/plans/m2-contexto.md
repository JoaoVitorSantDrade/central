# Central M2 (contexto, MCP e plugin) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (execução nativa) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cortar a releitura (spec §7) com um `brief` montado por papel (cartão do papel, núcleo do projeto, regras ativas do papel, fatos relevantes, mapa de leitura e o estado vivo da vaga) e abrir a Central para outros clientes: servidor MCP (`central mcp`) e plugin do Claude Code (spec §10).

**Architecture:**
- Um pacote novo, `internal/contextpack`, monta o brief a partir do núcleo, do processo e dos arquivos do projeto na pasta da vaga (`.central/nucleo.md`, `.central/papeis/`).
- Os cartões padrão ficam embutidos no binário (`embed`).
- As regras (lições) são uma entidade nova do núcleo.
- O servidor MCP fica na CLI: cada ferramenta vira uma chamada ao mesmo despachante da CLI, então a paridade entre os dois é garantida por construção.
- O plugin do Claude Code é uma pasta com manifesto, skill, `.mcp.json` e hooks.

**Tech Stack:** o do M1, mais `github.com/modelcontextprotocol/go-sdk` v1.8.0.

**Spec:** `docs/spec.md` §7 (brief), §10 (MCP e plugin), §12 (linha M2), §13 (passo 2: importar lições).

## Global Constraints

- O brief sai em blocos fixos e nesta ordem:
  1. quem você é;
  2. agora;
  3. pendências;
  4. cartão do papel;
  5. núcleo;
  6. regras;
  7. fatos;
  8. mapa de leitura.
- `--curto` = blocos 1–3. O teto padrão é **8000 tokens**, estimados como **bytes/4**. Ao estourar, o corte vai do fim (8 → 7 → 6) e nunca toca os blocos 1–5. O corte grava o evento `brief.truncado`.
- Todo brief gerado grava o evento `brief.gerado` com `papel`, `bytes` e `tokens`. É o dado do painel do M3.
- Regras com estado `no_codigo` não entram no brief. Estados válidos: `ativa`, `no_manual`, `no_codigo`, `aposentada`.
- O cartão de papel vem de `.central/papeis/<PAPEL>.md`, procurado primeiro pelo nome do papel no projeto e depois pelo nome canônico. Na falta dos dois, vale o padrão embutido.
- O MCP expõe só operações de agente, com os mesmos nomes da CLI (`_` no lugar de espaço). O `esperar` no MCP tem teto de **5 min** por chamada, porque o cliente MCP pode ter um timeout de requisição.
- A saída da CLI continua sendo JSON, com uma exceção: `brief --md` imprime o texto em markdown, porque é feito para o agente ler.

## Decisões deste plano

1. **Fatos relevantes:** `[brief.fatos]` mapeia papel canônico → padrões de chave (`*` no fim casa prefixo). Exemplo: `EXECUTOR = ["e2e.base*", "gpu.*"]`. Sem configuração, o bloco de fatos lista as chaves com o horário da última gravação, sem os valores.
2. **Mapa de leitura:** `[[leitura]]` com `caminho`, `resumo`, `quando` e `papeis` (vazio = todos). O brief mostra uma linha por doc e não lê o arquivo.
3. **Regra:** `aplica` guarda papéis canônicos ou `TODOS`. A importação do `LICOES.md` traduz "todos" → `TODOS` e os nomes do projeto (FIXER) → canônicos, pela configuração de papéis.
4. **MCP:** o servidor roda na pasta do projeto, que o cliente configura no `.mcp.json`. A ferramenta `entrar` só existe para clientes que precisam se apresentar.

## Review Focus

- **Brief maior que o teto mesmo sem os blocos 6–8**, com um núcleo enorme: os blocos 1–5 saem inteiros, o resto é cortado, `brief.truncado` registra o que caiu e o texto avisa. → `TestBriefNucleoGrandeNuncaCortaBloco1a5` (Task 3).
- **`.central/papeis/FIXER.md` presente para o papel EXECUTOR renomeado:** vale o do nome do projeto. → `TestCartaoPeloNomeDoProjeto` (Task 2).
- **`LICOES.md` com linhas fora do formato** (cabeçalho, separador, texto solto): são ignoradas, e as válidas entram. Reimportar não duplica. → `TestImportarLicoesIdempotente` (Task 1).
- **Ferramenta MCP com argumento faltando ou do tipo errado:** volta um erro MCP com a mensagem da CLI, e o servidor não cai. → `TestMCPErroDeUso` (Task 4).
- **Cliente MCP desconecta no meio de um `esperar`:** o servidor encerra sem deixar goroutine presa. → `TestMCPEncerraComStdin` (Task 4).

---

### Task 1: Regras (lições) no núcleo e importação do `LICOES.md`

**Files:**
- Create: `internal/core/rules.go` (+ teste), `internal/adapters/lessons/lessons.go` (+ teste com o `LICOES.md` real copiado para `testdata`)
- Modify: `internal/core/schema.go` (migração 5: `regras(id TEXT PRIMARY KEY, origem, texto, pratica, aplica TEXT /*json*/, estado, comando, criada_em, atualizada_em)`), service/api/cli

**Interfaces:**
- `type Rule struct{ ID, Origem, Texto, Pratica, Estado, Comando string; Aplica []string; CriadaEm, AtualizadaEm time.Time }`
- `SaveRule(ctx, quem string, r Rule) (Rule, bool, error)`: insere ou atualiza; `mudou` = algo diferente; evento `regra.gravada`.
- `SetRuleState(ctx, quem, id, estado, comando string) (Rule, error)`
- `Rules(ctx, f RuleFilter{Papel, Estado string}) ([]Rule, error)`: `Papel` casa com `TODOS` ou com o papel em `aplica`.
- `lessons.Parse(md []byte) ([]Lesson, []string)`: as linhas da tabela `| ID | Origem | Lição | Regra prática | Quem aplica | Estado |`; o segundo retorno são os avisos.
- CLI:
  - `regra listar [--papel P] [--estado E]`
  - `regra criar --id L-XXX --aplica A,B --texto T --pratica P [--origem O]`
  - `regra estado <id> <estado> [--comando C]`
  - `regra importar <arquivo>`

- [ ] Testes: `TestRegraCriarEFiltrar`, `TestRegraEstadoValido`, `TestParseLicoesReal` (o arquivo real tem 28 lições e "no manual (§4)" vira `no_manual`), `TestImportarLicoesIdempotente`, `TestRegraCLI`.
- [ ] Implementar e commitar `feat(core): rules (lessons) with LICOES.md import`.

### Task 2: Cartões de papel, núcleo e configuração do brief

**Files:**
- Create: `internal/contextpack/cards.go`, `internal/contextpack/cartoes/*.md` (um por papel canônico), testes
- Modify: `internal/config/` (`[brief]` com `max_tokens` e `fatos`; `[[leitura]]`)

**Interfaces:**
- `func Card(pasta, papelProjeto, papelCanonico string) (texto, fonte string)`: a fonte é o caminho do arquivo ou `embutido:<PAPEL>`.
- `func Nucleo(pasta string) (string, bool)`
- config:
  - `BriefSec{ MaxTokens int; Fatos map[string][]string }`, com padrão de 8000;
  - `Leitura []LeituraSec{ Caminho, Resumo, Quando string; Papeis []string }`;
  - validação: papéis canônicos e caminho não vazio.
- Cartões padrão:
  - cada um diz o que o papel faz na Central e os comandos dele;
  - todos terminam com a instrução de fim de ciclo do piloto (`esperar` em segundo plano e despertar de reserva de 30 min);
  - todos são curtos, abaixo de 1,2 mil tokens cada.

- [ ] Testes: `TestCartaoPadraoParaTodoPapel` (todo papel canônico tem cartão, menor que 4800 bytes, e todos citam `central esperar`); `TestCartaoPeloNomeDoProjeto`; `TestCartaoCanonicoSemONomeDoProjeto`; `TestNucleoAusente`; `TestParseBriefELeitura`.
- [ ] Implementar e commitar `feat(contextpack): role cards, project core and brief config`.

### Task 3: Montagem do brief

**Files:** Create `internal/contextpack/brief.go` (+ testes); Modify service/api/cli.

**Interfaces:**
- `type Bloco struct{ N int; Titulo, Texto string; Cortado bool }`
- `type Brief struct{ Papel string; Blocos []Bloco; Texto string; Tokens int; Cortados []int }`
- `type Input struct{ Agente core.Agent; Canonico, Projeto, RaizProjeto, DataDir string; Sprint *core.Sprint; Reserva *core.Item; Caixa []core.Message; Pendencias core.Pendencias; Cartao, Nucleo string; Regras []core.Rule; Fatos []core.Fact; Leitura []Leitura; Agora time.Time }`
- `func Montar(in Input, curto bool, maxTokens int) Brief`: pura, sem I/O.
- service: `Brief(ctx, projetoID, pasta string, curto bool, maxTokens int) (contextpack.Brief, error)`. Ele junta as entradas, grava `brief.gerado` (e `brief.truncado` se houve corte) e não avança o cursor: a caixa é só um resumo das não lidas.
- CLI: `brief [--curto] [--max-tokens N] [--md]`.

- [ ] Testes:
  - `TestBriefOrdemDosBlocos`;
  - `TestBriefCurto`;
  - `TestBriefCortaDoFim` (o teto corta só o 8, depois o 7, depois o 6);
  - `TestBriefNucleoGrandeNuncaCortaBloco1a5`;
  - `TestBriefRegrasDoPapelSemNoCodigo`;
  - `TestBriefFatosPorPadrao`;
  - `TestBriefNaoAvancaCursor` (service);
  - `TestBriefCLI` (e2e: `--md` imprime markdown, e o evento `brief.gerado` registra os tokens).
- [ ] Implementar e commitar `feat: per-role brief`.

### Task 4: Servidor MCP

**Files:** Create `internal/cli/mcp.go` (+ teste); Modify `internal/cli/cli.go` (subcomando `mcp`).

**Interfaces:**
- `func ServeMCP(ctx context.Context, in io.Reader, out io.Writer) error`, com o SDK oficial (transporte stdio).
- Ferramentas `type ferramenta struct{ nome, descricao string; params []param; args func(map[string]any) ([]string, error) }`. Cada uma monta a linha de comando e chama `run(args, stdin)`. O resultado JSON é o conteúdo; um erro da CLI vira `isError`.
- Ferramentas:
  - `brief`, `caixa`, `enviar`, `responder`, `pendentes`, `esperar` (até 5 m), `status`;
  - `proxima`, `reservar`, `concluir`, `bloquear`, `revisar`, `aprovar`, `rejeitar`, `integrar`, `validar`, `item_ver`, `item_listar`;
  - `trava`, `soltar`, `recurso_pedir`, `recurso_soltar`, `fato_get`, `fato_set`, `saude`, `entrar`.

- [ ] Testes (com o binário real e cliente MCP do próprio SDK sobre pipes):
  - `TestMCPListaFerramentas`;
  - `TestMCPEnviarECaixa`;
  - `TestMCPErroDeUso`;
  - `TestMCPEncerraComStdin`.
- [ ] Implementar e commitar `feat(cli): MCP server`.

### Task 5: Plugin do Claude Code

**Files:** Create `integrations/claude-code/.claude-plugin/plugin.json`, `integrations/claude-code/skills/central/SKILL.md`, `integrations/claude-code/.mcp.json`, `integrations/claude-code/hooks/hooks.json`, `integrations/claude-code/README.md`; teste `internal/cli/plugin_test.go` (valida os JSON e que os comandos citados existem na CLI).

- A skill ensina o ciclo:
  1. `entrar` uma vez;
  2. `brief --md` no começo;
  3. `brief --curto --md` depois de um resumo automático;
  4. trabalho pelos comandos do papel;
  5. sem trabalho, `esperar` em segundo plano e um despertar de reserva.
- Hook `SessionStart` (matcher `compact|resume`): `central brief --curto --md`. Quando a pasta não é um projeto, ele sai com 0 sem imprimir nada (`--se-projeto`).
- `.mcp.json`: `{"mcpServers": {"central": {"command": "central", "args": ["mcp"]}}}`.

- [ ] Testes: `TestPluginManifestosValidos`, `TestBriefSeProjetoForaDeProjetoSilencioso`.
- [ ] Implementar e commitar `feat: Claude Code plugin`.

### Task 6: Cenário M2 e documentação

- [ ] `TestCenarioM2`, com o binário real:
  1. um projeto com `.central/nucleo.md`, um cartão sobrescrito, duas regras (uma `no_codigo`), um fato relevante e o mapa de leitura;
  2. `brief --md` de um executor contém o cartão sobrescrito, o núcleo, a regra ativa (e não a `no_codigo`), o fato e o mapa;
  3. `brief --curto` traz só os blocos 1–3;
  4. o MCP `brief` devolve o mesmo texto.
- [ ] README: seções "Brief e contexto", "Regras", "MCP" e "Plugin do Claude Code".
- [ ] Suíte completa, `vet` e `gofmt`; commit `test: m2 scenario; docs`.
