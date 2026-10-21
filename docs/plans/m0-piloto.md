# Central M0 (piloto do "acordar na hora") Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Construir a base da Central (daemon com autostart, armazenamento, agentes, mensagens, `caixa` e `esperar` por long-poll) e provar que um agente acorda em menos de 10 s depois que outro lhe envia uma mensagem, inclusive entre duas sessões do app Claude em `/loop`.

**Architecture:** Um binário Go (`central.exe`). O subcomando `daemon` serve HTTP `/v1` em `127.0.0.1` e é o único que escreve nos bancos SQLite (registro da máquina + um banco por projeto). Os demais subcomandos são uma CLI cliente que sobe o daemon sozinha. Camadas e pacotes seguem a §4 da spec; o M0 cria `config`, `store`, `core`, `service`, `api`, `daemon` e `cli`.

**Tech Stack:** Go ≥ 1.23 (`CGO_ENABLED=0`), `modernc.org/sqlite`, `github.com/pelletier/go-toml/v2`, `github.com/gofrs/flock`, biblioteca padrão (`net/http` com rotas por padrão do Go 1.22+, `flag`, `encoding/json`).

**Spec:** `docs/superpowers/specs/2026-10-20-central-coordenacao-de-agentes-design.md` (no CortesEditor; a Task 1 a copia para o repositório novo em `docs/spec.md`).

## Global Constraints

