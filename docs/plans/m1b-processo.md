# Central M1b (processo "sprint") Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (execução nativa, decidida pelo humano em 2026-10-21) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** O processo opinativo da spec §6 sobre o núcleo do M1a: papéis configuráveis, itens com o ciclo de vida do §6.2 (com quem acorda quem), `proxima`, decisões de duas chaves, sprint com cerimônias e encerramento antecipado, adaptador do backlog em arquivos (git), `saude`/`orfas`/`metricas` e a migração do `$LOCKS` do LyricCut (`importar-locks`/`exportar-locks`).

**Architecture:** Um pacote novo, `internal/process`, com as regras: papéis, máquina de estados, `proxima`, horários da sprint e métricas. Ele **não usa SQL** (spec §4, regra 2). O `core` ganha primitivas genéricas de item (`WithItems`: uma transação com leitura, gravação, nota e mensagem de sistema) e de sprint (linhas, cerimônias disparadas). O `service` liga processo + core e expõe tudo; `api`/`cli` continuam cascas. O adaptador de arquivos (`internal/adapters/files`) e o importador do `$LOCKS` (`internal/adapters/lockdir`) ficam fora do núcleo.

**Tech Stack:** o do M1a, mais `go.yaml.in/yaml/v3` (frontmatter YAML dos arquivos de tarefa) e `git` na máquina (só o adaptador de arquivos).

**Spec:** `docs/spec.md` §6 (inteiro), §6.6 (comandos), §13 (migração do LyricCut).

## Global Constraints

- Tudo do M0/M1a continua valendo (saída JSON, códigos 0–5, loopback, eventos na mesma transação, `CENTRAL` como remetente das mensagens de sistema, motivo do `esperar` derivado do tipo).
- `internal/process` só importa `core` (e a biblioteca padrão). `core` não importa `process`.
- Migração 4 (nova, uma string só até o merge do M1b): `agentes.area`, `itens`, `itens_notas`, `sprints`, `cerimonias`.
- Papéis canônicos: `GERENTE`, `PLANEJADOR`, `PESQUISADOR`, `EXECUTOR`, `REVISOR`, `VALIDADOR`, `INTEGRADOR`, mais `HUMANO` (só para veto e respostas).
- Mensagens de sistema novas: `item.<estado>` (ref = id do item), `cerimonia.<nome>` (ref = id da sprint), `sprint.encerrar_antes`, `fila.vazia`, `decisao.<estado>`. O motivo do `esperar` fica `item`, `cerimonia`, `sprint`, `fila` ou `decisao`.
- Valores padrão: deslocamentos da sprint (antes do `fim`) `escopo 75m`, `congelamento 60m`, `corte 30m`, `retro 20m`; check-in no meio entre abertura e congelamento; condição de encerramento antecipado avaliada a cada minuto e avisada depois de `10m` contínuos; polling do adaptador a cada `30s`; `orfas` depois de `12h`; `fila.vazia` no máximo uma vez a cada `30m`; limites de item parado: `concluida 30m`, `aprovada 30m`, `em_andamento 3h`.

## Decisões deste plano (onde a spec é omissa)

1. **Papéis no `central.toml`:**
   ```toml
   [papeis]
   nomes = { EXECUTOR = "FIXER" }
   desligados = ["VALIDADOR"]
   senior = ["FIXER_SENIOR"]
   ```
   `senior` são vagas. Os agentes entram com o nome do projeto (`--papel FIXER`), e o processo traduz para o canônico.
