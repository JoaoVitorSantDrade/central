# Executor de vagas Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Central abre e supervisiona os agentes: cada vaga é uma sessão do Claude Code CLI, e a Central roda um turno dela (`claude -p --resume`) sempre que a vaga acordaria.

**Architecture:**
- `internal/executor` roda **um** turno, sem banco.
- `core` guarda as vagas executadas, os turnos e os despertares, e deriva o estado delas.
- `service` tem o escalonador (`RunExecutor`), que decide quando rodar, aplica a fila e as falhas e roda no daemon como o medidor.
- CLI, API, MCP e painel só expõem isso.

**Tech Stack:** Go 1.26 (CGO_ENABLED=0), SQLite (modernc), `internal/shell`, Claude Code CLI (`claude -p … --output-format stream-json`).

**Spec:** `docs/superpowers/specs/2026-10-21-executor-de-vagas-design.md`

## Global Constraints
- Tudo é aditivo: `/v1`, `ext/v1` e o `central.toml` continuam compatíveis. Projeto sem `[execucao]` funciona como hoje.
- **Primeiro turno:** `claude -p --session-id <uuid> -w <VAGA> --model M --effort E --permission-mode auto --permission-prompts none --output-format stream-json --verbose "<texto>"`, rodado na raiz do projeto.
- **Turnos seguintes:** `claude -p --resume <uuid>` com as mesmas opções, sem `-w`, rodados na pasta da vaga.
- **Padrões:** `max_simultaneos = 4` e `prazo_turno = "2h"`. Novas tentativas depois de **1, 5 e 15 min**, e suspensão na **3ª** falha seguida. Pausa do plano até a hora de reset, ou **30 min** sem ela. Teto de custo diário opcional (0 = sem teto), com pausa até 00:00 local.
- O log de cada turno fica em `<dados>/projetos/<id>/turnos/<VAGA>/<n>.jsonl`, até **20 MB**. O texto final guardado tem até **2 KB**.
- Só o GERENTE (ou o humano, sem pasta) abre e fecha vagas, dentro de `[execucao.cargos].<CARGO>.max`.
- O texto dos turnos sai só de modelos fixos mais dados da Central. Nada vem de páginas ou de arquivos de fora.
- Commits no repositório público usam autor e committer `JoaoVitorSantDrade <52583265+JoaoVitorSantDrade@users.noreply.github.com>` (`GIT_AUTHOR_*`/`GIT_COMMITTER_*`). A integração na `main` é por fast-forward.

## Review Focus
1. **Vaga executada e saúde:** entre turnos, uma vaga executada nunca aparece como sumida na `saude`, nas `orfas` nem no painel (`TestVagaExecutadaNuncaSumida`, Task 3).
2. **Mensagem durante o turno:** uma mensagem que chega com o turno em curso não dispara outro turno em paralelo, e o próximo turno começa logo que o atual termina (`TestMensagemDuranteTurnoEsperaOProximo`, Task 4).
3. **Daemon reinicia com turno vivo:** o turno não é duplicado e não vira falha; com o processo morto, vira `daemon_reiniciou` (`TestRecuperaTurnoVivoEMorto`, Task 4).
4. **`claude` ausente ou sem login:** `vaga abrir` recusa com uma mensagem clara e não deixa a vaga num estado meio aberto (`TestAbrirSemClaudeRecusa`, Task 5).
5. **Fim da sprint com turno em curso:** o turno termina e nenhum turno novo começa (`TestFimDaSprintFechaVagas`, Task 5).

---

## PR 1: executor e escalonador

### Task 1: `[execucao]` no `central.toml`

**Files:**
- Create: `internal/config/execucao.go`, `internal/config/execucao_test.go`
- Modify: `internal/config/project.go` (o campo `Execucao *ExecucaoSec` no `ProjectConfig` e a chamada da normalização em `ParseProjectConfig`)