- Módulo `github.com/JoaoVitorSantDrade/central`; repositório em `<repo>`. Criar o repositório no GitHub (privado, `JoaoVitorSantDrade/central`) e fazer push **só com confirmação explícita do humano**.
- `go.mod` com `go 1.23`; build com `CGO_ENABLED=0`.
- Daemon escuta só em `127.0.0.1`; endereço padrão `127.0.0.1:7420`; variável `CENTRAL_ADDR` sobrescreve.
- Dados em `%LOCALAPPDATA%\central\` (Linux/macOS: `os.UserConfigDir()`/`central`); variável `CENTRAL_HOME` sobrescreve. Layout: `registro.db`, `projetos/<id>.db`, `daemon.lock`, `logs/daemon.log`.
- SQLite em WAL, `busy_timeout=5000`, `foreign_keys=ON`, `SetMaxOpenConns(1)` (um dono do banco, sem disputa de escrita).
- Contrato HTTP sob `/v1`; JSON UTF-8; nomes de campos em português, como na spec.
- Códigos de saída da CLI (spec §6.6): `0` ok, `1` erro inesperado, `2` conflito, `3` não encontrado, `4` recusado por regra, `5` daemon indisponível.
- Mapeamento HTTP → saída: 409 → 2, 404 → 3, 422 → 4, rede/autostart falhou → 5, outro → 1. Corpo de erro: `{"ok":false,"erro":{"codigo":"<slug>","mensagem":"<texto>"}}`.
- Horários gravados em UTC, RFC 3339 com milissegundos.
- Saída da CLI é sempre uma linha JSON em stdout; erros também saem em JSON (stdout) com o código de saída correspondente.
- Nada de domínio LyricCut no código (spec §4, regra 3).

## Review Focus

- **Daemon reiniciado durante um `esperar`**: uma mensagem enviada depois da volta ainda acorda o `esperar` dentro do prazo, sem erro. → teste `TestEsperarSobreviveReinicioDoDaemon` (Task 7).
- **Mensagem enviada antes de a vaga entrar** (o GERENTE escreve ao REVISOR2 antes de a sessão abrir): a primeira `caixa` da vaga a recebe. Sessão substituta na mesma vaga continua do cursor da anterior. → `TestCaixaEntregaMensagemAnteriorAEntrada` e `TestVagaSubstitutaHerdaCursor` (Task 3).
- **Duas `caixa` simultâneas da mesma vaga**: cada mensagem é entregue exatamente uma vez. → `TestCaixaConcorrenteEntregaUmaVez` (Task 3).
- **Corpo com acentos, aspas e várias linhas** vindo do PowerShell: chega intacto. Corpo longo pode vir do stdin (`-`). → `TestEnviarCorpoUTF8EStdin` (Task 7).
- **Comando rodado numa subpasta ou fora de projeto**: subpasta acha o `central.toml` acima; fora de projeto sai com código 3 e mensagem clara. → `TestFindProjectSobePastas` e `TestFindProjectForaDeProjeto` (Task 2).

---

### Task 0: Instalar o Go (precisa do humano)

**Files:** nenhum.

- [ ] **Step 1: Pedir ao humano** para instalar o Go ou autorizar a instalação. Sugestão: `winget install --id GoLang.Go -e`. O winget pede aceite de termos, que é decisão do humano. Não aceite termos pelo agente.
- [ ] **Step 2: Verificar**

Run: `go version` (num shell novo)
Expected: `go version go1.2x.x windows/amd64`, com x ≥ 3.

### Task 1: Repositório e esqueleto

**Files:**
- Create: `<repo>\go.mod`, `.gitignore`, `README.md`, `docs/spec.md` (cópia da spec), `docs/plans/m0-piloto.md` (cópia deste plano), `cmd/central/main.go`, `internal/version/version.go`
- Test: `internal/version/version_test.go`

**Interfaces:**
- Produces: `version.Versao = "0.1.0"`, `version.API = "v1"`.

- [ ] **Step 1:** `mkdir <repo>`, `git init -b main`, `go mod init github.com/JoaoVitorSantDrade/central`, ajustar `go 1.23`. `.gitignore`: `/bin/`, `*.exe`, `/dist/`. Copiar spec e plano.
- [ ] **Step 2: Teste** `TestVersao`: `Versao` casa com `^\d+\.\d+\.\d+$` e `API == "v1"`.
- [ ] **Step 3:** criar `version.go`; `main.go` imprime `{"ok":true,"versao":"0.1.0","api":"v1"}` para `central versao` (vira dispatcher na Task 7).
- [ ] **Step 4:** `go test ./...` → PASS; `go build -o bin/central.exe ./cmd/central` e `bin/central.exe versao` → JSON acima.
- [ ] **Step 5: Commit** `chore: scaffold central module`.

### Task 2: Configuração e identidade do projeto

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type Project struct { ID, Name, Root string }`
  - `func FindProject(dir string) (Project, error)`: sobe de `dir` até achar `central.toml`; `Root` = pasta do arquivo (caminho absoluto, limpo); erro `ErrNoProject` quando chega à raiz do disco.
  - `func InitProject(dir, id, name string) (Project, error)`: cria `central.toml` (`[projeto]` com `id` e `nome`) e a pasta `.central/`; erro `ErrExists` se já houver `central.toml`.
  - `func DataDir() (string, error)`: `CENTRAL_HOME` ou o padrão das Global Constraints.
  - `func Addr() string`: `CENTRAL_ADDR` ou `127.0.0.1:7420`.
  - `var ErrNoProject, ErrExists error`
- `id` válido: `^[a-z0-9][a-z0-9-]{0,62}$`; inválido → erro `ErrInvalidID`.

- [ ] **Step 1: Testes** (em `t.TempDir()`):
  - `TestInitEFindProject`: `InitProject(d,"lyriccut","LyricCut")` e `FindProject(d)` devolvem `{ID:"lyriccut", Name:"LyricCut", Root:d}`.
  - `TestFindProjectSobePastas`: `FindProject(d/a/b)` devolve `Root == d`.
  - `TestFindProjectForaDeProjeto`: pasta sem `central.toml` em nenhum ancestral (usar `t.TempDir()` e conferir que não há `central.toml` acima; pular se houver) → `errors.Is(err, ErrNoProject)`.
  - `TestInitRecusaExistente`: segundo `InitProject` → `ErrExists`.
  - `TestIDInvalido`: `"Lyric Cut"` → `ErrInvalidID`.
  - `TestDataDirEAddrPorVariavel`: com `t.Setenv`, valores das variáveis são respeitados.
- [ ] **Step 2:** `go test ./internal/config/` → FAIL (não compila).
- [ ] **Step 3:** implementar com `go-toml/v2` (`Unmarshal` em `struct{ Projeto struct{ ID string \`toml:"id"\`; Nome string \`toml:"nome"\` } \`toml:"projeto"\` }`).
- [ ] **Step 4:** `go test ./internal/config/` → PASS.
- [ ] **Step 5: Commit** `feat(config): project discovery and data dir`.