2. **Área do agente:** `entrar --area <a>` (coluna `agentes.area`, opcional) é a preferência usada pelo `proxima`.
3. **`proposta → pronta`** é o comando `item pronto <id>`. **Bloquear** registra a nota, incrementa `bloqueios` e devolve o item a `pronta` na mesma transação (o evento registra a passagem por `bloqueada`). **Reabrir** (dono, a partir de `concluida`/`em_revisao`/`aprovada`) é `central reabrir <id>`, que anula a revisão e acorda o REVISOR.
4. **Decisão:** `decisao abrir` (PLANEJADOR) cria em `chave1` e acorda o GERENTE; `decisao chave <id> --acordo` ou `--objecao "<motivo>"` (GERENTE) leva a `aprovada` ou `objecao` e acorda o PLANEJADOR; `decisao humano <id>` leva a `humano`; `decisao veto <id> "<motivo>"` vale em qualquer estado, é do papel `HUMANO`, e a CLI aceita `--humano` para quem está no terminal.
5. **Antecipar a sprint:** `congelamento = corte = agora`, `retro = agora + 10m`, `fim = agora + 30m`; os horários originais ficam guardados.
6. **Adaptador:** os campos do arquivo são lidos pelos nomes canônicos (`id`, `titulo`, `area`, `prioridade`, `tipo`, `sprint`, `arquivos`, `folga`, `bloqueante`, `visivel_ao_usuario`, `bloqueio`). `[itens.arquivos.mapa]` cria apelidos (ex.: `arquivos = "arquivos_provaveis"`). `[itens.arquivos.recurso_se]` liga um booleano a um recurso (ex.: `usa_gpu = "gpu"`). Os campos restantes vão em `campos` (JSON). O repositório usado é o `repo` configurado ou, sem ele, a raiz registrada.
7. **`importar-locks`** grava tudo pelo serviço, com os horários originais, e nunca apaga nada do `$LOCKS`. **`exportar-locks`** escreve numa pasta indicada, no formato do manual do LyricCut (§2 e §5): `canal/`, `equipe/`, `T-XXX/owner.json` e `sprint.json`.

## Review Focus

- **Duas vagas reservando a mesma tarefa ao mesmo tempo:** uma vence e a outra recebe 2. → `TestReservaConcorrente` (Task 3).
- **O adaptador vê uma tarefa em `todo/` que no daemon já está `em_andamento`:** o estado não volta. E um arquivo que aparece em `done/` leva o item a `integrada`. → `TestAdaptadorNuncaVolta` (Task 7).
- **Frontmatter malformado ou sem `id` num arquivo:** o arquivo é ignorado com evento `item.fonte_invalida` e o resto é importado. → `TestAdaptadorArquivoInvalido` (Task 7).
- **A sprint cruza a meia-noite** (`22:00-07:00`) e o daemon reinicia no meio: as cerimônias já disparadas não repetem e as vencidas disparam no próximo `Tick`. → `TestSprintCruzaMeiaNoiteEReinicio` (Task 5).
- **Papel desligado no meio do fluxo** (sem REVISOR): `concluida` acorda o INTEGRADOR, e `aprovar` sem REVISOR fica com o INTEGRADOR. → `TestFluxoSemRevisor` (Task 3).

---

### Task 1: Configuração do processo e papéis

**Files:** Create `internal/process/roles.go`, `internal/process/roles_test.go`; Modify `internal/config/project.go`, `internal/config/project_test.go`.

**Interfaces:**
- config: `ProjectConfig` ganha `Papeis PapeisSec` (`Nomes map[string]string`, `Desligados []string`, `Senior []string`), `Itens ItensSec` (`Obrigatorios []string`, padrão `["titulo","area","prioridade","criterio_de_pronto"]`; `Arquivos *ArquivosSec`), `Sprint SprintSec` (`Escopo`, `Congelamento`, `Corte`, `Retro Duration`; `Acordar map[string][]string`) e `Processo ProcessoSec` (`LimiteParado map[string]Duration`, `OrfasApos Duration`). `ArquivosSec{Ref, Repo, Padrao string; Pastas map[string]string; Mapa map[string]string; RecursoSe map[string]string}`.
- Validação: chaves de `nomes` e `desligados` ⊂ papéis canônicos; `GERENTE`, `PLANEJADOR` e `EXECUTOR` não podem ser desligados; nomes de projeto únicos; offsets da sprint > 0 e `escopo > congelamento > corte > retro`; pastas do adaptador só com estados `proposta`, `pronta` e `integrada`.
- process: `type Roles struct`; `func NewRoles(nomes map[string]string, desligados, senior []string) Roles`; `(r Roles) Canonical(papel string) (string, bool)`; `(r Roles) Name(canonico string) string`; `(r Roles) Enabled(canonico string) bool`; `(r Roles) IsSenior(vaga string) bool`; constantes `Gerente`, `Planejador`, `Pesquisador`, `Executor`, `Revisor`, `Validador`, `Integrador`, `Humano`.