**Interfaces:**
- Produces:
  - `type ExecucaoSec struct { Claude string; MaxSimultaneos int; PrazoTurno Duration; CustoDiaUSD float64; Cargos map[string]CargoExec }`, com as tags toml `claude`, `max_simultaneos`, `prazo_turno`, `custo_dia_usd` e `cargos`;
  - `type CargoExec struct { Max int; Modelo, Esforco string; PrazoTurno Duration }`, com as tags `max`, `modelo`, `esforco` e `prazo_turno`;
  - `func (e ExecucaoSec) Cargo(canonico string) (CargoExec, bool)`, que devolve o cargo com o prazo próprio ou herdado.
- Regras:
  - os padrões são `Claude = "claude"`, `MaxSimultaneos = 4` e `PrazoTurno = 2h`;
  - as chaves de `Cargos` são traduzidas para o canônico, pelo mesmo `canonico` de `normalizeProcesso`;
  - `Max ≥ 1`, `Modelo` não vazio e `Esforco ∈ {low, medium, high, xhigh, max}`;
  - prazos entre 1 min e 12 h, `CustoDiaUSD ≥ 0` e `MaxSimultaneos ≥ 1`.

- [ ] **Step 1: Write the failing tests:**
  - `TestExecucaoPadroesETraducao`: `[execucao.cargos] FIXER = { max = 2, modelo = "opus", esforco = "high" }`, com `nomes = { EXECUTOR = "FIXER" }`, dá `Cargo("EXECUTOR")` com `Max == 2`, `PrazoTurno == 2h`, `Claude == "claude"` e `MaxSimultaneos == 4`;
  - `TestExecucaoInvalida`, em tabela: esforço `"turbo"`, `max = 0`, modelo vazio, `prazo_turno = "10s"`, cargo desconhecido e `custo_dia_usd = -1`. Cada caso dá um erro que cita o campo;
  - `TestSemExecucao`: um projeto sem a seção fica com `Execucao == nil`.
- [ ] **Step 2:** `go test ./internal/config -run Execucao` falha (os tipos não existem).
- [ ] **Step 3:** Implementar em `execucao.go` e ligar em `project.go`, depois de `normalizeProcesso`, porque usa os nomes dos papéis.
- [ ] **Step 4:** `go test ./internal/config` passa.
- [ ] **Step 5:** Commit: `feat(config): [execucao] section for the vaga executor`.

### Task 2: `claude-falso` e `internal/executor`

**Files:**
- Create: `examples/claude-falso/main.go`, `internal/executor/executor.go`, `internal/executor/stream.go`, `internal/executor/executor_test.go`

**Interfaces:**
- Produces:
  - `type Turno struct { Claude, Pasta, Sessao, Worktree, Modelo, Esforco, Texto, Log string; Primeiro bool; Prazo time.Duration; Ambiente []string }`;
  - `type Resultado struct { Sessao, Pasta, Texto string; CustoUSD float64; TokensEntrada, TokensSaida int; TurnosModelo int; Negacoes []string; Codigo int; Falha string; ResetEm *time.Time; Stderr string }`;
  - `const FalhaOK, FalhaPrazo, FalhaLimitePlano, FalhaErro, FalhaSemClaude = "", "prazo", "limite_plano", "erro", "sem_claude"`;
  - `func Args(t Turno) []string` (pura) e `func Rodar(ctx context.Context, t Turno, onStart func(pid int)) (Resultado, error)`.
- **`claude-falso`:**
  - grava os argumentos recebidos (um por linha) em `$CLAUDE_FALSO_ARGS`;
  - com `-w <nome>`, cria `<cwd>/.claude/worktrees/<nome>` e informa esse `cwd` no init;
  - responde no stream: `{"type":"system","subtype":"init","session_id":…,"cwd":…}` e depois `{"type":"result","subtype":"success","result":"ok","total_cost_usd":0.01,"num_turns":1,"usage":{"input_tokens":10,"output_tokens":5},"permission_denials":[…]}`;
  - por variável muda o comportamento:
    - `CLAUDE_FALSO_DORMIR` (duração) demora;
    - `CLAUDE_FALSO_SAIR` (código) termina com esse código;
    - `CLAUDE_FALSO_LIMITE` imprime o resultado de erro com `"Claude AI usage limit reached|<epoch>"`;
    - `CLAUDE_FALSO_NEGAR` (texto) põe um item em `permission_denials`.