### Task 3: Armazenamento e núcleo de mensagens

**Files:**
- Create: `internal/store/store.go`, `internal/core/schema.go`, `internal/core/core.go`, `internal/core/notify.go`
- Test: `internal/store/store_test.go`, `internal/core/core_test.go`, `internal/core/notify_test.go`

**Interfaces:**
- Produces (store):
  - `func Open(path string, migrations []string) (*sql.DB, error)`: cria a pasta, abre com os PRAGMAs das Global Constraints, aplica em transação as migrações cujo índice ≥ `schema_version` (tabela `schema_version(versao INTEGER)`), cada uma uma vez.
- Produces (core):
  - `var ProjectMigrations []string`: migração 1 cria
    - `agentes(vaga TEXT PRIMARY KEY, papel TEXT NOT NULL, pasta TEXT NOT NULL UNIQUE, entrou_em TEXT NOT NULL, batimento TEXT NOT NULL, cursor INTEGER NOT NULL DEFAULT 0)`
    - `mensagens(seq INTEGER PRIMARY KEY AUTOINCREMENT, de TEXT NOT NULL, para TEXT NOT NULL, tipo TEXT NOT NULL, ref TEXT NOT NULL DEFAULT '', corpo TEXT NOT NULL, criada_em TEXT NOT NULL, chave TEXT UNIQUE)`
    - `eventos(seq INTEGER PRIMARY KEY AUTOINCREMENT, quando TEXT NOT NULL, quem TEXT NOT NULL, tipo TEXT NOT NULL, ref TEXT NOT NULL DEFAULT '', dados TEXT NOT NULL DEFAULT '{}')`
  - `type Agent struct { Vaga, Papel, Pasta string; EntrouEm, Batimento time.Time; Cursor int64 }` (tags JSON `vaga`, `papel`, `pasta`, `entrou_em`, `batimento`, `cursor`)
  - `type Message struct { Seq int64; De, Para, Tipo, Ref, Corpo string; CriadaEm time.Time; Chave string }` (tags `seq`, `de`, `para`, `tipo`, `ref`, `corpo`, `criada_em`, `chave,omitempty`)
  - `type Core struct` com `func New(db *sql.DB, now func() time.Time) *Core`
  - `func (c *Core) Enter(ctx, vaga, papel, pasta string) (Agent, error)`
  - `func (c *Core) AgentByPasta(ctx, pasta string) (Agent, error)` → `ErrNotFound`
  - `func (c *Core) Send(ctx, deVaga, para, tipo, ref, corpo, chave string) (Message, error)`
  - `func (c *Core) Inbox(ctx, vaga string) ([]Message, error)`
  - `func (c *Core) Unread(ctx, vaga string) (int, error)`
  - `func (c *Core) Changed() <-chan struct{}`: canal fechado na próxima mudança
  - `var ErrNotFound, ErrInvalid error`
  - `type Notifier` com `Wait() <-chan struct{}` e `Broadcast()`: `Broadcast` fecha o canal atual e cria outro; seguro para goroutines.

**Regras:**
- `Enter`: `vaga` e `papel` não vazios, senão `ErrInvalid`. Vaga nova: `cursor = 0`. Vaga existente: troca a `pasta` e mantém o `cursor` (sessão substituta). Se a `pasta` estava ligada a outra vaga, essa outra vaga fica com `pasta = '(liberada) ' || vaga`, o que mantém o `UNIQUE`. Grava `agente.entrou` e renova o `batimento`.
- Destinatários de uma mensagem para a vaga V de papel P: `para IN (V, P, 'TODOS') AND de <> V`.
- `Inbox`: numa transação, lê as mensagens com `seq > cursor` endereçadas à vaga em ordem de `seq`, avança `cursor` para o maior `seq` lido, grava `caixa.lida` com `{"ate":<seq>}` e renova o batimento.
- `Send`: `para`, `tipo`, `corpo` não vazios, senão `ErrInvalid`; com `chave` repetida, devolve a mensagem existente sem gravar nada; grava `mensagem.enviada`; depois do commit chama `Broadcast`.
- Toda escrita grava o evento na mesma transação.