- [ ] Testes: `TestRolesRenomeiaEDesliga` (`FIXER` → `EXECUTOR`; `Name(EXECUTOR) == "FIXER"`; `Enabled(VALIDADOR) == false`); `TestRolesPapelDesconhecido`; `TestParseProcessoPadroes` (offsets 75/60/30/20 e obrigatórios padrão); `TestParseProcessoInvalido` (desligar GERENTE; papel desconhecido; offsets fora de ordem; pasta com estado `em_andamento`).
- [ ] Rodar → FAIL; implementar; rodar → PASS.
- [ ] Commit `feat(process): roles and process configuration`.

### Task 2: Primitivas de item no núcleo

**Files:** Create `internal/core/items.go`, `internal/core/items_test.go`; Modify `internal/core/schema.go` (migração 4).

**Interfaces:**
- Tabelas:
  - `itens(id TEXT PRIMARY KEY, tipo TEXT, titulo TEXT, estado TEXT, dono TEXT, prioridade TEXT, area TEXT, categoria TEXT, sprint TEXT, arquivos TEXT /*json*/, recurso TEXT, folga INT, bloqueante INT, visivel INT, bloqueios INT, ponta TEXT, ponta_revisada TEXT, commit_ TEXT, marco INT, campos TEXT /*json*/, fonte TEXT, criado_em TEXT, atualizado_em TEXT, estado_desde TEXT)`;
  - `itens_notas(id INTEGER PK, item TEXT, tipo TEXT, texto TEXT, vaga TEXT, quando TEXT)`;
  - `ALTER TABLE agentes ADD area TEXT NOT NULL DEFAULT ''`.
- `type Item struct{ ID, Tipo, Titulo, Estado, Dono, Prioridade, Area, Categoria, Sprint, Recurso, Ponta, PontaRevisada, Commit, Fonte string; Arquivos []string; Folga, Bloqueante, Visivel bool; Bloqueios, Marco int; Campos map[string]any; CriadoEm, AtualizadoEm, EstadoDesde time.Time }` (tags snake_case; `commit` no JSON).
- `type ItemFilter struct{ IDs, Estados, Tipos []string; Dono, Sprint string }`.
- `type ItemTx interface{ Now() time.Time; Get(id string) (Item, error); List(f ItemFilter) ([]Item, error); Agent(vaga string) (Agent, error); Agents() ([]Agent, error); Save(it Item, evento string, dados map[string]any) error; Note(item, tipo, texto string) error; Notes(item string) ([]Note, error); Notify(para, tipo, ref, corpo string) error }`.
- `func (c *Core) WithItems(ctx, quem string, fn func(ItemTx) error) error`: uma transação; `Save` grava o item (insere ou atualiza; `estado_desde` muda quando o estado muda) e o evento `item.<evento>` com `quem`; `Notify` envia mensagem de sistema; `Broadcast` depois do commit se houve `Notify`.
- `func (c *Core) Items(ctx, f ItemFilter) ([]Item, error)`; `func (c *Core) Item(ctx, id string) (Item, []Note, error)`.
- `Agent` ganha `Area string`; `Enter` aceita a área por `EnterWithConfig` (parâmetro novo `area`); a CLI tem `entrar --area`.

- [ ] Testes: `TestWithItemsSalvaEventoEMensagem` (Save + Notify numa transação → item, evento `item.criado` e mensagem); `TestWithItemsRollback` (`fn` com erro → nada gravado e ninguém acordado); `TestEstadoDesdeMudaSoComEstado`; `TestItemsFiltro`; `TestEntrarComArea`.
- [ ] Rodar → FAIL; implementar; rodar → PASS.
- [ ] Commit `feat(core): item primitives with transactional closure`.

### Task 3: Máquina de estados e comandos de item

**Files:** Create `internal/process/items.go`, `internal/process/items_test.go`; Modify `internal/service/` (novo `process.go`), `internal/api/server.go`, `internal/cli/cli.go` (novo `process_cmds.go`).

