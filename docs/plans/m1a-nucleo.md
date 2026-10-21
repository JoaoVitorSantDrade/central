# Central M1a (núcleo completo) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Completar o núcleo da spec §5 sobre a base do M0: estado dos agentes, respostas com prazo, mensagens do sistema, travas, recursos com dimensões (admissão declarada + medida, fila, backfill, aprendizado, concessão sem uso, prazo), execuções longas (`rodar`), fatos, ids, configuração do projeto no daemon, backups e `doctor`.

**Architecture:** Mesmas camadas do M0 (spec §4). Toda regra nova fica em `core` (genérico) e é exposta por `service` → `api` → `cli`. O `central.toml` é lido pela CLI e enviado ao daemon no `entrar`; o daemon guarda a última versão válida por projeto. Tudo que precisa "acordar" um agente vira **mensagem do sistema** (remetente `CENTRAL`), então o `esperar` continua sendo um só mecanismo; o `motivo` devolvido é derivado do tipo da primeira mensagem não lida. Um relógio do serviço (`Tick`, a cada 2 s) cuida de prazos, expirações e admissão; os medidores rodam em goroutines do daemon.

**Tech Stack:** o do M0 (Go 1.26+, `CGO_ENABLED=0`, `modernc.org/sqlite`, `go-toml/v2`, `gofrs/flock`).

**Spec:** `docs/spec.md` §5 (núcleo), §5.3 (recursos), §5.4 (execuções), §6.6 (comandos e códigos), §10 (`doctor`), §12 (linha M1). O resultado do piloto está em `docs/piloto-m0.md`.

## Global Constraints

- Tudo do M0 continua valendo: loopback, `CENTRAL_HOME`/`CENTRAL_ADDR`, saída JSON em uma linha e códigos `0/1/2/3/4/5`. Erros `core.ErrConflict` → HTTP 409 → saída 2.
- **Nada de domínio no `core`** (spec §4, regra 3). GPU, VRAM e nvidia-smi só aparecem em testes como exemplo de configuração.
- **Remetente reservado `CENTRAL`** (`core.Sistema`). `Enter` recusa as vagas `TODOS` e `CENTRAL`.
- Uma migração nova (a 3) cria tudo do M1a. Nunca edite as migrações 1 e 2.
- Valores padrão: `sumido_apos = 30m`, intervalo do medidor `15s`, `prazo_max = 30m`, liberação `10m` depois do prazo sem batimento, `sem_uso.apos = 5m`, folga da estimativa `+20 %` sobre o maior pico das últimas 5 execuções, `Tick` a cada `2s`, backup a cada `1h` mantendo `48`, amostras guardadas por `7 dias`, log girado acima de `10 MB` mantendo 3.
- Horários gravados em UTC, RFC 3339 com milissegundos, em todos os bancos (inclusive `registro.db`).
- Toda chamada identificada por `pasta` renova o batimento da vaga (spec §5: "todo comando do agente renova o batimento").

## Decisões deste plano (onde a spec é omissa)

1. **Configuração:** o `entrar` envia o texto do `central.toml`. O daemon valida e guarda (tabela `config`, com hash). Com o arquivo inválido, o `entrar` falha com código 4 e o erro com a linha, e o daemon mantém a configuração anterior.
2. **Acordar = mensagem do sistema.** Concessão ativada, prazo de resposta vencido, prazo de recurso, concessão sem uso, liberação por expiração e agente sumido (aviso ao papel `GERENTE` se existir, senão a `TODOS`) geram mensagens de `CENTRAL` com tipos `recurso.concedida`, `recurso.prazo`, `recurso.liberada`, `recurso.sem_uso`, `resposta.prazo` e `agente.sumiu`. `WaitResult.Motivo` = parte antes do ponto do tipo da primeira não lida, se ela vier de `CENTRAL`; senão `mensagem`.
3. **Travas** não têm prazo próprio: valem enquanto o dono não estiver `sumido` nem `encerrado`.
4. **Dimensões na CLI** são genéricas: `--dims vram_mb=4500,uso_pct=80` (a spec usa `--vram` como exemplo).
5. **Prazo de recurso:** `--min` acima do teto (`prazo_max`, ou `prazo_max_por_prioridade[p]`) é recusado (4) com a sugestão de dividir. Sem `--min`, usa o teto.
6. **Pico de uma concessão** = maior leitura menos `reservado`, por dimensão (nunca negativo). Ele só alimenta o histórico se a concessão ficou sozinha durante toda a vigência.
7. **Concessão sem uso:** precisa de `sem_uso = { dimensao, limite, apos }` no recurso. Só vale para concessões que declararam valor > 0 nessa dimensão. Dispara quando a leitura menos o reservado fica abaixo de `limite` continuamente por `apos`, uma vez por concessão.
8. **Medidor sem leitura recente** (nenhuma amostra nos últimos 3 intervalos): a admissão usa só a conta declarada e grava o evento `medidor.sem_leitura` (no máximo um por minuto).
9. Os menores pendentes do M0 entram na Task 1.

## Review Focus

- **Medidor que trava ou imprime lixo** (timeout, texto não numérico, colunas a menos): a amostra é descartada, grava-se o evento, a admissão segue só pela conta declarada, e o daemon não para. → `TestMedidorSaidaInvalida` e `TestMedidorTimeout` (Task 8).
- **Dono de uma concessão que morre sem soltar:** depois do prazo, aviso; 10 min depois, sem batimento, a concessão é liberada e o primeiro da fila entra. As travas do dono caem quando ele fica `sumido`. → `TestConcessaoExpiraSemBatimento` (Task 7) e `TestTravaCaiComDonoSumido` (Task 5).
- **Daemon reiniciado com concessões ativas e fila:** estado, fila e prazos sobrevivem; a admissão continua no primeiro `Tick`. → `TestConcessoesSobrevivemReinicio` (Task 7).
- **`central.toml` inválido editado num worktree:** o `entrar` sai com 4 e a linha do erro, e a configuração anterior continua valendo para todos. → `TestConfigInvalidaMantemAnterior` (Task 2).
- **`rodar` com comando inexistente, filho que falha ou `Ctrl+C`:** a execução é encerrada com o código, a concessão é solta e o código de saída é o do filho (ou 1 se não iniciou). → `TestRodarComandoInexistente`, `TestRodarPropagaCodigo` e `TestRodarSoltaRecursoNoFim` (Task 9).