- [ ] **Step 1: Testes de store** `TestOpenAplicaMigracoesUmaVez` (abrir duas vezes; `schema_version` = número de migrações; tabela criada uma vez) e `TestOpenUsaWAL` (`PRAGMA journal_mode` = `wal`).
- [ ] **Step 2: Testes de core** (banco em `t.TempDir()`, relógio fixo):
  - `TestEnviarECaixa`: A envia a `REVISOR1`; `Inbox("REVISOR1")` devolve 1 mensagem com o corpo; segunda `Inbox` devolve 0.
  - `TestEnderecamentoPorPapelETodos`: mensagens para o papel `REVISOR` e para `TODOS` chegam a `REVISOR1` e `REVISOR2`; a mensagem para `TODOS` não volta ao remetente.
  - `TestCaixaEntregaMensagemAnteriorAEntrada`: mensagem para `REVISOR2` enviada antes de `Enter("REVISOR2",…)` aparece na primeira `Inbox`.
  - `TestVagaSubstitutaHerdaCursor`: `REVISOR1` lê 1 mensagem; `Enter("REVISOR1","REVISOR", outraPasta)`; nova mensagem; `Inbox` devolve só a nova; `AgentByPasta(outraPasta)` acha `REVISOR1`.
  - `TestCaixaConcorrenteEntregaUmaVez`: 100 mensagens; 10 goroutines chamando `Inbox("X")` até esvaziar; a união dos `seq` recebidos tem 100 elementos e nenhum repetido.
  - `TestChaveIdempotente`: dois `Send` com `chave="k1"` devolvem o mesmo `Seq`; só uma linha em `mensagens`.
  - `TestEventosGravados`: depois de `Enter`, `Send` e `Inbox`, `eventos` tem os tipos `agente.entrou`, `mensagem.enviada`, `caixa.lida` nessa ordem.
  - `TestUnread`: conta só as não lidas endereçadas.
  - `TestChangedDisparaNoSend`: canal de `Changed()` obtido antes do `Send` está fechado depois dele.
  - `TestValidacao`: `Send` com corpo vazio → `ErrInvalid`; `AgentByPasta` desconhecida → `ErrNotFound`.
- [ ] **Step 3: Teste de notify** `TestBroadcastAcordaTodos`: 5 goroutines esperando `Wait()` acordam após um `Broadcast`; um `Wait()` obtido depois do `Broadcast` não está fechado.
- [ ] **Step 4:** `go test ./internal/store/ ./internal/core/` → FAIL.
- [ ] **Step 5:** implementar.
- [ ] **Step 6:** `go test -race ./internal/store/ ./internal/core/` → PASS. Sem `-race` disponível (exige CGO no Windows), rodar sem a flag e registrar isso no commit.
- [ ] **Step 7: Commit** `feat(core): agents, messages, inbox cursor and events`.

### Task 4: Serviço (registro da máquina e esperar)

**Files:**
- Create: `internal/service/service.go`, `internal/service/registry.go`
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `store.Open`, `core.*` (Task 3).
- Produces:
  - `type Service struct`; `func New(dataDir string, now func() time.Time) (*Service, error)`; `func (s *Service) Close() error`
  - `func (s *Service) Enter(ctx, projetoID, nome, raiz, pasta, vaga, papel string) (core.Agent, error)`: registra ou atualiza o projeto em `registro.db` (`projetos(id TEXT PRIMARY KEY, nome TEXT, raiz TEXT, ultimo_acesso TEXT)`) e abre ou reaproveita `projetos/<id>.db`.
  - `func (s *Service) Send(ctx, projetoID, pasta, para, tipo, ref, corpo, chave string) (core.Message, error)`: o remetente vem de `AgentByPasta(pasta)`.
  - `func (s *Service) Inbox(ctx, projetoID, pasta string) ([]core.Message, error)`
  - `type WaitResult struct { Motivo string; Novas int; Sugestao string; EsperouMs int64 }` (tags `motivo`, `novas`, `sugestao`, `esperou_ms`); `Motivo` ∈ {`mensagem`, `tempo`}; com `mensagem`, `Sugestao = "central caixa"`.
  - `func (s *Service) Wait(ctx, projetoID, pasta string, ate time.Duration) (WaitResult, error)`
  - Projeto desconhecido ou pasta que não entrou → `core.ErrNotFound`.