**Interfaces:**
- `type Engine struct`; `func NewEngine(c *core.Core, roles Roles, cfg Config) *Engine`; `type Config struct{ Obrigatorios []string }` (o congelamento é lido da sprint corrente no core: `now >= Congelamento` e sprint não encerrada).
- Ações, todas `(ctx, vaga string, ...) (core.Item, error)`: `Criar(in ItemInput)`; `Pronto(id)`; `Reservar(id)`; `Concluir(id, ponta)`; `Bloquear(id, nota)`; `Revisar(id)`; `Aprovar(id, ponta)`; `Rejeitar(id, motivo)`; `Integrar(id, commit)`; `Validar(id, resultado)`; `Reabrir(id)`. `type ItemInput struct{ ID, Titulo, Area, Prioridade, Categoria, Sprint, Recurso string; Arquivos []string; Folga, Bloqueante, Visivel bool; Campos map[string]any }` (`ID` vazio → `NextID("T")`).
- A tabela da spec §6.2 é implementada como dados (`transicoes []transicao{de, para, papel, cond, acorda}`). Quem não pode → `core.ErrInvalid` com "papel X não pode …"; estado errado → `core.ErrConflict`; `Reservar` com conflito de arquivos ou congelada → `core.ErrConflict` com o motivo.
- Acordar: `Notify(roles.Name(papel), "item."+novoEstado, id, corpo)`; o dono é endereçado pela vaga. Papel desligado: segue a regra "sem REVISOR → INTEGRADOR" e, sem VALIDADOR, não acorda ninguém.
- CLI: `item criar --titulo … [--id --area --prioridade --categoria --sprint --recurso --arquivos a,b --folga sim --bloqueante sim --visivel sim]`, `item pronto|ver|listar [--estado E]`, `reservar <id>`, `concluir <id> --ponta <hash>`, `bloquear <id> "<nota>"`, `revisar <id>`, `aprovar <id> --ponta <hash>`, `rejeitar <id> "<motivo>"`, `integrar <id> --commit <hash>`, `validar <id> <resultado>`, `reabrir <id>`.

- [ ] Testes (com o core real num banco temporário): uma linha por transição da tabela do §6.2, permitida e recusada (`TestTransicoes`, tabela); `TestReservaConcorrente` (20 goroutines → 1 vence); `TestConflitoDeArquivos` (prefixo `backend/` × `backend/x.py`); `TestAprovarExigePontaAtual`; `TestBloquearVoltaAPronta` (`bloqueios == 1`, nota gravada, PLANEJADOR acordado); `TestReabrirAnulaRevisao`; `TestFluxoSemRevisor`; `TestCongeladaNaoReserva`; `TestItemCLI` (e2e: criar → pronto → reservar → concluir → revisar → aprovar → integrar, com o REVISOR recebendo `item.concluida` no `esperar`).
- [ ] Rodar → FAIL; implementar; rodar → PASS.
- [ ] Commit `feat(process): item state machine with wake-ups`.

### Task 4: `proxima`

**Files:** Create `internal/process/next.go`, `internal/process/next_test.go`; Modify service/api/cli.

**Interfaces:** `func (e *Engine) Proxima(ctx, vaga string) (Escolha, error)`; `type Escolha struct{ Item *core.Item; Motivo string; Descartados map[string]string }` (tags `item`, `motivo`, `descartados`). Ordem e filtros do §6.3. `Recurso` saturado = fila não vazia ou exclusivo ativo (`core.ResourceStatus`). Sem resultado: `Item == nil` com `Motivo` e, se a causa for fila vazia, `fila.vazia` ao PLANEJADOR no máximo a cada 30 min (evento `fila.vazia_avisada`).

- [ ] Testes (quadros sintéticos): `TestProximaBugP0Primeiro`; `TestProximaSprintAntesDeFolga`; `TestProximaFolgaAntesDoResto`; `TestProximaAreaPreferida`; `TestProximaBloqueioSoSenior` (com e sem vaga sênior; duas notas → ninguém); `TestProximaCongelada`; `TestProximaEvitaRecursoSaturado`; `TestProximaFilaVaziaAvisaUmaVez`.
- [ ] Rodar → FAIL; implementar; rodar → PASS.
- [ ] Commit `feat(process): next-task selection`.