---

### Task 1: Menores pendentes do M0

**Files:**
- Modify: `internal/store/store.go`, `internal/service/service.go`, `internal/api/server.go`, `internal/cli/cli.go`, `internal/cli/client.go`, `internal/cli/autostart.go`, `internal/cli/autostart_windows.go`, `internal/api/server_test.go`, `internal/cli/cli_e2e_test.go`
- Test: `internal/store/store_test.go`, `internal/cli/unit_test.go`, `internal/api/server_test.go`

**Interfaces:**
- Produces: `GET /v1/saude` passa a devolver também `"dados": <pasta de dados absoluta>` e `"pid"`; `service.New` guarda `dataDir` absoluto.

- [ ] **Step 1: Testes:**
  - `TestTxImediata` (store): duas goroutines com `BeginTx` + `UPDATE` no mesmo banco aberto **duas vezes** (dois `*sql.DB`) não dão `SQLITE_BUSY`; prova o `_txlock=immediate`.
  - `TestRegistroGravaMsEMantemRaiz` (service): o `ultimo_acesso` casa com `\.\d{3}Z$`; um segundo `Enter` com outra `raiz` mantém a primeira.
  - `TestSaudeInformaDados` (api): `dados` é igual ao `dataDir` passado.
  - `TestEsperarRestoMinimo` (cli, unidade): com o prazo a menos de 1 ms, `wait` devolve `motivo=tempo` sem chamar o servidor (servidor de teste que falha se for chamado).
  - `TestEnsureDaemonRecusaOutraPastaDeDados` (cli, unidade): o servidor de teste responde `saude` com `dados` diferente de `config.DataDir()` → saída 5 com `daemon_indisponivel` e as duas pastas na mensagem.
  - `TestErroDoServicoSemVocabularioDaCLI` (service): a mensagem de pasta desconhecida não contém `central entrar`; a CLI acrescenta a dica (`TestSemEntrarSai3` confere `central entrar` na mensagem final).
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar:
  - DSN com `&_txlock=immediate`, mais o comentário de que a entrega exatamente uma vez depende disso;
  - `service.stamp` no layout de milissegundos; `raiz` só no `INSERT`;
  - `saude` com `dados` e `pid`;
  - em `wait`, `remaining < time.Millisecond` → `tempo`;
  - `ensureDaemon` compara `dados` (com `filepath.Clean` e `strings.EqualFold` no Windows);
  - no Windows, `CREATE_BREAKAWAY_FROM_JOB` (`0x01000000`) na primeira tentativa de `Start`, refazendo sem ela se der erro;
  - testes do M0: `enviar` em goroutine sem `t.Fatalf` (canal de erro), `sent` protegido por canal, guarda de `central.toml` no ancestral em `TestForaDeProjetoSai3`, limite de `TestEsperarAcordaEmMenosDeUmSegundo` em 2 s.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Commit** `fix: m0 deferred minors`.

### Task 2: Configuração do projeto no daemon

**Files:**
- Create: `internal/config/project.go`, `internal/service/config.go`
- Modify: `internal/core/schema.go` (migração 3: só a tabela `config` nesta task; as outras tabelas entram nas tasks seguintes como **parte da mesma migração 3**, que só é publicada no fim do M1a), `internal/service/service.go`, `internal/api/server.go`, `internal/cli/cli.go`
- Test: `internal/config/project_test.go`, `internal/service/config_test.go`

**Interfaces:**
- Produces (config):
  - `type Duration struct{ time.Duration }` com `UnmarshalText`.
  - `type ProjectConfig struct { Projeto ProjetoSec; Agentes AgentesSec; Recursos map[string]ResourceSec }`; `AgentesSec{ SumidoApos Duration \`toml:"sumido_apos"\` }`.
  - `type ResourceSec struct { Dimensoes, Reservado, Margem, TetoMedido map[string]float64; Medidor *MeterSec; PrazoMax Duration; PrazoMaxPorPrioridade map[string]Duration; Prioridades []string; Tipos map[string]map[string]any; SemUso *SemUsoSec }`, com tags TOML iguais aos nomes da spec §5.3 (`dimensoes`, `reservado`, `margem`, `teto_medido`, `medidor`, `prazo_max`, `prazo_max_por_prioridade`, `prioridades`, `tipos`, `sem_uso`); `MeterSec{ Comando string; Mapa []string; Intervalo Duration }`; `SemUsoSec{ Dimensao string; Limite float64; Apos Duration }`.
  - `func ParseProjectConfig(data []byte) (ProjectConfig, error)`: aplica os padrões das Global Constraints e valida (erro com o nome do recurso e do campo; erro de TOML com a linha).
  - `func (r ResourceSec) Spec(nome string) (core.ResourceSpec, error)`: converte para o tipo do núcleo (Task 6 define `core.ResourceSpec`; nesta task, `ParseProjectConfig` só valida).
- Produces (service): `type EnterInput struct{ ProjetoID, Nome, Raiz, Pasta, Vaga, Papel, Config string }`; `func (s *Service) Enter(ctx, in EnterInput) (core.Agent, error)` substitui a assinatura do M0. `func (s *Service) ProjectConfig(ctx, projetoID) (config.ProjectConfig, error)`.
- Validações: `dimensoes` não vazio e valores > 0; chaves de `reservado`, `margem`, `teto_medido`, `medidor.mapa`, `sem_uso.dimensao` e as dimensões dos tipos ⊂ `dimensoes`; `teto_medido` ∈ (0,1]; `prioridades` não vazia e sem repetição; chaves de `prazo_max_por_prioridade` ⊂ `prioridades`; tipo: `exclusivo` bool ou dimensões ≤ `dimensoes − reservado`.
- Tabela `config(versao INTEGER PRIMARY KEY AUTOINCREMENT, texto TEXT NOT NULL, hash TEXT NOT NULL, vaga TEXT NOT NULL, quando TEXT NOT NULL)`. Grava só quando o hash muda; evento `config.atualizada`. A configuração vigente é a última linha. Ao abrir um projeto, o serviço carrega e aplica a vigente.
- API: `POST .../agentes` aceita `"config"`. CLI `entrar` lê `<raiz>/central.toml` e o envia.