- `Wait`: não avança o cursor. Laço: obtenha `ch := core.Changed()`, depois `Unread`; se > 0, retorne `mensagem`; senão espere `ch`, o prazo ou `ctx.Done()`. A ordem "canal antes da contagem" evita perder uma mensagem que chega entre as duas.

- [ ] **Step 1: Testes:**
  - `TestEnterRegistraProjeto`: depois de `Enter`, `registro.db` tem a linha do projeto e `projetos/<id>.db` existe.
  - `TestWaitRetornaNaHoraComPendente`: com mensagem não lida, `Wait(…, 5s)` volta com `mensagem` em < 100 ms.
  - `TestWaitAcordaQuandoChega`: `Wait(…, 5s)` numa goroutine; `Send` 200 ms depois; volta com `mensagem` e `EsperouMs` entre 150 e 1 000.
  - `TestWaitTempo`: sem mensagem, `Wait(…, 300ms)` volta com `tempo`.
  - `TestWaitCancelado`: contexto cancelado → volta com `ctx.Err()`.
  - `TestProjetosIsolados`: mensagem no projeto `a` não aparece no projeto `b`.
  - `TestDesconhecidos`: projeto ou pasta desconhecidos → `errors.Is(err, core.ErrNotFound)`.
- [ ] **Step 2:** `go test ./internal/service/` → FAIL.
- [ ] **Step 3:** implementar (mapa `id → *core.Core` protegido por mutex; bancos fechados em `Close`).
- [ ] **Step 4:** `go test ./internal/service/` → PASS.
- [ ] **Step 5: Commit** `feat(service): machine registry and long-poll wait`.

### Task 5: API HTTP `/v1`

**Files:**
- Create: `internal/api/server.go`, `internal/api/errors.go`
- Test: `internal/api/server_test.go`

**Interfaces:**
- Consumes: `service.Service` (Task 4).
- Produces: `func NewHandler(s *service.Service) http.Handler`. Rotas:

| Método e caminho | Corpo / query | Resposta 200 |
|---|---|---|
| `GET /v1/saude` | — | `{"ok":true,"versao":…,"api":"v1"}` |
| `POST /v1/projetos/{id}/agentes` | `{"nome","raiz","pasta","vaga","papel"}` | `{"ok":true,"agente":Agent}` |
| `POST /v1/projetos/{id}/mensagens` | `{"pasta","para","tipo","ref","corpo","chave"}` | `{"ok":true,"mensagem":Message}` |
| `POST /v1/projetos/{id}/caixa` | `{"pasta"}` | `{"ok":true,"mensagens":[…]}` (lista vazia, nunca `null`) |
| `GET /v1/projetos/{id}/esperar` | `pasta`, `ate` (duração Go, padrão `30m`, máximo `60m`) | `{"ok":true,"motivo","novas","sugestao","esperou_ms"}` |

- Erros: `core.ErrNotFound` → 404 `nao_encontrado`; `core.ErrInvalid` e JSON malformado → 422 `invalido`; resto → 500 `interno`. Formato das Global Constraints.
- `esperar` usa o contexto da requisição: se o cliente desconecta, a espera termina.

- [ ] **Step 1: Testes** com `httptest.NewServer`:
  - `TestSaude`.
  - `TestFluxoEntrarEnviarCaixa`: entra A e B, A envia a B, a caixa de B tem a mensagem.
  - `TestEsperarLongPoll`: `GET esperar?ate=5s` numa goroutine; envio após 200 ms; resposta `motivo=mensagem` em < 1 s.
  - `TestErros`: pasta desconhecida → 404 com `erro.codigo == "nao_encontrado"`; corpo vazio → 422; JSON malformado → 422; `ate=2h` → 422.
  - `TestCaixaVaziaEhLista`: o JSON contém `"mensagens":[]`.