### Task 5: Sprint e cerimônias

**Files:** Create `internal/process/sprint.go`, `internal/process/sprint_test.go`, `internal/core/sprints.go`, `internal/core/sprints_test.go`; Modify schema (migração 4: `sprints`, `cerimonias`), `internal/core/tick.go` (gancho), service/api/cli.

**Interfaces:**
- core: `type Sprint struct{ ID string; Abertura, Checkin, Escopo, Congelamento, Corte, Retro, Fim time.Time; EncerradaEm *time.Time; Original map[string]time.Time; AntecipadaEm *time.Time }`; `CreateSprint(ctx, quem string, s Sprint) error` (recusa com `ErrConflict` se há sprint aberta); `CurrentSprint(ctx) (*Sprint, error)`; `UpdateSprint(ctx, quem string, s Sprint, evento string) error`; `DueCeremonies(ctx, now) ([]Ceremony, error)`; `MarkCeremony(ctx, sprint, nome string, now, papeis []string, corpo string) error` (grava, avisa e emite o evento `cerimonia.<nome>` numa transação; idempotente).
- core: `type TickHook func(ctx context.Context) error`; `(c *Core) AddTickHook(h TickHook)`: o `Tick` chama os ganchos depois das regras do núcleo (é por aí que o processo entra no relógio sem o núcleo importar o processo).
- process: `func Horarios(abertura, fim time.Time, cfg SprintCfg) core.Sprint` (com o check-in no meio até o congelamento); `func ParseExpediente(s string, agora time.Time, loc *time.Location) (inicio, fim time.Time, err error)` (cruza a meia-noite); `(e *Engine) AbrirSprint(ctx, vaga, expediente, id string)`, `VerSprint`, `Antecipar`, `Encerrar`, `TickSprint(ctx)` (cerimônias vencidas e encerramento antecipado). O id padrão é `S-<AAAAMMDD da abertura>`, com `-2`, `-3`… se já existir.
- Quem é acordado por cerimônia (sobrescrito por `[sprint.acordar]`): abertura → TODOS; checkin → GERENTE, PLANEJADOR; escopo → GERENTE, PLANEJADOR, INTEGRADOR; congelamento → TODOS; corte → INTEGRADOR; retro → GERENTE, PLANEJADOR; fim → TODOS.
- Encerramento antecipado: as condições do §6.4 são verdadeiras por 10 min seguidos → `sprint.encerrar_antes` ao GERENTE uma vez (o contador zera se a condição cair).

- [ ] Testes: `TestHorariosPadrao` (expediente de 8 h → checkin = abertura + (8h − 60m)/2, etc.); `TestParseExpedienteMeiaNoite`; `TestIdComSufixo`; `TestCerimoniasDisparamUmaVez`; `TestSprintCruzaMeiaNoiteEReinicio` (fecha e reabre o serviço com o relógio adiantado → as vencidas disparam uma vez, sem repetir as já disparadas); `TestEncerramentoAntecipado` (9 min → nada; 10 min → aviso; a condição cai e volta → novo aviso só depois de mais 10 min); `TestAnteciparGuardaOriginais`; `TestSprintCLI` (e2e: `sprint abrir --expediente` e `sprint ver`).
- [ ] Rodar → FAIL; implementar; rodar → PASS.
- [ ] Commit `feat(process): sprint, ceremonies and early-close detection`.

### Task 6: Decisões

**Files:** Create `internal/process/decisions.go`, `internal/process/decisions_test.go`; Modify service/api/cli.

**Interfaces:** `(e *Engine) AbrirDecisao(ctx, vaga, titulo, corpo string) (core.Item, error)` (id `D-XXX`, estado `chave1`, acorda o GERENTE); `Chave(ctx, vaga, id string, acordo bool, motivo string)`; `Humano(ctx, vaga, id)`; `Veto(ctx, quem, id, motivo string)` (`quem = "HUMANO"`; qualquer estado vira `vetada`; acorda o PLANEJADOR). A decisão é um item de `tipo = "decisao"`, com o corpo e os motivos em `campos` e nas notas.