- [ ] **Step 1: Testes:**
  - `TestParseConfigCompleta`: o exemplo da spec §5.3, com `sem_uso` e `prazo_max_por_prioridade = { versao = "120m" }`, resulta nos valores esperados (ex.: `Recursos["gpu"].Dimensoes["vram_mb"] == 8192`, `Medidor.Intervalo == 15s`, `Tipos["e2e-nivel2"]["exclusivo"] == true`).
  - `TestParseConfigPadroes`: só `[projeto]` → `SumidoApos == 30m`, sem recursos.
  - `TestParseConfigInvalida` (tabela de casos): reservado com dimensão desconhecida; `teto_medido = 1.5`; prioridade repetida; tipo maior que a capacidade livre; TOML quebrado (a mensagem contém `line`).
  - `TestConfigInvalidaMantemAnterior` (service): `Enter` com configuração válida A e depois inválida B → o segundo `Enter` devolve `core.ErrInvalid` e `ProjectConfig` ainda é A.
  - `TestConfigSoGravaQuandoMuda`: dois `Enter` com o mesmo texto → uma linha em `config`.
  - `TestConfigRecarregadaAoReabrir`: fecha e reabre o serviço → `ProjectConfig` igual.
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat(config): project config sent on entrar and kept by the daemon`.

### Task 3: Estado dos agentes

**Files:**
- Create: `internal/core/agents.go` (mover `Enter`, `AgentByPasta` e o `scanAgent` de `core.go`)
- Modify: `internal/core/schema.go` (migração 3: `ALTER TABLE agentes ADD estado TEXT NOT NULL DEFAULT 'ativo'`, `ADD status TEXT NOT NULL DEFAULT ''`, `ADD sumido_avisado INTEGER NOT NULL DEFAULT 0`), `internal/service/service.go`, `internal/api/server.go`, `internal/cli/cli.go`
- Test: `internal/core/agents_test.go`, `internal/cli/cli_e2e_test.go`

**Interfaces:**
- Produces (core):
  - `Agent` ganha `Estado string \`json:"estado"\`` e `Status string \`json:"status"\``.
  - `type Settings struct{ SumidoApos time.Duration }`; `func (c *Core) SetSettings(s Settings)`. A Task 6 acrescenta o campo `Resources map[string]ResourceSpec`.
  - `func (c *Core) Touch(ctx, vaga string) error`
  - `func (c *Core) SetStatus(ctx, vaga, estado, status string) (Agent, error)`: `estado` ∈ {`""`, `ativo`, `ocioso`, `trabalhando`}; `""` mantém o atual.
  - `func (c *Core) Leave(ctx, vaga string) (Agent, error)`: `encerrado`, solta travas e concessões da vaga (depois das Tasks 5 e 7), evento `agente.saiu`.
  - `func (c *Core) Agents(ctx) ([]Agent, error)`: ordenado por vaga; `Estado` derivado `sumido` quando não está `encerrado`, o batimento é mais antigo que `SumidoApos` e não há execução com batimento recente (Task 9).
  - `const Sistema = "CENTRAL"`.
- Produces (service/api/cli): toda operação com `pasta` chama `Touch` (também `Send` e `Wait`, no início e a cada retorno). `GET .../agentes`, `POST .../status {pasta, estado, status}`, `POST .../sair {pasta}`. CLI: `central status [--estado E] "<texto>"`, `central sair` e `central agentes`.

- [ ] **Step 1: Testes:**
  - `TestTouchRenovaBatimento` (relógio controlável no teste: `now` é um ponteiro que o teste avança).
  - `TestSendEWaitRenovamBatimento` (service).
  - `TestSumidoDerivado`: `SumidoApos = 30m`; o relógio avança 31 min → `Agents()` mostra `sumido`; `Touch` → `ativo` de novo.
  - `TestSetStatusValida`: `estado = "dormindo"` → `ErrInvalid`.
  - `TestLeaveEncerra`: `Leave` → `encerrado`; `encerrado` nunca vira `sumido`.
  - `TestEnterRecusaReservados`: `TODOS` e `CENTRAL` → `ErrInvalid`.
  - `TestAgentesStatusSair` (cli e2e): `status --estado trabalhando "rodando gate"` → `agentes` mostra o texto e o estado; `sair` → `encerrado`.
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat(core): agent state, status, leave and derived sumido`.

### Task 4: Respostas com prazo, mensagens do sistema e `Tick`

**Files:**
- Create: `internal/core/replies.go`, `internal/core/tick.go`, `internal/service/tick.go`
- Modify: `internal/core/core.go`, `internal/core/schema.go` (migração 3: `ALTER TABLE mensagens ADD responde_a INTEGER`, `ADD responder_ate TEXT`, `ADD prazo_avisado INTEGER NOT NULL DEFAULT 0`), `internal/service/service.go`, `internal/api/server.go`, `internal/cli/cli.go`, `internal/daemon/daemon.go`
- Test: `internal/core/replies_test.go`, `internal/service/tick_test.go`

**Interfaces:**
- Produces (core):
  - `type SendInput struct{ De, Para, Tipo, Ref, Corpo, Chave string; RespondeA int64; ResponderEm time.Duration }`; **`func (c *Core) Send(ctx, in SendInput) (Message, error)`** substitui a assinatura posicional do M0 (atualize os chamadores e os testes).
  - `Message` ganha `RespondeA int64 \`json:"responde_a,omitempty"\`` e `ResponderAte *time.Time \`json:"responder_ate,omitempty"\``.
  - `func (c *Core) Reply(ctx, deVaga string, seq int64, corpo string) (Message, error)`: a resposta vai para o remetente original, com `tipo = "resposta"`, o mesmo `ref` e `RespondeA = seq`. Só quem recebeu a mensagem pode responder (senão `ErrInvalid`).
  - `type Pendencias struct{ Devo, Esperam []Message }` (tags `devo`, `esperam`); `func (c *Core) Pending(ctx, vaga string) (Pendencias, error)`: mensagens com `responder_ate` não nulo, ainda sem resposta (nenhuma mensagem com `responde_a = seq`). `Devo` são as endereçadas a mim; `Esperam` são as que eu enviei.
  - `func (c *Core) FirstUnread(ctx, vaga string) (Message, int, error)`
  - `func (c *Core) Tick(ctx context.Context) error`: nesta task, avisa prazos de resposta vencidos (mensagem de `CENTRAL` para o `para` original, tipo `resposta.prazo`, `ref` = seq original, uma vez) e avisa sumidos (tipo `agente.sumiu`, uma vez por sumiço, para `GERENTE` se houver vaga com esse papel, senão `TODOS`).
  - `func (c *Core) sendSystem(ctx, tx *sql.Tx, para, tipo, ref, corpo string) error` (interno) e `Broadcast` depois do commit.