- [ ] **Step 2:** `go test ./internal/api/` → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./internal/api/` → PASS.
- [ ] **Step 5: Commit** `feat(api): v1 http endpoints with long-poll`.

### Task 6: Daemon (instância única)

**Files:**
- Create: `internal/daemon/daemon.go`
- Test: `internal/daemon/daemon_test.go`

**Interfaces:**
- Consumes: `service.New`, `api.NewHandler`, `config.DataDir`, `config.Addr`.
- Produces: `func Run(ctx context.Context, dataDir, addr string) error`. Toma `flock` exclusivo em `<dataDir>/daemon.lock`; se não conseguir, retorna `ErrAlreadyRunning`. Escuta em `addr`, recusando qualquer host diferente de `127.0.0.1` (`ErrNotLoopback`). Grava `<dataDir>/daemon.json` com `{"pid","addr","versao","inicio"}` depois de começar a escutar e o apaga ao encerrar. Loga em `<dataDir>/logs/daemon.log`. Encerra limpo com `ctx` (desliga o HTTP com 5 s de prazo, fecha o serviço, solta a trava).
- `var ErrAlreadyRunning, ErrNotLoopback error`

- [ ] **Step 1: Testes:**
  - `TestRunServeSaude`: `Run` numa goroutine com porta livre; `GET /v1/saude` responde em até 2 s; `daemon.json` tem o `pid` do processo e o `addr`; cancelar o contexto faz `Run` retornar `nil` e apaga `daemon.json`.
  - `TestSegundaInstanciaRecusada`: segundo `Run` com o mesmo `dataDir` e outra porta → `ErrAlreadyRunning`.
  - `TestRecusaEnderecoNaoLoopback`: `addr = "0.0.0.0:0"` → `ErrNotLoopback`.
- [ ] **Step 2:** `go test ./internal/daemon/` → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./internal/daemon/` → PASS.
- [ ] **Step 5: Commit** `feat(daemon): single-instance loopback daemon`.

### Task 7: CLI com autostart e teste ponta a ponta

**Files:**
- Create: `internal/cli/cli.go` (dispatcher e saída JSON), `internal/cli/client.go` (cliente HTTP e mapeamento de erros), `internal/cli/autostart.go`, `internal/cli/autostart_windows.go`, `internal/cli/autostart_other.go`
- Modify: `cmd/central/main.go` (chama `os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout))`)
- Test: `internal/cli/cli_e2e_test.go`

**Interfaces:**
- Consumes: `config.*`, `daemon.Run`, contrato HTTP da Task 5.
- Produces: `func Main(args []string, stdin io.Reader, stdout io.Writer) int`. Subcomandos:

| Comando | Faz |
|---|---|
| `central versao` | imprime versão e API |
| `central daemon` | `daemon.Run` em primeiro plano (`ErrAlreadyRunning` → saída 0 com `{"ok":true,"ja_rodando":true}`) |
| `central init --id <id> [--nome <nome>]` | `config.InitProject` na pasta atual |
| `central entrar --vaga <V> --papel <P>` | `pasta` = raiz do projeto achado a partir da pasta atual |
| `central enviar <para> <tipo> [--ref R] [--chave K] <corpo \| ->` | `-` lê o corpo inteiro do stdin |
| `central caixa` | lista e marca como lidas |
| `central esperar [--ate 30m]` | long-poll; reconecta até o prazo |

- **Pasta do agente:** `config.FindProject(cwd).Root`. Fora de projeto → saída 3, `erro.codigo = "sem_projeto"`.
- **Autostart:** antes de cada chamada, `GET /v1/saude` com timeout de 300 ms. Se falhar, executa `os.Executable()` com `daemon`, desanexado (Windows: `CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS` e `HideWindow`; outros: `Setsid`), e tenta a saúde a cada 100 ms por até 5 s. Se não subir → saída 5.
- **`esperar` resiliente:** calcula o prazo final; se a conexão cair, refaz o autostart e chama de novo com o tempo restante; se o prazo acabar, imprime `motivo=tempo`.