- **Detecção do limite do plano:** o `result` com `is_error: true` e um texto que contém `usage limit`, case-insensitive. O epoch depois de `|`, quando vem, é o `ResetEm`.
- **Log e resultado:** o stream é copiado para `t.Log` até 20 MB. `Texto` são os últimos 2 KB de `result`.
- **`claude` ausente:** `exec.ErrNotFound` vira `FalhaSemClaude`.
- **Prazo:** o processo roda pelo `internal/shell` (matando a árvore de processos) e, se passa do prazo, a falha é `FalhaPrazo`.

- [ ] **Step 1: Write the failing tests** (o `TestMain` compila o falso uma vez, como o eco):
  - `TestArgsPrimeiroESeguintes`: confere as duas listas das Global Constraints, na ordem, e que `-w` só aparece no primeiro;
  - `TestRodarLeStream`: sessão, `CustoUSD == 0.01`, tokens 10/5, `Falha == ""` e o log escrito;
  - `TestRodarNegacao`: `Negacoes` traz o texto de `CLAUDE_FALSO_NEGAR`;
  - `TestRodarLimitePlano`: `Falha == "limite_plano"` e `ResetEm` igual ao epoch;
  - `TestRodarPrazoMataArvore`: com dormir de 30 s e prazo de 1 s, `Falha == "prazo"` em menos de 10 s;
  - `TestRodarErroESemClaude`: com sair 3, dá `Falha == "erro"` e `Codigo == 3`; com o comando inexistente, dá `"sem_claude"`.
- [ ] **Step 2:** `go test ./internal/executor` falha.
- [ ] **Step 3:** Implementar o falso, `Args`, `Rodar` e a leitura do stream (linha a linha, `json.Unmarshal` por linha, ignorando as linhas que não são JSON).
- [ ] **Step 4:** `go test ./internal/executor` passa.
- [ ] **Step 5:** Commit: `feat(executor): run one Claude Code turn and read its stream`.

### Task 3: vagas executadas no `core`

**Files:**
- Create: `internal/core/vagas_exec.go`, `internal/core/vagas_exec_test.go`
- Modify:
  - `internal/core/schema.go`: migração nova (a próxima do `ProjectMigrations`), com as tabelas `vagas_exec` e `despertares` (colunas da spec §3.2) e as colunas `sprint` (a sprint aberta quando a vaga abriu, ou vazio), `n_turnos`, `custo_dia`, `custo_dia_em`, `plano_pausado_ate` (uma linha só na tabela `executor_estado`);
  - `internal/core/agents.go`: o estado derivado.