- Produces (service): `WaitResult` ganha `Primeira *core.Message \`json:"primeira,omitempty"\``; `Motivo` é derivado como na Decisão 2. `func (s *Service) Tick(ctx) error` percorre os projetos abertos; `func (s *Service) OpenAll(ctx) error` abre todos os registrados (o daemon chama ao subir). `func (s *Service) RunTicker(ctx, every time.Duration)`.
- API/CLI: `enviar ... [--responder 30m]`; `POST .../responder {pasta, seq, corpo}`; `GET .../pendentes?pasta=`. CLI `central responder <seq> <corpo|->` e `central pendentes`.
- Daemon: `OpenAll` ao subir e `RunTicker(ctx, 2*time.Second)` numa goroutine.

- [ ] **Step 1: Testes:**
  - `TestResponderFechaPendencia`: A envia a B com `ResponderEm: 30m` → `Pending(B).Devo` e `Pending(A).Esperam` têm a mensagem; B responde → as duas vazias; a caixa de A tem `tipo = resposta`, `responde_a = seq`.
  - `TestResponderPorPapelUmBasta`: mensagem a `REVISOR` com prazo; REVISOR2 responde → pendência fechada para REVISOR1 também.
  - `TestSoDestinatarioResponde`: C (não destinatário) → `ErrInvalid`.
  - `TestTickAvisaPrazoUmaVez`: o relógio passa do prazo; `Tick` duas vezes → uma mensagem `CENTRAL`/`resposta.prazo` na caixa de B.
  - `TestTickAvisaSumidoAoGerente`: com uma vaga GERENTE, o FIXER some → mensagem `agente.sumiu` com `ref` = vaga para GERENTE, uma vez; o FIXER volta e some de novo → nova mensagem.
  - `TestMotivoDerivado` (service): a primeira não lida de `CENTRAL` com tipo `recurso.concedida` → `Motivo == "recurso"`; de um agente → `mensagem`; `Primeira.Seq` correto.
  - `TestTickerChamaTick` (service): `RunTicker` com `every = 10ms` avisa um prazo vencido em < 500 ms.
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat(core): replies with deadlines, system messages and service tick`.

### Task 5: Travas

**Files:**
- Create: `internal/core/locks.go`
- Modify: `internal/core/schema.go` (migração 3: `travas(nome TEXT PRIMARY KEY, vaga TEXT NOT NULL, motivo TEXT NOT NULL, inicio TEXT NOT NULL)`), `internal/core/core.go` (`ErrConflict`), `internal/api/errors.go`, `internal/api/server.go`, `internal/cli/cli.go`, `internal/core/agents.go` (`Leave` solta)
- Test: `internal/core/locks_test.go`

**Interfaces:**
- Produces: `var ErrConflict`; `type Lock struct{ Nome, Vaga, Motivo string; Inicio time.Time; DonoEstado string }` (tags `nome`, `vaga`, `motivo`, `inicio`, `dono_estado`); `func (c *Core) Lock(ctx, vaga, nome, motivo string) (Lock, error)`; `func (c *Core) Unlock(ctx, vaga, nome string) error`; `func (c *Core) Locks(ctx) ([]Lock, error)`.
- Regras: livre ou com dono `sumido`/`encerrado` → toma (evento `trava.tomada`, com `de` = dono anterior se havia). Com dono vivo e outra vaga → `ErrConflict` com o dono na mensagem. Mesmo dono → idempotente. `Unlock` de quem não é dono → `ErrConflict`; de trava inexistente → `ErrNotFound`. `nome` com `^[A-Za-z0-9._/-]{1,128}$`.
- API: `POST .../travas {pasta, nome, motivo}`, `DELETE .../travas/{nome}?pasta=` e `GET .../travas`. CLI: `central trava <nome> --motivo "<m>"`, `central soltar <nome>` e `central travas`.

- [ ] **Step 1: Testes:** `TestTravaExclusiva` (B recebe `ErrConflict`, e a mensagem contém `A`); `TestTravaIdempotente`; `TestSoltarSoDono`; `TestTravaCaiComDonoSumido` (o relógio passa de `SumidoApos` sem batimento de A → B toma, com evento `trava.tomada` e `de = A`); `TestLeaveSoltaTravas`; `TestNomeDeTravaInvalido`; `TestTravaCLI` (e2e: a segunda vaga sai com 2).
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat(core): locks tied to owner liveness`.

### Task 6: Admissão de recursos (função pura)

**Files:**
- Create: `internal/core/resources_types.go`, `internal/core/admission.go`
- Modify: `internal/config/project.go` (`ResourceSec.Spec`), `internal/core/agents.go` (`Settings.Resources`), `internal/service/config.go` (aplica `Resources` em `SetSettings`)
- Test: `internal/core/admission_test.go`, `internal/config/project_test.go`