- [ ] **Step 1: Teste ponta a ponta** (`go build` do binário em `t.TempDir()` uma vez em `TestMain`; cada teste com `CENTRAL_HOME` e `CENTRAL_ADDR` próprios, porta livre; o daemon é encerrado no `t.Cleanup` matando o processo cujo `pid` está em `<CENTRAL_HOME>/daemon.json`):
  - `TestAutostartEFluxo`: duas pastas de projeto (worktrees simulados, ambas com o mesmo `central.toml`); `entrar` como `FIXER1` e `REVISOR1` (sobe o daemon sozinho); FIXER1 `enviar REVISOR1 concluida "T-1 pronta"`; REVISOR1 `caixa` mostra a mensagem.
  - `TestEsperarAcordaEmMenosDeUmSegundo`: `esperar --ate 30s` do REVISOR1 em processo separado; após 500 ms, `enviar`; o processo termina com `motivo=mensagem` em < 1 s depois do envio.
  - `TestEsperarSobreviveReinicioDoDaemon`: `esperar --ate 30s` em andamento; matar o daemon; enviar (o autostart sobe outro); o `esperar` termina com `motivo=mensagem`.
  - `TestEnviarCorpoUTF8EStdin`: corpo `Revisão concluída: "ok" ✓` por argumento e um corpo de 3 linhas por `-` chegam iguais na `caixa`.
  - `TestForaDeProjetoSai3`: `caixa` numa pasta sem `central.toml` → saída 3 e `sem_projeto`.
  - `TestSemEntrarSai3`: `caixa` numa pasta de projeto sem `entrar` → saída 3 e `nao_encontrado`.
- [ ] **Step 2:** `go test ./internal/cli/` → FAIL.
- [ ] **Step 3:** implementar.
- [ ] **Step 4:** `go test ./...` → PASS (toda a suíte).
- [ ] **Step 5:** `go vet ./...` sem avisos; `CGO_ENABLED=0 go build -o bin/central.exe ./cmd/central`.
- [ ] **Step 6: Commit** `feat(cli): commands with daemon autostart and resilient wait`.

### Task 8: Piloto com sessões do app Claude e decisão do §5.1

**Files:**
- Create: `docs/piloto-m0.md` (procedimento e resultado), `README.md` (seção "Uso rápido")

- [ ] **Step 1:** Montar um projeto de teste `<pasta-de-teste>\central-piloto` com `central init --id piloto` e duas pastas de "worktree" com o mesmo `central.toml` (`a\` e `b\`); `central.exe` no `PATH` da sessão (ou chamado por caminho absoluto).
- [ ] **Step 2:** Com confirmação do humano, abrir duas sessões do app em `/loop`, uma em cada pasta, com estas instruções:
  - **A (ouvinte):** "Rode `central entrar --vaga B1 --papel OUVINTE`. Ao ficar ocioso, rode `central esperar --ate 30m` em segundo plano e agende um despertar de reserva de 30 min. Quando o comando terminar, rode `central caixa` e responda com `central enviar A1 eco \"recebi <seq> às <hora>\"`."
  - **B (emissor):** "Rode `central entrar --vaga A1 --papel EMISSOR`. A cada 3 min, por 15 min, rode `central enviar B1 ping \"<hora>\"` e anote a hora."
- [ ] **Step 3:** Medir, para 5 pings, `criada_em` do ping → hora em que a sessão A rodou `caixa` (evento `caixa.lida` no banco do projeto). Critério: mediana < 10 s, sem polling (a sessão A não acordou por relógio entre os pings).
- [ ] **Step 4:** Se falhar, repetir com o plano B (`central esperar --ate 3m` em primeiro plano dentro do ciclo) e medir igual.
- [ ] **Step 5:** Registrar em `docs/piloto-m0.md` os números, o modo que funcionou e a instrução exata de fim de ciclo que os cartões de papel vão usar. Atualizar o §5.1 de `docs/spec.md` se o plano B for o escolhido.
- [ ] **Step 6: Commit** `docs: m0 pilot results`.
- [ ] **Step 7:** Perguntar ao humano se cria o repositório privado `JoaoVitorSantDrade/central` no GitHub e faz o push (`gh repo create JoaoVitorSantDrade/central --private --source . --push`). Só com "sim".