- [ ] Testes: `TestDecisaoDuasChaves`, `TestDecisaoObjecao`, `TestDecisaoSoGerenteDaChave2`, `TestVetoEmQualquerEstado`, `TestDecisaoCLI`.
- [ ] Rodar → FAIL; implementar; rodar → PASS.
- [ ] Commit `feat(process): two-key decisions and human veto`.

### Task 7: Adaptador de itens em arquivos (git)

**Files:** Create `internal/adapters/files/files.go`, `internal/adapters/files/files_test.go`; Modify `internal/process/items.go` (`Sincronizar`), `internal/service/` (novo `adapters.go`), `internal/daemon/daemon.go`.

**Interfaces:**
- files: `type Source struct{ Dir, Ref, Padrao string; Pastas, Mapa, RecursoSe map[string]string }`; `func Read(ctx, s Source) (head string, itens []process.Found, invalidos []process.Invalid, err error)`, com `ref` vazio = pasta de trabalho, ou `git rev-parse` + `git ls-tree -r --name-only` + `git cat-file --batch`. `func ParseFrontmatter(b []byte) (map[string]any, error)`.
- process: `(e *Engine) Sincronizar(ctx, achados []files.Found, invalidos []files.Invalid) (Resumo, error)`: cria o que falta; atualiza campos descritivos (título, prioridade, área, categoria, sprint, arquivos, recurso, folga, bloqueante, visível, campos); só **avança** o estado (`proposta < pronta < integrada`, e nunca mexe num item reservado, salvo para levá-lo a `integrada`); `bloqueio` no arquivo conta como uma nota de bloqueio (sem duplicar); evento `item.sincronizado`; inválidos → evento `item.fonte_invalida` (uma vez por caminho e conteúdo). `type Found struct{ Estado, Caminho, Hash string; In ItemInput; Bloqueio string }` e `type Invalid struct{ Caminho, Hash, Erro string }` ficam no `process` (o `files` importa o `process`, que só importa o `core`).
- service: `RunAdapters(ctx)`: para cada projeto com `[itens.arquivos]`, consulta o `head` a cada 30 s e sincroniza quando ele muda. O daemon liga isso como os medidores.

- [ ] Testes: `TestParseFrontmatterLyricCut` (o formato real de `backlog/done/T-094`: listas, `>` dobrado, mapa aninhado); `TestReadPastaDeTrabalho`; `TestReadGit` (repo temporário com `git init`, commit e branch `develop`); `TestAdaptadorCriaEAvança`; `TestAdaptadorNuncaVolta`; `TestAdaptadorArquivoInvalido`; `TestAdaptadorMapaERecursoSe` (`arquivos_provaveis` e `usa_gpu = "gpu"`); `TestRunAdaptersSoComHeadNovo`.
- [ ] Rodar → FAIL; implementar; rodar → PASS.
- [ ] Commit `feat(adapters): file/git backlog adapter`.

### Task 8: `saude`, `orfas` e `metricas`

**Files:** Create `internal/process/health.go`, `internal/process/metrics.go`, testes; Modify core (`Events(ctx, f EventFilter) ([]Event, error)`: leitura de eventos por tipo, intervalo e ref), service/api/cli.

**Interfaces:**
- `(e *Engine) Saude(ctx) (Saude, error)`: `{sumidos []Agent; parados []ItemParado{item, estado, desde, limite}; fila_qa int; concessoes_vencidas, sem_uso []Grant; pendencias_vencidas []Message; proxima_cerimonia *{nome, quando}; alertas []string}`.
- `(e *Engine) Orfas(ctx) ([]core.Item, error)` (dono encerrado ou sumido há mais de `orfas_apos`); `LiberarOrfas(ctx, vaga)` (só GERENTE ou INTEGRADOR) → `pronta` com nota.
- `(e *Engine) Metricas(ctx, sprintID string) (Metricas, error)`: `integradas`, `rejeicoes`, `bloqueios`, `latencias` (mediana e p75 de `pronta→em_andamento`, `concluida→em_revisao`, `concluida→integrada`, em segundos), `tarefas_por_executor_hora`, `uso_recursos` (% da janela com concessão ativa, por recurso) e `ociosidade_min` por vaga (eventos `agente.status` com estado `ocioso`). A janela é a da sprint ou, sem sprint, as últimas 24 h.
- CLI: `saude`, `orfas [--liberar]`, `metricas [--sprint S]`.