**Interfaces:**
- Produces:
  - `type VagaExec struct { Vaga, Papel, Area, Nota, Sessao, Pasta, Modelo, Esforco, Estado, Sprint string; Falhas, NTurnos, PID int; ProximoEm, TurnoDesde, AbertaEm, FechadaEm *time.Time; CustoDia float64 }`;
  - `const VagaAberta, VagaFechada, VagaSuspensa = "aberta", "fechada", "suspensa"` e `EstadoSuspenso = "suspenso"`;
  - funções do `Core`:
    - `AbrirVagaExec(ctx, quem string, v VagaExec) (VagaExec, error)`: insere ou reabre e mantém a sessão de uma vaga que já existe;
    - `FecharVagaExec(ctx, quem, vaga string) (VagaExec, error)`;
    - `VagaExec(ctx, vaga)` e `VagasExec(ctx)`;
    - `IniciarTurno(ctx, vaga, motivo string, pid int) (n int, err error)`;
    - `TerminarTurno(ctx, vaga string, r TurnoFim) (VagaExec, error)`, com `type TurnoFim struct { Falha, Texto, Stderr, Sessao, Pasta string; CustoUSD float64; TokensEntrada, TokensSaida, Codigo int; Negacoes []string; ProximoEm *time.Time; Suspender bool; Dur time.Duration }`;
    - `PausarPlano(ctx, ate time.Time, motivo string) error` e `PlanoPausadoAte(ctx) (time.Time, error)`;
    - `Despertar(ctx, vaga string, em time.Time, motivo string) error` e `DespertaresVencidos(ctx, agora time.Time) (map[string]string, error)`, que devolve vaga → motivo e marca os despertares como feitos;
    - `CustoHoje(ctx) (float64, error)`.
  - Eventos: `vaga.aberta`, `vaga.fechada`, `vaga.suspensa`, `vaga.retomada`, `turno.iniciado`, `turno.terminou`, `turno.falhou`, `plano.pausado` e `plano.retomado`. A suspensão também manda uma mensagem de sistema `vaga.suspensa` ao GERENTE.
- **Estado derivado em `Agents`/`deriveState`:** quando a vaga tem linha em `vagas_exec`, ela nunca é `sumido`.
  - `TurnoDesde != nil`: `trabalhando`;
  - `aberta`: `ocioso`;
  - `suspensa`: `suspenso`;
  - `fechada`: `encerrado`.

- [ ] **Step 1: Write the failing tests:**
  - `TestAbrirFecharReabrirMantemSessao`;
  - `TestTurnoIniciaETermina`: confere `n_turnos`, o custo do dia e os eventos com dados;
  - `TestFalhaESuspensao`: `Suspender` dá o estado `suspensa`, o evento e a mensagem ao GERENTE;
  - `TestDespertaresVencidosMarcaFeito`;
  - `TestPlanoPausado`;
  - **`TestVagaExecutadaNuncaSumida`:** a vaga entrou, ficou 2 h sem batimento e está `aberta`. `Agents` mostra `ocioso`, e `Saude().Sumidos` vem vazio.
- [ ] **Step 2:** `go test ./internal/core -run 'Vaga|Turno|Despert|Plano'` falha.
- [ ] **Step 3:** Implementar a migração, as funções e a derivação. O `deriveState` faz uma consulta a `vagas_exec` pela vaga. Em `Agents`, isso vai num LEFT JOIN no `agentSelect`, como a subconsulta de execuções já faz.
- [ ] **Step 4:** `go test ./internal/core ./internal/process` passa (a `saude` usa o estado novo).
- [ ] **Step 5:** Commit: `feat(core): executed vagas, turns, wake-ups and plan pause`.

### Task 4: o escalonador `RunExecutor`

**Files:**
- Create: `internal/service/executor.go`, `internal/service/executor_test.go`
- Modify: `internal/daemon/daemon.go` (subir `svc.RunExecutor(ctx, 2*time.Second)` junto com os outros laços) e `internal/service/service.go` (o estado dos turnos em curso no `Service`)

**Interfaces:**
- Consumes: `executor.Rodar` e `executor.Args` (Task 2), as funções do `core` da Task 3 e `config.ExecucaoSec` (Task 1).
- Produces:
  - `func (s *Service) RunExecutor(ctx context.Context, intervalo time.Duration)`;
  - `func (s *Service) passoExecutor(ctx context.Context, agora time.Time)`, exportado aos testes do pacote;
  - `var rodarTurno = executor.Rodar`, a costura para os testes.
- **Decisão (spec §3.3):** uma vaga recebe turno quando está `aberta`, sem turno em curso, com `ProximoEm` vencido, sem pausa do plano e sem o teto de custo passado, e quando ela:
  - tem mensagem não lida (`core.Unread` da vaga, ou o primeiro turno quando a vaga ainda não entrou);
  - tem despertar vencido;
  - ou tem `NTurnos == 0`.