**Interfaces:**
- Produces:
  - `type Dims map[string]float64`
  - `type TipoSpec struct{ Dims Dims; Exclusivo bool }`; `type SemUsoSpec struct{ Dimensao string; Limite float64; Apos time.Duration }`
  - `type ResourceSpec struct{ Nome string; Dimensoes, Reservado, Margem, TetoMedido Dims; Prioridades []string; PrazoMax time.Duration; PrazoMaxPorPrioridade map[string]time.Duration; Tipos map[string]TipoSpec; SemUso *SemUsoSpec; MedidorIntervalo time.Duration }` (`MedidorIntervalo == 0` quer dizer sem medidor)
  - `type Grant struct{ ID int64; Recurso, Vaga, Tipo, Prioridade, Motivo, Estado, FimMotivo, Estimativa string; Declarado, Pico Dims; Exclusivo, Sozinha bool; Duracao time.Duration; PedidoEm time.Time; Inicio, Prazo, Fim *time.Time }`, com tags JSON em snake_case português; `Estado` ∈ {`fila`, `ativa`, `encerrada`}; `Estimativa` ∈ {`declarada`, `historico`, `tipo`, `exclusivo`}.
  - `func SortQueue(spec ResourceSpec, fila []Grant)`: índice da prioridade, depois `PedidoEm`, depois `ID`.
  - `type AdmissionInput struct{ Spec ResourceSpec; Ativas, Fila []Grant; Medido Dims; Now time.Time }` (`Medido == nil` = sem leitura recente)
  - `func Admit(in AdmissionInput) []int64`: os IDs da fila a ativar agora, em ordem.
  - `func EstimatedStart(spec ResourceSpec, ativas []Grant, head Grant, now time.Time) time.Time`
- Algoritmo de `Admit` (simule ativando um por vez, atualizando a soma declarada e somando o pedido ao medido):
  1. Percorra a fila ordenada. A cabeça entra se `cabe(g)`. Se não couber, calcule `inicio := EstimatedStart(cabeça)` e, para os seguintes, só entra quem `cabe(g)` **e** `now + g.Duracao ≤ inicio`.
  2. `cabe(g)`: (a) nenhum ativo exclusivo; (b) se `g.Exclusivo`, nenhum ativo; (c) para cada dimensão d de `g.Declarado`: `somaAtivos[d] + g[d] ≤ Dimensoes[d] − Reservado[d]`; (d) com `Medido != nil`: para cada d de `g.Declarado`, `Medido[d] + g[d] + Margem[d] ≤ Dimensoes[d]`, e para cada d de `TetoMedido`, `Medido[d] < TetoMedido[d] × Dimensoes[d]`.
  3. `EstimatedStart`: cabeça exclusiva → maior `Prazo` entre os ativos (ou `now` se nenhum). Senão, ordene os ativos por `Prazo` e libere um a um até a conta declarada caber; devolva o `Prazo` daquele (ou `now` se já cabe).