- [ ] Testes: `TestSaudeMostraParadosESumidos`; `TestOrfasDepoisDeDozeHoras`; `TestMetricasLatencias` (eventos sintéticos com horários conhecidos → medianas exatas); `TestMetricasUsoDeRecurso`; `TestSaudeCLI`.
- [ ] Rodar → FAIL; implementar; rodar → PASS.
- [ ] Commit `feat(process): health, orphans and sprint metrics`.

### Task 9: `importar-locks` e `exportar-locks`

**Files:** Create `internal/adapters/lockdir/lockdir.go`, `internal/adapters/lockdir/lockdir_test.go` (com `testdata/` sintético no formato do manual); Modify core (`ImportMessages`, `ImportItem`, `ImportAgent`: gravam com horários e autores dados, com evento `importado`), service/api/cli.

**Interfaces:**
- `func Load(dir string) (Snapshot, error)` (lê `canal/*.json`, `equipe/*.json`, `T-*/owner.json`, `M-*/owner.json`, `e2e/*.json`, `ids/*`, `sprint.json`; tolera BOM UTF-8 e arquivos quebrados, que listam em `Snapshot.Erros`).
- `func Export(dir string, s Snapshot) error`.
- service `ImportLocks(ctx, projetoID, pasta, snap)` e `ExportLocks(ctx, projetoID) (Snapshot, error)`.
- Mapeamento:
  - mensagem: `de`, `para`, `assunto` → tipo, `ref`, `corpo`, `responde`, `precisa_resposta`; horário a partir do id `AAAAMMDDTHHMMSS`;
  - ponto: vaga, papel e estado → agente (pasta = worktree, se existir; senão `(importado) <id>`);
  - reserva: item com o estado mapeado (`em_andamento`, `concluida` com revisão → `em_revisao`/`aprovada`/`rejeitada`, `bloqueada`, `rejeitada`);
  - `e2e/<commit>-<nivel>.json` → fato `e2e.<commit>.<nivel>`;
  - `ids/T-XXX` → contador mínimo;
  - `sprint.json` → sprint (se não houver uma aberta).
  - Reimportar é idempotente (chave `importado:<origem>`).
- CLI: `importar-locks <pasta>` e `exportar-locks <pasta>`.

- [ ] Testes: `TestLoadToleraBOMEQuebrados`; `TestImportarIdempotente`; `TestImportarMapeiaEstados`; `TestExportarEReimportar` (exporta e reimporta num projeto novo → mesmo quadro); `TestImportarCLI`.
- [ ] Rodar → FAIL; implementar; rodar → PASS.
- [ ] Commit `feat(adapters): import/export the LyricCut $LOCKS directory`.

### Task 10: Cenário de sprint ponta a ponta e documentação

**Files:** Create `internal/cli/m1b_e2e_test.go`; Modify `README.md`.

- [ ] Teste `TestCenarioSprint`, com binário e daemon reais, papéis renomeados (`EXECUTOR = "FIXER"`) e um repositório git temporário com `backlog/todo/T-001.md` e `T-002.md`:
  1. o adaptador importa as duas tarefas;
  2. GERENTE `sprint abrir --expediente` curto; todos recebem `cerimonia.abertura`;
  3. FIXER1 `proxima` → `T-001`; `reservar`; `concluir`;
  4. REVISOR acorda (`esperar` → `item`); `revisar`; `aprovar`;
  5. INTEGRADOR acorda; `integrar`;
  6. o arquivo vai para `backlog/done` no git → o item segue `integrada`;
  7. `metricas` mostra 1 integrada e latências > 0;
  8. `saude` sem alertas.
- [ ] README: seção "Processo" (papéis, ciclo, `proxima`, sprint, decisões, adaptador, `saude`/`metricas`, importação do `$LOCKS`).
- [ ] `go test -count=1 ./...`, `go vet ./...`, `gofmt -l .` → limpos.
- [ ] Commit `test: sprint end-to-end scenario; docs`.