- **Fila:** no máximo `MaxSimultaneos` turnos ao mesmo tempo no projeto, com o GERENTE primeiro e depois por `ProximoEm`/`AbertaEm`.
- **Textos dos turnos**, em `textoTurno(v VagaExec, motivo string, n int, primeiro bool, falhaAnterior string) string` (as constantes ficam em `executor.go`):
  - **primeiro turno:** `Você é a vaga <V> (papel <P>[, área <A>]) do projeto <id>. Rode central entrar --vaga <V> --papel <P>[ --area <A>] e depois central brief --md, e siga o seu cartão.[ Nota do Gerente: <nota>] Ao terminar uma unidade de trabalho, termine o turno: a Central acorda você quando houver algo. Para esperar algo sem mensagem, use central despertar --em <duração> "<motivo>".`;
  - **turnos seguintes:** `Você acordou: <motivo> (<n> mensagens). Comece por central caixa.`, com o prefixo `O turno anterior terminou por <falha>; confira onde parou. ` quando o anterior falhou.
- **Pasta e ambiente:** o primeiro turno roda na raiz registrada do projeto com `Worktree = <VAGA>`. A pasta devolvida no init é gravada pelo `TerminarTurno`. O ambiente leva `CENTRAL_ADDR`.
- **Depois do turno:**
  - `ok`: as falhas voltam a 0;
  - `prazo`, `erro` ou `sem_claude`: as falhas sobem, com `ProximoEm` = agora + [1 m, 5 m, 15 m][falhas-1], e `Suspender` na 3ª;
  - `limite_plano`: `PausarPlano(ResetEm` ou agora+30 m), sem somar falha.
- **Teto de custo:** passado o `CustoDiaUSD`, `PausarPlano` até 00:00 local.
- **Recuperação:** no início de cada passo, um turno em curso cujo pid não existe mais (`processoVivo(pid) bool`, por plataforma) vira `TerminarTurno` com a falha `daemon_reiniciou`, quando a goroutine não é deste processo, ou `processo_sumiu`.

- [ ] **Step 1: Write the failing tests** (o `rodarTurno` é trocado por um falso em memória, e o pid é o do próprio teste):
  - `TestPrimeiroTurnoNaRaizComWorktree`;
  - `TestTurnoPorMensagem`;
  - `TestTurnoPorDespertar`;
  - **`TestMensagemDuranteTurnoEsperaOProximo`;**
  - `TestFilaRespeitaMaxSimultaneosEGerentePrimeiro`;
  - `TestTresFalhasSuspendem`: confere os intervalos de 1, 5 e 15 min;
  - `TestLimitePlanoPausaTodas`;
  - `TestTetoDeCustoPausa`;
  - **`TestRecuperaTurnoVivoEMorto`.**
  
  Um teste de integração, `TestExecutorComClaudeFalso`, usa o binário da Task 2 de verdade: dois turnos, sessão e pasta gravadas.
- [ ] **Step 2:** `go test ./internal/service -run Executor` falha.
- [ ] **Step 3:** Implementar e ligar no daemon. A espera do passo é `select` em `Changed()` e no tique.
- [ ] **Step 4:** `go test ./internal/service ./internal/daemon` passa.
- [ ] **Step 5:** Commit: `feat(service): vaga executor scheduler (wake, queue, retries, plan pause, recovery)`.
- [ ] **Step 6:** Revisão final do PR 1, registro, PR e fast-forward na `main`.

## PR 2: comandos, sprint, MCP e brief

### Task 5: abrir, fechar, listar, despertar e a sprint

**Files:**
- Create: `internal/process/vagas.go`, `internal/process/vagas_test.go`, `internal/api/vagas.go`, `internal/cli/vagas_cmds.go`, `internal/cli/m13_e2e_test.go`
- Modify: `internal/service/process.go` (os métodos `AbrirVaga`, `FecharVaga`, `Vagas`, `Despertar` e `VagaHumano`), `internal/process/sprint.go` (`AbrirSprint` abre a GERENTE1), `internal/service/executor.go` (o fim da sprint fecha as vagas), `internal/cli/mcp.go`, `internal/config/extensions.go` (`ComandosReservados`: `vaga`, `vagas`, `despertar`) e `internal/api/testdata/rotas-v1.golden.txt` (regravado, aditivo)