- [ ] **Step 1: Testes** (tabela; GPU de 8192 MB com reservado 2200, salvo indicação):
  - `TestAdmitDeclarado`: ativo 4000 → pedido 2000 entra (4000 + 2000 ≤ 5992); pedido 2500 não entra.
  - `TestAdmitMedido`: sem ativos, `Medido{vram_mb: 7000}`, pedido 1000 com margem 512 → não entra (7000 + 1000 + 512 > 8192); `Medido = nil` → entra.
  - `TestAdmitTeto`: `TetoMedido{vram_mb: 0.95}`, medido 7900 → nada entra.
  - `TestAdmitExclusivo`: exclusivo na fila com um ativo → não entra; sem ativos → entra, e o seguinte não entra no mesmo `Admit`.
  - `TestAdmitPrioridade`: fila [executor (antigo), versao (novo)] → `versao` entra primeiro quando só cabe um.
  - `TestAdmitBackfill`: ativo até `now + 20m` ocupando quase tudo; cabeça grande (início estimado em `now + 20m`); pedido pequeno de 10 min entra, pedido pequeno de 30 min não entra.
  - `TestAdmitDimensaoAusente`: pedido só com `vram_mb` ignora `uso_pct` nas contas.
  - `TestSpecDeConfig` (config): `ResourceSec.Spec("gpu")` do exemplo da spec dá `Tipos["e2e-nivel2"].Exclusivo == true`, `MedidorIntervalo == 15s` e `PrazoMaxPorPrioridade["versao"] == 120m`.
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./internal/core/ ./internal/config/` → PASS.
- [ ] **Step 5: Commit** `feat(core): pure resource admission with backfill`.

### Task 7: Concessões (pedido, fila, prazo, expiração)

**Files:**
- Create: `internal/core/grants.go`
- Modify: `internal/core/schema.go` (migração 3: `concessoes(id INTEGER PRIMARY KEY AUTOINCREMENT, recurso, vaga, tipo, prioridade, motivo, declarado TEXT /*json*/, exclusivo INTEGER, duracao_ms INTEGER, estimativa, estado, pedido_em, inicio, prazo, fim, fim_motivo, sozinha INTEGER, pico TEXT, prazo_avisado INTEGER DEFAULT 0, abaixo_desde TEXT, sem_uso_avisado INTEGER DEFAULT 0, chave TEXT, UNIQUE(vaga, chave))` e `historico_recursos(id INTEGER PRIMARY KEY AUTOINCREMENT, recurso, tipo, pico TEXT, fim TEXT)`), `internal/core/tick.go`, `internal/core/agents.go` (`Leave`), `internal/service/service.go`, `internal/api/server.go`, `internal/cli/cli.go`
- Test: `internal/core/grants_test.go`, `internal/cli/cli_e2e_test.go`

**Interfaces:**
- Consumes: `Admit`, `SortQueue`, `ResourceSpec` (Task 6); `Settings.Resources` (Task 3); `sendSystem` (Task 4).
- Produces:
  - `type GrantRequest struct{ Recurso, Vaga, Tipo, Prioridade, Motivo, Chave string; Declarado Dims; Exclusivo bool; Duracao time.Duration }`
  - `func (c *Core) RequestGrant(ctx, r GrantRequest) (Grant, error)`: valida (recurso e prioridade conhecidos → senão `ErrInvalid`; declarado maior que a capacidade livre → `ErrInvalid`; `Duracao` acima do teto → `ErrInvalid` com "divida em etapas"; `Duracao == 0` → teto). Estima (Decisão 6 e spec: declarado > histórico (máximo das últimas 5 do tipo × 1,2) > tipo > exclusivo). Recusa um segundo pedido não encerrado da mesma vaga no mesmo recurso (`ErrConflict`, com o id existente). Insere com `fila` e roda a admissão do recurso na mesma transação.
  - `func (c *Core) ReleaseGrant(ctx, vaga, recurso string) (Grant, error)`: encerra o pedido ou a concessão da vaga (`fim_motivo = "solta"`), grava o histórico se `sozinha` e o pico não estiver vazio, e roda a admissão.
  - `func (c *Core) GrantByID(ctx, id int64) (Grant, error)`
  - `type ResourceView struct{ Spec ResourceSpec; Ativas, Fila []Grant; Ultima *Sample; LivreDeclarado Dims }` (tags `spec`, `ativas`, `fila`, `ultima`, `livre_declarado`); `func (c *Core) ResourceStatus(ctx, nome string) (ResourceView, error)`
  - Ativação: `inicio = now`, `prazo = now + Duracao`, `sozinha` = nenhum outro ativo (e `sozinha = 0` em todos os ativos quando outro entra), evento `recurso.concedida` e mensagem de `CENTRAL` com o mesmo tipo para a vaga (corpo com recurso, id e prazo `HH:MM` UTC).
  - `Tick` acrescenta, por recurso: (1) prazo vencido sem aviso → mensagem `recurso.prazo`; (2) `now > prazo + 10m` e batimento do dono anterior ao prazo → encerra com `fim_motivo = "expirada"`, mensagem `recurso.liberada` ao dono e evento; (3) admissão.
  - Daemon reiniciado: nada a fazer além do `OpenAll`, porque o estado está no banco e o primeiro `Tick` retoma.
- API: `POST .../recursos/{nome}/pedidos {pasta, tipo, prioridade, motivo, dims, exclusivo, min, chave}` → `{concessao}`; `GET .../recursos/{nome}/pedidos/{gid}/esperar?ate=` (long-poll até `ativa`; `motivo` ∈ {`concedida`, `tempo`}); `DELETE .../recursos/{nome}/concessao?pasta=`; `GET .../recursos/{nome}`.
- CLI: `central recurso pedir <nome> [--tipo T] [--dims k=v,k=v] [--exclusivo sim] [--prioridade P] [--min N] [--motivo M] [--chave K] [--esperar 60m]`. Com `--esperar`, prazo vencido ainda na fila → saída 2 com `ainda_na_fila`. Mais `central recurso soltar <nome>` e `central recurso ver <nome>`.

- [ ] **Step 1: Testes** (relógio controlável; recurso do exemplo da spec sem medidor, salvo indicação):
  - `TestPedidoEntraNaHora`: fila vazia → `ativa`, mensagem `recurso.concedida` na caixa da vaga.
  - `TestFilaEAtivacaoAoSoltar`: A exclusivo ativo; B na fila; `ReleaseGrant(A)` → B `ativa` e B recebe a mensagem.
  - `TestEstimativaPorHistorico`: duas execuções sozinhas do tipo `t` com pico de 3000 e 4000 → o próximo pedido sem dims declara `vram_mb = 4800` com `Estimativa = "historico"`.
  - `TestEstimativaTipoEExclusivo`: sem histórico, usa o tipo; tipo desconhecido e sem dims → exclusivo.
  - `TestPicoNaoAlimentaQuandoAcompanhada`: duas ativas ao mesmo tempo → nenhuma linha nova em `historico_recursos`.
  - `TestPrazoAcimaDoTeto`: `executor` com `--min 45` e teto 30m → `ErrInvalid` com "divida"; `versao` com 90 min e `prazo_max_por_prioridade.versao = 120m` → aceito.
  - `TestSegundoPedidoMesmaVaga` → `ErrConflict`.
  - `TestConcessaoExpiraSemBatimento`: A ativa com 10 min; o relógio vai a +11 min → `recurso.prazo`; vai a +21 min sem `Touch` de A → A `encerrada`/`expirada`, B da fila `ativa`. Variante: A faz `Touch` aos +15 min → não é liberada.
  - `TestConcessoesSobrevivemReinicio` (service): A ativa e B na fila; fecha e reabre o serviço; `Tick` → estado igual; `ReleaseGrant(A)` → B ativa.
  - `TestRecursoCLI` (e2e): `pedir` exclusivo por FIXER1 → `ativa`; `pedir --esperar 3s` por FIXER2 → saída 2 com `ainda_na_fila`; `pedir --esperar 10s` em segundo plano, FIXER1 `soltar` → o processo termina com 0 e `ativa`.
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat(core): resource grants with queue, deadlines and expiry`.

### Task 8: Medidor, amostras, aprendizado e concessão sem uso

**Files:**
- Create: `internal/service/meter.go`, `internal/core/samples.go`
- Modify: `internal/core/schema.go` (migração 3: `amostras(recurso TEXT NOT NULL, quando TEXT NOT NULL, valores TEXT NOT NULL)` com índice `(recurso, quando)`), `internal/core/grants.go`, `internal/core/tick.go`, `internal/service/service.go`, `internal/daemon/daemon.go`
- Test: `internal/service/meter_test.go`, `internal/core/samples_test.go`

**Interfaces:**
- Produces:
  - `type Sample struct{ Quando time.Time; Valores Dims }` (tags `quando`, `valores`)
  - `func (c *Core) RecordSample(ctx, recurso string, s Sample) error`: grava; atualiza o `pico` das ativas (`max(valor − reservado, 0)`); atualiza `abaixo_desde` e dispara `recurso.sem_uso` (Decisão 7); roda a admissão.
  - `func (c *Core) LatestSample(ctx, recurso string, maxAge time.Duration) (*Sample, error)`: usada pela admissão com `maxAge = 3 × MedidorIntervalo`; sem amostra → evento `medidor.sem_leitura` (no máximo um por minuto).
  - `func (c *Core) Samples(ctx, recurso string, de, ate time.Time) ([]Sample, error)`
  - `func ParseMeterOutput(out string, mapa []string) (core.Dims, error)` (service): primeira linha não vazia, valores separados por vírgula, espaços aparados, quantidade igual a `len(mapa)`, todos numéricos.
  - `func RunMeter(ctx, comando string, mapa []string, timeout time.Duration) (core.Dims, error)`: shell do sistema (`cmd /C` no Windows, `sh -c` nos outros), com timeout de 10 s.
  - `func (s *Service) RunMeters(ctx)`: para cada projeto aberto e recurso com medidor, uma goroutine no intervalo configurado; erro → evento `medidor.falhou` (no máximo um por minuto por recurso). Reinicia os medidores quando a configuração muda.
  - O `Tick` apaga amostras com mais de 7 dias (uma vez por hora).