**Interfaces:**
- Produces:
  - `func (e *Engine) AbrirVaga(ctx, quem string, vaga, papel, area, nota string, exec config.ExecucaoSec, claudeOK error) (core.VagaExec, error)`. Quem chama tem de ser o GERENTE ou `core.Humano`. O papel tem de ter `Cargo`. As vagas abertas do cargo têm de ser menos que o `Max`, senão dá `ErrConflict` com o teto na mensagem. Com `claudeOK != nil`, recusa com `ErrInvalid` sem gravar nada;
  - `func (e *Engine) FecharVaga(ctx, quem, vaga string) (core.VagaExec, error)`;
  - `func (s *Service) ClaudeDisponivel(ctx, projetoID string) error`, que roda `<claude> auth status`. O resultado fica em cache por 5 min;
  - CLI:
    - `central vaga abrir <VAGA> --papel P [--area A] [--nota T]` e `central vaga fechar <VAGA>`;
    - `central vagas`;
    - `central despertar --em <duração> <motivo>`, com duração entre 1 min e 24 h, como a vaga da pasta;
  - API:
    - `POST /v1/projetos/{id}/vagas {pasta, vaga, papel, area, nota}`: sem pasta, quem age é o humano;
    - `POST /v1/projetos/{id}/vagas/{vaga}/fechar {pasta}`;
    - `GET /v1/projetos/{id}/vagas`;
    - `POST /v1/projetos/{id}/despertar {pasta, em, motivo}`;
  - MCP: `vaga_abrir`, `vaga_fechar`, `vagas` e `despertar`.
- **Sprint:**
  - `AbrirSprint`: num projeto com `[execucao]` e um cargo GERENTE, abre a GERENTE1 se ela não está aberta. Erros do executor não impedem a sprint: viram um aviso no retorno (`Sprint.Avisos []string`).
  - **Fim da sprint:** o escalonador fecha as vagas abertas cuja `Sprint` já está encerrada. O turno em curso termina, e nenhum novo começa.

- [ ] **Step 1: Write the failing tests:**
  - `TestAbrirVagaSoGerenteDentroDoTeto`;
  - `TestReabrirRetomaSessao`;
  - **`TestAbrirSemClaudeRecusa`;**
  - **`TestFimDaSprintFechaVagas`;**
  - `TestSprintAbrirAbreGerente1`;
  - os e2e `TestVagaCLI` e `TestDespertarCLI`;
  - `TestMCPVagas`;
  - `TestRotasV1Golden`, regravado.
- [ ] **Step 2:** Os testes falham.
- [ ] **Step 3:** Implementar.
- [ ] **Step 4:** `go test ./...` passa.
- [ ] **Step 5:** Commit: `feat: vaga abrir/fechar, vagas, despertar; sprint opens GERENTE1 and closing it closes vagas`.

### Task 6: o fim de ciclo das vagas executadas

**Files:**
- Modify: `internal/contextpack/cards.go`, `internal/contextpack/brief.go`, `internal/service/brief.go`, `integrations/claude-code/skills/central/SKILL.md` e `internal/contextpack/cards_test.go`

**Interfaces:**
- Produces:
  - `const FimDeTurno = "Ao terminar uma unidade de trabalho, termine o turno: a Central acorda você quando houver algo. Para esperar algo sem mensagem, use ` + "`central despertar --em <duração> \"<motivo>\"`" + `. Depois de um resumo automático da conversa, ` + "`central brief --curto --md`" + ` devolve onde você estava."`;
  - `Card(pasta, papelProjeto, papelCanonico string, executada bool)`: com `executada`, usa `FimDeTurno` no lugar de `FimDeCiclo`. Um cartão que já fala em `central esperar` ou em `termine o turno` fica sem nenhum dos dois.
- **Brief:** `service.Brief` passa `executada = (a vaga tem linha aberta em vagas_exec)`.
- **Skill:** ganha a seção "Se a Central executa a sua vaga".

- [ ] **Step 1: Write the failing tests:** `TestCartaoDeVagaExecutadaTerminaOTurno` e `TestBriefDeVagaExecutada`, este no serviço (o brief contém `termine o turno` e não contém `central esperar`).
- [ ] **Step 2:** Os testes falham.
- [ ] **Step 3:** Implementar.
- [ ] **Step 4:** `go test ./internal/contextpack ./internal/service ./internal/cli` passa.
- [ ] **Step 5:** Commit: `feat(contextpack): executed vagas end the turn instead of waiting`.
- [ ] **Step 6:** Revisão final do PR 2, registro, PR e fast-forward.

## PR 3: painel, doctor, documentação e release

### Task 7: o painel

**Files:**
- Modify: `internal/web/static/app.js`, `internal/web/static/app.css`, `internal/api/vagas.go` (`POST /v1/projetos/{id}/vagas/{vaga}/app`) e `internal/service/process.go` (`AbrirNoApp(ctx, projetoID, vaga string) error`, que roda `<claude> --desktop --resume <sessão>` desanexado)

**Interfaces:**
- Consumes: `GET /vagas` (Task 5) e `PlanoPausadoAte` (Task 3), exposto como `plano_pausado_ate` no `GET /vagas`.
- **Aba Agora:**
  - uma coluna "Execução" na tabela de vagas, para as executadas: "turno desde HH:MM" ou "aguardando", com `n` turnos e `US$ x` hoje, e a última negação em vermelho atenuado;
  - os botões Fechar, Reabrir (quando fechada ou suspensa) e Abrir no app;
  - uma faixa de aviso no topo enquanto o plano está pausado ("Execução pausada até HH:MM: limite do plano").

- [ ] **Step 1: Write the failing test:** `TestAbrirNoAppRodaDesktop`, com o `claude` falso. Os argumentos gravados são `--desktop --resume <sessão>`.
- [ ] **Step 2:** O teste falha.
- [ ] **Step 3:** Implementar o endpoint e a interface.
- [ ] **Step 4:** O teste passa. Verificação visual no navegador embutido, num daemon de demonstração com o `claude` falso: vagas trabalhando, aguardando e suspensa, a faixa de pausa, e os botões funcionando.
- [ ] **Step 5:** Commit: `feat(web): executed vagas in the Agora tab`.

### Task 8: doctor, documentação e release 1.3.0

**Files:**
- Modify: `internal/cli/doctor.go` (as verificações `executor` e `executor:login` quando o projeto local tem `[execucao]`), `internal/cli/m13_e2e_test.go`, `README.md` (a seção "Executor de vagas"), `CHANGELOG.md` (1.3.0), `internal/version/version.go` e o teste dele (1.3.0), `integrations/claude-code/.claude-plugin/plugin.json` (1.3.0) e `docs/registro/v1.3.md`

- [ ] **Step 1: Write the failing test:** `TestDoctorExecutor`. Com o falso no `[execucao] claude`, as duas verificações dão ok. Com um comando inexistente, `executor` dá falha, citando o comando.
- [ ] **Step 2:** O teste falha.
- [ ] **Step 3:** Implementar e escrever a documentação, com exemplo de `[execucao]`, o ciclo, as falhas e a pausa, e os comandos.
- [ ] **Step 4:** `go test ./...` passa. Teste real opcional com `CENTRAL_TESTE_CLAUDE_REAL=1` (`TestClaudeReal`, um turno com `--model haiku` e o texto "responda ok"), se o `claude` estiver instalado.
- [ ] **Step 5:** Revisão final, registro, PR, fast-forward, a tag `v1.3.0` e `scripts/release.ps1 -Versao 1.3.0` com `gh release create`.