- [ ] **Step 1: Testes:**
  - `TestParseMeterOutput`: `"45, 3120\n"` com `["uso_pct","vram_mb"]` → `{uso_pct:45, vram_mb:3120}`; colunas a menos, texto (`"N/A, 3120"`) e saída vazia → erro.
  - `TestMedidorSaidaInvalida`: o medidor é um comando `echo lixo`; `RunMeters` por 300 ms → nenhuma amostra, evento `medidor.falhou`, e um pedido ainda é admitido pela conta declarada.
  - `TestMedidorTimeout`: o comando dorme 30 s com timeout de 200 ms → erro, sem goroutine presa (o teste termina).
  - `TestAdmissaoUsaMedido` (core): amostra recente de 7000 MB → pedido de 1000 MB fica na fila; amostra de 3000 MB → entra.
  - `TestAmostraVelhaIgnorada`: amostra mais velha que 3 intervalos → só a conta declarada, mais o evento.
  - `TestPicoEHistorico`: concessão sozinha recebe amostras de 2200+1000 e 2200+3500 → ao soltar, `historico_recursos` tem `vram_mb = 3500`.
  - `TestSemUsoAvisaUmaVez`: `sem_uso = {dimensao = "uso_pct", limite = 5, apos = "5m"}`; concessão que declarou `uso_pct = 80`; amostras com uso 16 % (reservado 15) por 6 min → uma mensagem `recurso.sem_uso`; uma amostra com 60 % no meio reinicia a contagem.
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat: resource meters, samples, learning and idle-grant alert`.

### Task 9: Execuções longas (`rodar`)

**Files:**
- Create: `internal/core/executions.go`, `internal/cli/rodar.go`
- Modify: `internal/core/schema.go` (migração 3: `execucoes(id INTEGER PRIMARY KEY AUTOINCREMENT, vaga, rotulo, comando, inicio, batimento, fim, codigo INTEGER, final_log TEXT, concessao INTEGER)`), `internal/core/agents.go` (sumido considera execução viva), `internal/api/server.go`, `internal/cli/cli.go`
- Test: `internal/core/executions_test.go`, `internal/cli/cli_e2e_test.go`

**Interfaces:**
- Produces (core): `type Execution struct{ ID int64; Vaga, Rotulo, Comando, FinalLog string; Inicio, Batimento time.Time; Fim *time.Time; Codigo *int; Concessao int64 }`; `StartExecution(ctx, vaga, rotulo, comando string, concessao int64) (Execution, error)`; `BeatExecution(ctx, id int64) error` (também faz `Touch` da vaga); `FinishExecution(ctx, id int64, codigo int, finalLog string) (Execution, error)` (evento com `duracao_ms`; se `concessao > 0`, grava em `dados` o resumo das amostras do período: pico e média por dimensão).
- API: `POST .../execucoes {pasta, rotulo, comando, concessao}`, `POST .../execucoes/{eid}/batimento` e `POST .../execucoes/{eid}/fim {codigo, final_log}`.
- CLI `central rodar --rotulo R [--recurso N --tipo T --prioridade P --min M --dims ...] -- <comando> [args...]`:
  1. Com `--recurso`: `pedir` + `esperar` até 60 min (se não sair da fila, saída 2 sem rodar nada).
  2. Inicia a execução no daemon e roda o filho (`exec.Command` direto, sem shell), com o stdout e o stderr do filho indo para o **stderr** da CLI e as últimas 50 linhas guardadas.
  3. Batimento a cada 60 s numa goroutine.
  4. No fim, mesmo com erro ou `Ctrl+C` (`signal.NotifyContext` repassando a interrupção ao filho): `fim` com o código, `soltar` o recurso, e uma linha JSON no stdout com `{ok, execucao, codigo}`.
  5. Saída da CLI = código do filho; comando inexistente → 1, e a execução é encerrada com `codigo = -1`.

- [ ] **Step 1: Testes:**
  - `TestExecucaoImpedeSumido` (core): sem `Touch` da vaga, mas com `BeatExecution` recente → `Agents()` não mostra `sumido`.
  - `TestFinishResumeAmostras` (core): com concessão e amostras no período, o evento de fim traz o pico de `vram_mb`.
  - `TestRodarPropagaCodigo` (e2e): `rodar --rotulo t -- <go-helper> exit 3` → saída 3; o JSON tem `codigo: 3`; a execução está encerrada no daemon.
  - `TestRodarComandoInexistente` (e2e) → saída 1 e execução com `codigo -1`.
  - `TestRodarSoltaRecursoNoFim` (e2e): recurso exclusivo configurado no `central.toml` do teste; `rodar --recurso gpu --tipo t -- <helper> sleep 1`; depois, `recurso ver gpu` sem ativas.
  - `TestRodarGuardaFinalDoLog` (e2e): o helper imprime 80 linhas → `final_log` tem as linhas 31–80.
  - Helper: um `main` mínimo em `internal/cli/testdata/helper/` compilado no `TestMain` (subcomandos `exit N`, `sleep S` e `lines N`), para não depender de shell.
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat: long-running executions with heartbeat and resource lifecycle`.

### Task 10: Fatos e ids

**Files:**
- Create: `internal/core/facts.go`, `internal/core/counters.go`
- Modify: `internal/core/schema.go` (migração 3: `fatos(id INTEGER PRIMARY KEY AUTOINCREMENT, chave TEXT NOT NULL, valor TEXT NOT NULL, vaga TEXT NOT NULL, motivo TEXT NOT NULL, quando TEXT NOT NULL)` com índice `(chave, id)`, e `contadores(prefixo TEXT PRIMARY KEY, ultimo INTEGER NOT NULL)`), `internal/api/server.go`, `internal/cli/cli.go`
- Test: `internal/core/facts_test.go`, `internal/core/counters_test.go`

**Interfaces:**
- Produces: `type Fact struct{ Chave string; Valor json.RawMessage; Vaga, Motivo string; Quando time.Time }`; `SetFact(ctx, vaga, chave string, valor json.RawMessage, motivo string) (Fact, error)` (chave `^[a-z0-9][a-z0-9._-]{0,127}$`, `valor` JSON válido, `motivo` obrigatório); `GetFact(ctx, chave) (Fact, error)`; `FactHistory(ctx, chave) ([]Fact, error)` (mais novo primeiro); `Facts(ctx) ([]Fact, error)` (o último de cada chave). `NextID(ctx, prefixo string, minimo int) (string, error)`: `prefixo` com `^[A-Z]{1,5}$`; `ultimo = max(ultimo, minimo−1) + 1`; formato `%s-%03d`.
- API: `GET/POST .../fatos/{chave}`, `GET .../fatos` e `GET .../fatos/{chave}/historico`; `POST .../ids/{prefixo} {minimo}`.
- CLI: `central fato set <chave> <valor> --motivo M` (o `valor` vira JSON se for JSON válido; senão, string JSON); `central fato get <chave>`; `central fato historico <chave>`; `central fatos`; `central id <PREFIXO> [--minimo N]`.

- [ ] **Step 1: Testes:** `TestFatoUltimoVence`; `TestHistoricoDoFato`; `TestFatoValidacao` (chave inválida, JSON inválido e motivo vazio → `ErrInvalid`); `TestNextIDConcorrente` (50 goroutines → 50 ids distintos e consecutivos); `TestNextIDMinimo` (`minimo = 115` → `T-115`, depois `T-116`; `minimo = 10` com o último em 116 → `T-117`); `TestFatoCLI` (e2e: `fato set e2e.base '{"commit":"abc"}' --motivo x` → `fato get` devolve o objeto; `fato set nota texto` → a string).
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat(core): facts with history and atomic id counters`.

### Task 11: Backups, rotação de log e `doctor`

**Files:**
- Modify: `internal/store/store.go`, `internal/service/service.go`, `internal/daemon/daemon.go`, `internal/cli/cli.go`
- Create: `internal/store/backup.go`, `internal/cli/doctor.go`
- Test: `internal/store/backup_test.go`, `internal/daemon/daemon_test.go`, `internal/cli/cli_e2e_test.go`

**Interfaces:**
- Produces:
  - `type Options struct{ BackupDir string }`; **`func Open(path string, migrations []string, opt Options) (*sql.DB, error)`**: antes de migrar um banco existente com `versao < len(migrations)`, faz `Backup`. Atualize os chamadores.
  - `func Backup(db *sql.DB, dir, base string, keep int) (string, error)`: `VACUUM INTO <dir>/<base>-<AAAAMMDDTHHMMSS>.db`, apagando os mais antigos além de `keep`.
  - `func (s *Service) BackupAll(ctx) error`: registro + projetos abertos, `keep = 48`. O daemon chama a cada hora.
  - Daemon: ao subir, se `logs/daemon.log` > 10 MB, gira para `.1` (mantendo `.1`–`.3`).
  - CLI `central doctor`: imprime `{"ok": bool, "verificacoes": [{"nome","ok","detalhe"}]}` e sai com 0 se tudo ok, 1 senão. Verificações:
    - `daemon`: saúde, mesma versão do binário e mesmos `dados`;
    - `dados`: a pasta existe e é gravável;
    - `projeto`: o `central.toml` é encontrado e `ParseProjectConfig` passa;
    - `medidor:<recurso>`: `RunMeter` roda e devolve valores;
    - `porta`: se o daemon não responde, se a porta está ocupada por outro processo.

- [ ] **Step 1: Testes:** `TestBackupMantemUltimos` (`keep = 2` com 3 chamadas → 2 arquivos, e o mais novo abre com os dados); `TestBackupAntesDeMigrar` (banco na versão 1 aberto com 2 migrações → um arquivo em `BackupDir`); `TestGiraLogGrande` (daemon); `TestDoctorTudoOk` (e2e com projeto e medidor `echo 10, 2000`) e `TestDoctorMedidorQuebrado` (medidor `exit 1` → saída 1 e `medidor:gpu` com `ok: false`).
- [ ] **Step 2:** rodar → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Commit** `feat: backups, log rotation and doctor`.

### Task 12: Cenário ponta a ponta do M1a e documentação

**Files:**
- Create: `internal/cli/m1a_e2e_test.go`
- Modify: `README.md`, `docs/spec.md` (só a Decisão 2 deste plano: no §5.1, acrescente que todo motivo de acordar é materializado como mensagem de `CENTRAL`)

- [ ] **Step 1: Teste** `TestCenarioM1a`, com dois agentes e o recurso `gpu` (medidor `echo` controlado por um arquivo que o teste reescreve):
  1. FIXER1 e FIXER2 entram;
  2. FIXER1 `trava package.json` e FIXER2 recebe 2;
  3. FIXER1 `rodar --recurso gpu --tipo t -- helper sleep 2` enquanto FIXER2 `recurso pedir gpu --exclusivo sim --esperar 30s`; FIXER2 recebe a concessão depois que o `rodar` termina;
  4. FIXER2 `esperar` devolve `motivo = recurso` (a mensagem `recurso.concedida`);
  5. `fato set` / `fato get`;
  6. `enviar --responder 1s` e `pendentes`; depois de 3 s, o destinatário tem `resposta.prazo`;
  7. `agentes` mostra os dois `ativo`;
  8. `doctor` ok.
- [ ] **Step 2:** rodar → deve passar, porque tudo já existe; se falhar, é defeito de integração: corrija na task de origem e registre no ledger.
- [ ] **Step 3:** README: seções Travas, Recursos (com o exemplo da GPU e o `--dims`), `rodar`, Fatos, Respostas com prazo, `doctor`; tabela de códigos de mensagem do sistema (`recurso.*`, `resposta.prazo`, `agente.sumiu`).
- [ ] **Step 4:** `go test -count=1 ./...` e `go vet ./...` → PASS.
- [ ] **Step 5: Commit** `test: m1a end-to-end scenario; docs`.
