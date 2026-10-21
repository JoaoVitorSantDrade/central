# Central: coordenação de times de agentes — design

Data: 2026-10-20 · Estado: aprovado em conversa, aguardando revisão da spec escrita · Origem: LyricCut (CortesEditor), time autônomo de `docs/agents/AUTOMELHORIA-AUTONOMA.md`

## 1. Por que existe

O time autônomo do LyricCut coordena ~10 agentes por arquivos JSON em `.git/lyriccut-agents` (canal, ponto, reservas, fila da GPU) e por um manual de 1 333 linhas. Medido em 2026-10-20 (quatro sprints):

- **Latência entre papéis domina a entrega.** Tarefa leva ~9 min até `concluida`, mas 30–49 min até entrar na `develop`; `concluida → veredito` tem mediana de 13 min. Causa: cada papel acorda por polling a cada 10–60 min.
- **Canal sem semântica.** 805 mensagens num dia; 42 % são `alerta` porque não há tipo para "concluída/veredito/entregue"; 28 de 115 mensagens que pediam resposta não foram respondidas.
- **Estado gravado à mão.** Quatro lições ativas (L-009, L-011, L-015, L-022) existem só por erros de gravar JSON pelo shell.
- **Contexto relido.** O manual (~60 mil tokens) é lido por cada sessão ao abrir e depois de cada resumo automático, e boa parte dele é algoritmo em prosa.
- **Pouca visibilidade** para o humano durante a sprint, e adicionar exemplos (vídeos-meta, conjuntos de base) exige edição manual em vários lugares.

## 2. Objetivo e critérios de sucesso

Um sistema local, reutilizável em qualquer projeto, que guarda o estado vivo de um time de agentes, executa as regras de processo como código, acorda o agente certo no instante em que há trabalho e mostra tudo num painel único da máquina.

Prioridades de entrega, decididas pelo humano: **velocidade > menos releitura > visibilidade > exemplos**.

| Critério | Hoje (LyricCut) | Meta |
|---|---|---|
| `concluida → início da revisão` (mediana) | parte dos 13 min até o veredito | < 1 min |
| `concluida → integrada` (mediana) | 30–49 min | < 20 min |
| destinatário acorda após mensagem | 10–60 min (plantão) | < 10 s |
| tokens de contexto ao abrir uma sessão | ~60 mil (só o manual) | < 15 mil |
| mensagens do tipo `alerta` | 42 % | < 10 % |
| pendências de resposta esquecidas | 28 de 115 | visíveis no painel; 0 sem resposta após o prazo |
| adicionar vídeo-meta ou conjunto de base | edição manual em 2–3 lugares | soltar arquivo + formulário |
| usar em outro projeto | — | `central init` sem tocar no código da Central |

## 3. Decisões tomadas

| # | Pergunta | Decisão |
|---|---|---|
| D1 | Quem usa | Claude Code **e** outros agentes/ferramentas: o núcleo tem interface neutra (CLI, HTTP, MCP); o plugin do Claude Code é só um cliente |
| D2 | Quanto processo embutir | **Processo opinativo embutido** (sprint, papéis, QA, integração), parametrizável por projeto, construído sobre um núcleo de primitivas estável |
| D3 | Visibilidade | **Painel web local único para todos os projetos** da máquina |
| D4 | Lançar agentes | **Só coordenar na v1**; o desenho já prevê lançar e supervisionar agentes depois |
| D5 | Ordem de entrega | velocidade > menos releitura > visibilidade > exemplos |
| D6 | Tecnologia | **Binário único em Go**: daemon + CLI + MCP + painel embutido; SQLite em Go puro (sem CGO) |
| D7 | GPU | Recurso com **dimensões** (VRAM, uso), pedidos declarados ou aprendidos do histórico, admissão por conta declarada **e** medida |
| D8 | Repositório | `JoaoVitorSantDrade/central` no GitHub (público desde a 1.1.0, licença MIT) |

### Fora de escopo da v1
Lançar/supervisionar agentes (D4); várias máquinas; autenticação e permissões entre agentes (tudo local e confiável); editar arquivos do projeto pelo daemon; interface dentro do app do Claude.

## 4. Arquitetura

```
┌─────────────── central.exe (um processo daemon por máquina) ─────────────┐
│ Interfaces    CLI · HTTP/JSON /v1 + SSE · MCP (stdio) · painel web       │  cascas finas
│ Serviço       operações únicas usadas por todas as interfaces            │
│ Processo      "sprint": papéis, ciclo do item, quem acorda quem,         │  opinativo,
│               cerimônias, escolha de tarefa                              │  parametrizado
│ Núcleo        projetos · agentes · mensagens · itens · travas ·          │  pequeno,
│               recursos · eventos · fatos · regras                        │  estável
│ Armazenamento SQLite por projeto + registro da máquina                   │
└──────────────────────────────────────────────────────────────────────────┘
     ▲ extensões do projeto: executáveis externos, contrato ext/v1 (JSON via stdin/stdout)
```

### Regras de arquitetura (o que evita refatorar)
1. **Interfaces sem lógica.** CLI, HTTP, MCP e painel chamam a camada de serviço. Interface nova nunca toca núcleo ou processo.
2. **O processo só usa o núcleo**, nunca SQL direto. Mudar o processo não muda o esquema.
3. **O núcleo não conhece domínio.** Nada de LyricCut, GPU, vídeo ou git no núcleo. GPU é um recurso configurado; catálogo e e2e são extensões; backlog em arquivos é um adaptador.
4. **Tudo é evento.** Cada mudança grava um evento imutável na mesma transação; consultas de métricas, linha do tempo e retro são derivadas dos eventos.
5. **Contratos públicos versionados desde o início:** API `/v1`, `ext/v1` e o formato do `central.toml`. O esquema do banco é interno, com migrações automáticas e backup antes de migrar.

### Pacotes Go (fronteiras)
| Pacote | Responsabilidade | Depende de |
|---|---|---|
| `store` | SQLite, migrações, transações, backup | — |
| `core` | entidades do núcleo, eventos, cursores, travas, recursos | `store` |
| `process` | papéis, máquina de estados do item, sprint, cerimônias, `proxima` | `core` |
| `contextpack` | montagem do `brief` | `core`, `process` |
| `ext` | executar extensões, contrato `ext/v1` | `core` |
| `adapters/files` | indexar itens a partir de arquivos markdown com frontmatter (git ou pasta) | `core` |
| `meter` | rodar medidores de recursos, guardar amostras | `core` |
| `service` | operações públicas (uma função por comando) | todos acima |
| `api` | HTTP `/v1`, SSE | `service` |
| `cli` | comandos, saída JSON/texto, autostart do daemon | `api` (cliente HTTP) |
| `mcp` | servidor MCP stdio | `api` (cliente HTTP) |
| `web` | painel (HTML/JS estático embutido via `embed`) | `api` |
| `config` | leitura e validação do `central.toml` | — |

### Processo e dados na máquina
- **Daemon:** um por máquina, escuta só em `127.0.0.1` (porta padrão `7420`, configurável). A primeira chamada da CLI sobe o daemon em segundo plano se ele não responder; um arquivo de trava em `%LOCALAPPDATA%\central\daemon.lock` impede dois daemons. É o único processo que escreve nos bancos.
- **Dados:** `%LOCALAPPDATA%\central\` (no Linux/macOS, o diretório de dados do usuário):
  - `registro.db`: projetos conhecidos (id, raiz, nome, último acesso);
  - `projetos/<id>.db`: um banco por projeto (SQLite WAL, `busy_timeout`);
  - `backups/`: cópia horária via API de backup do SQLite, mantendo as últimas 48, e cópia antes de cada migração;
  - `logs/`: log do daemon com rotação.
- **Queda do daemon:** a próxima chamada o sobe de novo. Nada de estado só em memória além de conexões de espera, que os clientes refazem.

### Identidade
- **Projeto:** `central.toml` na raiz do repositório (versionado) com `id` estável. Para resolver o projeto, a CLI sobe da pasta atual até achar `central.toml`. Num worktree git, usa a raiz do worktree; como o `central.toml` é versionado, todos os worktrees acham o mesmo `id`.
- **Agente:** a pasta raiz onde o comando roda (worktree ou pasta). `central entrar --vaga <VAGA>` liga a vaga a essa pasta; depois disso, qualquer comando rodado ali sabe quem é o agente. `--como <agente>` sobrescreve. Uma pasta pertence a no máximo um agente ativo por projeto.

## 5. Núcleo

### Entidades
| Entidade | Campos principais | Garantias |
|---|---|---|
| `agente` | id, vaga, papel, pasta, modelo, esforço, estado (`ativo`, `ocioso`, `trabalhando`, `sumido`, `encerrado`), status livre, último batimento | todo comando do agente renova o batimento; sem batimento por `sumido_apos` (padrão 30 min) e sem execução longa ativa → `sumido` |
| `mensagem` | id sequencial, de, para (agente, papel ou `TODOS`), tipo, ref, corpo, `responder_ate`, `responde_a` | cada destinatário tem um cursor próprio; entrega exatamente uma vez por `caixa`; pendência de resposta rastreada até chegar uma mensagem com `responde_a` |
| `item` | id (ex.: `T-112`), tipo (`tarefa`, `decisao`, `missao`), estado, dono, prioridade, campos livres (JSON), ref externa | transições só pelo processo; reserva atômica |
| `trava` | nome, dono, motivo, lease | expira sem batimento do dono; nunca fica presa |
| `recurso` | nome, dimensões, reservado, medidor, regras de fila | ver §5.3 |
| `concessao` | recurso, dono, declarado, tipo, prioridade, início, prazo | ver §5.3 |
| `evento` | seq, quando, quem, tipo, ref, dados (JSON) | imutável, gravado na mesma transação da mudança |
| `fato` | chave, valor (JSON), quem, quando, motivo | histórico completo; a leitura devolve o mais recente |
| `regra` | id (ex.: `L-025`), origem, texto, regra prática, `aplica` (papéis), estado (`ativa`, `no_manual`, `no_codigo`, `aposentada`), comando que a aplica (quando `no_codigo`) | histórico de mudanças por evento |
| `execucao` | rótulo, agente, comando, início, fim, código de saída, final do log, amostras de recursos | criada por `central rodar` |

### 5.1 Acordar na hora
`central esperar [--ate 30m] [--tipos ...]` é uma long-poll HTTP. Ela termina assim que existir algo endereçado ao agente:
- mensagem para ele, para o papel dele ou para `TODOS`;
- item que entrou num estado atendido pelo papel dele (regra do processo, §6);
- vez dele num recurso pedido com `--esperar`;
- cerimônia que o envolve;
- prazo vencido de uma pendência de resposta dele.

Todo motivo de acordar que não é uma mensagem de outro agente é materializado como **mensagem do remetente reservado `CENTRAL`**, endereçada a quem precisa acordar: concessão ativada (`recurso.concedida`), prazo de concessão vencido (`recurso.prazo`), concessão liberada por expiração (`recurso.liberada`), concessão sem uso (`recurso.sem_uso`), prazo de resposta vencido (`resposta.prazo`) e agente sumido (`agente.sumiu`, ao papel `GERENTE` se houver alguém vivo nele, senão a `TODOS`). Assim o `esperar` tem um mecanismo só (o cursor da caixa), e o `motivo` é derivado do tipo da primeira não lida: se ela vem de `CENTRAL`, é a parte antes do ponto (`recurso.concedida` → `recurso`); senão, `mensagem`.

A saída é um JSON com `motivo`, os dados mínimos e `sugestao` (o próximo comando). Se o daemon reiniciar, o cliente reconecta; o cursor garante que nada se perde. Sem nada novo até `--ate`, sai com `motivo: "tempo"` e código 0.

**Uso no Claude Code:** no fim de um ciclo ocioso, o agente roda `central esperar --ate 30m` em segundo plano (o app re-invoca a sessão quando o comando termina) e agenda um despertar de reserva de 30 min. **Plano B**, se o M0 mostrar que a re-invocação não funciona dentro do `/loop`: `central esperar --ate 3m` em primeiro plano dentro do ciclo.

### 5.2 Idempotência e conflitos
- Comandos de escrita aceitam `--chave <texto>`. Repetir com a mesma chave devolve o resultado original sem duplicar.
- Duas reservas simultâneas do mesmo item: o daemon serializa; uma vence e a outra recebe código 2 (`conflito`) com o dono atual.

### 5.3 Recursos com dimensões
```toml
[recursos.gpu]
dimensoes   = { vram_mb = 8192, uso_pct = 100 }
reservado   = { vram_mb = 2200, uso_pct = 15 }     # "zero" da máquina
medidor     = { comando = "nvidia-smi --query-gpu=utilization.gpu,memory.used --format=csv,noheader,nounits", mapa = ["uso_pct", "vram_mb"], intervalo = "15s" }
margem      = { vram_mb = 512 }
teto_medido = { vram_mb = 0.95 }                    # fração da capacidade; acima disso, nada novo é concedido
prazo_max   = "30m"                                 # salvo prioridade que permita mais
prioridades = ["versao", "bug-p0", "missao", "executor", "pesquisa"]   # da maior para a menor

[recursos.gpu.tipos]
e2e-nivel1 = { vram_mb = 5000, uso_pct = 80 }       # valor inicial conservador, até haver histórico
e2e-nivel2 = { exclusivo = true }
studio     = { vram_mb = 1500, uso_pct = 30 }
```

- **Pedido:** `central recurso pedir gpu --tipo e2e-nivel1 [--vram 4500 --uso 80] [--exclusivo] --prioridade executor --min 25 --motivo "..." [--esperar]`.
- **Estimativa:** sem números explícitos, usa o maior pico medido nas últimas 5 execuções do mesmo tipo com +20 % de folga; sem histórico, o valor do tipo no `central.toml`; sem isso, exclusivo.
- **Admissão**, em ordem de prioridade e depois de chegada. Um pedido entra quando, ao mesmo tempo:
  1. **declarado:** soma do declarado das concessões ativas + o pedido ≤ dimensões − reservado;
  2. **medido:** última leitura do medidor + o pedido + margem ≤ dimensões, e a leitura está abaixo de `teto_medido`;
  3. **exclusivo:** um pedido exclusivo só entra sem nenhuma outra concessão, e nada entra enquanto ele estiver ativo.
- **Backfill:** um pedido que não é o primeiro da fila pode entrar antes se couber nas três contas e se o seu prazo não for passar do início estimado do primeiro da fila.
- **Sem medidor** (recurso de outro tipo), só vale a conta declarada.
- **Aprendizado:** cada concessão guarda o pico medido durante a sua vigência (quando há só uma concessão ativa, o pico é atribuído a ela; com várias, o pico conjunto é registrado e não alimenta a estimativa individual).
- **Concessão sem uso:** se o uso medido ficar abaixo de `reservado + 5` por 5 min numa concessão de tipo que declarou uso, o dono recebe uma mensagem e o painel destaca. Cobre o fallback silencioso para CPU.
- **Prazo vencido:** o dono recebe aviso; 10 min depois, sem batimento, a concessão é liberada. O daemon **nunca** mata processos.
- **Soltar:** `central recurso soltar gpu`; também ao fim de `central rodar` que a pediu.

### 5.4 Execuções longas
`central rodar --rotulo "gate completo" [--recurso gpu --tipo e2e-nivel1] -- <comando>` executa o comando como filho, mantém o batimento do agente, grava a `execucao` (duração, código de saída, últimas 50 linhas) e, se houver recurso, pede e solta a concessão e guarda as amostras do medidor do período. O código de saída é o do comando.

## 6. Processo "sprint"

### 6.1 Papéis
Padrão: `GERENTE`, `PLANEJADOR`, `PESQUISADOR`, `EXECUTOR`, `REVISOR`, `VALIDADOR`, `INTEGRADOR`. O `central.toml` pode renomear (`EXECUTOR = "FIXER"`), desligar papéis e fixar vagas. Com um papel desligado, o fluxo pula a etapa dele.

### 6.2 Ciclo do item `tarefa`
| De → para | Quem pode | Condição verificada | Acorda |
|---|---|---|---|
| `proposta → pronta` | PLANEJADOR | campos obrigatórios do `central.toml` presentes | — |
| `pronta → em_andamento` | EXECUTOR | sem reserva; sem conflito de `arquivos` com itens `em_andamento`; não depois do congelamento | — |
| `em_andamento → concluida` | dono | `ponta` informada | REVISOR (ou INTEGRADOR, se não houver REVISOR) |
| `em_andamento → bloqueada` | dono | nota obrigatória | PLANEJADOR |
| `bloqueada → pronta` | automático | — | — |
| `concluida → em_revisao` | REVISOR | — | — |
| `em_revisao → aprovada` | REVISOR | `ponta` revisada = `ponta` atual do item | INTEGRADOR |
| `em_revisao → rejeitada` | REVISOR | motivo obrigatório | dono (EXECUTOR) |
| `rejeitada → em_andamento` | dono | — | — |
| `aprovada → integrada` | INTEGRADOR | `commit` informado | VALIDADOR, se `visivel_ao_usuario`; PLANEJADOR (aceite) |
| `integrada → validada` | VALIDADOR | resultado (`ligada`, `sem_caminho`, `sem_efeito`, `quebrada`) | PLANEJADOR, se não for `ligada` |
| qualquer → `em_andamento` | dono | a `ponta` mudou depois de `concluida` | REVISOR (revisão anulada) |

`missao` usa o mesmo ciclo por marco (`marco` no item). `decisao` tem estados `rascunho → chave1 → aprovada | objecao | humano`, com `veto` possível a qualquer momento pelo humano; aprovada ou vetada acorda o PLANEJADOR.

### 6.3 `central proxima`
Devolve a melhor tarefa para o agente e o motivo. Ordem:
1. bugs P0 (qualquer sprint);
2. tarefas da sprint atual;
3. prioridade P0 → P3;
4. dentro de cada grupo, `bloqueante` antes, `bug` antes dos outros tipos.

Filtros: sem reserva; sem conflito de `arquivos` com itens `em_andamento`; área do agente primeiro (preferência, não trava); tarefas com `bloqueio` só para a vaga sênior quando ela existe, nunca com duas notas `bloqueio`; depois do congelamento, nenhuma; com o recurso exigido saturado, prefere tarefas que não o usam; sem tarefa da sprint, uma com `folga = true`. Sem resultado, devolve `nada` com o motivo e avisa o PLANEJADOR se a causa for fila vazia.

### 6.4 Sprint e cerimônias
- `central sprint abrir --expediente 22:00-07:00` (GERENTE) calcula e grava abertura, check-in (meio do caminho até o congelamento), escopo (fim − 75 min), congelamento (fim − 60), corte (fim − 30), retro (fim − 20) e fim. Os deslocamentos ficam no `central.toml`.
- Em cada horário, o daemon emite o evento da cerimônia e acorda os papéis envolvidos com uma mensagem do tipo `cerimonia` que diz o que fazer.
- **Encerramento antecipado:** o daemon avalia as condições (nada `em_andamento`, nada `concluida`/`aprovada` esperando, todos os executores ociosos) a cada minuto. Quando valem por 10 min seguidos, avisa o GERENTE. A confirmação do PLANEJADOR e do INTEGRADOR e a decisão continuam com os agentes; `central sprint antecipar` regrava os horários.
- Métricas da sprint são consultas sobre eventos: entregues, retrabalho, bloqueadas, latência entre estados, ociosidade por vaga, uso de recursos, tokens do brief.

### 6.5 Adaptador de itens em arquivos
```toml
[itens.arquivos]
ref      = "develop"                       # branch git; vazio = pasta de trabalho
pastas   = { proposta = "backlog/proposed", pronta = "backlog/todo", integrada = "backlog/done" }
padrao   = "T-*.md"
campos   = { obrigatorios = ["titulo", "area", "prioridade", "tipo", "criterio_de_pronto"] }
```
O daemon lê o frontmatter dos arquivos (via `git show`/`git ls-tree` quando `ref` é uma branch) quando a ponta da branch muda (verificada a cada 30 s) e cria ou atualiza o item. O daemon nunca escreve nesses arquivos. O estado vivo (reserva, revisão) é do daemon; o estado da pasta só pode avançar o item (ex.: arquivo aparece em `done/` → `integrada`), nunca voltar.

### 6.6 Comandos do processo (CLI)
`entrar`, `sair`, `brief`, `status`, `caixa`, `enviar`, `responder`, `pendentes`, `esperar`, `proxima`, `reservar`, `concluir`, `bloquear`, `revisar`, `aprovar`, `rejeitar`, `integrar`, `validar`, `item criar|ver|listar`, `decisao abrir|chave|veto`, `sprint abrir|ver|antecipar|encerrar`, `recurso pedir|soltar|ver`, `trava|soltar`, `rodar`, `fato get|set|historico`, `regra listar|criar|estado`, `saude`, `orfas`, `metricas`, `id <prefixo>`, `init`, `doctor`, `painel`, `mcp`, `importar-locks`, `exportar-locks`.

Códigos de saída: `0` ok, `1` erro inesperado, `2` conflito, `3` não encontrado, `4` recusado por regra (o JSON traz qual), `5` daemon indisponível.

## 7. Contexto: `brief`
`central brief [--curto] [--max-tokens 8000]` monta, nesta ordem:
1. **Quem você é:** vaga, papel, projeto, pasta e variáveis (raiz do projeto, raiz principal, dados).
2. **Agora:** relógio da sprint, a reserva atual e o próximo passo dela.
3. **Pendências:** mensagens não lidas (resumidas) e respostas devidas.
4. **Cartão do papel.**
5. **Núcleo do projeto** (`.central/nucleo.md`).
6. **Regras ativas** cujo `aplica` contém o papel.
7. **Fatos** marcados como relevantes para o papel.
8. **Mapa de leitura:** uma linha por doc, com "leia quando".

`--curto` = blocos 1–3. Ao estourar o teto, corta do fim (8 → 7 → 6), nunca os blocos 1–5, e grava um evento `brief-truncado` que aparece no painel para o GERENTE. O tamanho de cada brief (estimado em tokens como bytes/4) é registrado por papel.

**Fontes:** cartões padrão embutidos no binário e sobrescritos por `.central/papeis/<PAPEL>.md`; núcleo em `.central/nucleo.md`; mapa de leitura em `[[leitura]]` do `central.toml` (`caminho`, `resumo`, `quando`, `papeis`); regras e fatos no daemon. Regras com estado `no_codigo` não entram no brief.

## 8. Painel
`central painel` abre `http://127.0.0.1:7420/`. Atualização ao vivo por SSE. HTML/JS sem framework, embutido no binário.

- **Máquina:** um cartão por projeto registrado (sprint, agentes ativos, filas, alertas).
- **Por projeto:** Agora (vagas, status, execução em curso, batimento) · Quadro (colunas do §6.2 com tempo parado; limite configurável por coluna) · Conversa (chat filtrável, pendências destacadas, envio como `HUMANO`) · Linha do tempo (Gantt por vaga; séries medidas × declaradas por recurso, com a concessão de cada período) · Sprint (relógio, métricas do §6.4) · Decisões (pendentes de chave ou do humano; botões de veto e resposta) · abas de extensão.
- **O que o painel escreve:** mensagem do humano, veto e resposta de decisão, arquivos soltos numa pasta de entrada de extensão. Nada mais.

## 9. Extensões (`ext/v1`)
```toml
[[extensoes]]
nome    = "exemplos"
comando = "resources/runtime/python.exe -I .central/ext/exemplos.py"
timeout = "60s"
cli     = ["listar", "importar", "status"]
aba     = { titulo = "Exemplos", visao = "galeria", acao = "listar" }
entrada = { pasta = "artifacts/entrada", ao_chegar = "importar" }
```
- **Chamada:** o daemon executa `comando` com a raiz do projeto como pasta atual e escreve uma linha JSON no stdin: `{"versao":"ext/v1","acao":"importar","args":{...},"projeto":{...},"agente":{...}}`.
- **Resposta:** uma linha JSON no stdout: `{"ok":true,"dados":...,"eventos":[{"tipo":"exemplo-novo","ref":"...","dados":{...}}],"formulario":{...}}`. `formulario` (opcional) faz o painel pedir campos ao humano e chamar a mesma ação de novo com eles.
- **Visões do painel:** `tabela`, `galeria` (imagem + legenda), `serie` (pontos no tempo), `formulario`.
- **Eventos de extensão** acordam papéis conforme `[acordar]` no `central.toml` (ex.: `exemplo-novo = ["PESQUISADOR"]`).
- **Isolamento:** timeout, saída limitada a 1 MB, erro mostrado na aba sem afetar o resto. Extensões só rodam a partir do `central.toml` do projeto.

### Extensões do LyricCut (ficam no repositório do LyricCut)
1. **`exemplos`.** Arquivo solto em `artifacts/entrada/` ou arrastado na aba:
   - um vídeo sozinho → vídeo-meta;
   - vídeo + áudio + `.txt` com o mesmo nome base → conjunto de base;
   - pede por formulário nome, perfil e o que ensina (vídeo-meta) ou idioma (conjunto);
   - move para `artifacts/goal_videos/` ou `artifacts/base_{videos,music,lyrics}/` sem recodificar;
   - grava os metadados no catálogo versionado (a ampliação de `.claude/skills/goal-videos/videos.json` com uma seção de conjuntos de base);
   - chama `goal_videos.py preprocess --video <id>`; emite `exemplo-novo`.
   - Tarefa associada no LyricCut: `scripts/e2e_cyberpunk.py --conjunto <id>` lendo o catálogo em vez dos caminhos fixos.
2. **`e2e`.** `registrar` e `achar --commit` (substitui `$LOCKS\e2e`), aba `serie` com gates por commit e sprint.

## 10. Interfaces e distribuição
- **HTTP `/v1`:** um endpoint por operação do serviço (`POST /v1/projetos/{id}/mensagens`, `GET /v1/projetos/{id}/esperar?...`, etc.) e `GET /v1/eventos` (SSE). JSON em UTF-8.
- **MCP:** `central mcp` (stdio) expõe as operações do agente como ferramentas, com os mesmos nomes da CLI.
- **Plugin do Claude Code** (no mesmo repositório, pasta `integrations/claude-code/`): skill curta de uso, configuração do servidor MCP, hooks opcionais (recusar escrita nos arquivos de estado antigos; `central brief --curto` após resumo automático).
- **Build:** Go ≥ 1.23, `CGO_ENABLED=0`, SQLite via `modernc.org/sqlite`, TOML via `github.com/pelletier/go-toml/v2`, MCP via o SDK oficial em Go. Binários para Windows (alvo principal), Linux e macOS.
- **Versão:** semântica. Quebra de `/v1`, `ext/v1` ou do formato do `central.toml` exige versão maior e caminho novo (`/v2`) com o anterior mantido por pelo menos uma versão maior.
- **`central init`** cria `central.toml` mínimo e `.central/`; **`central doctor`** verifica daemon, porta, versão dos contratos, bancos e extensões.

## 11. Testes
- **`store`/`core`:** unidade; concorrência (50 goroutines reservando o mesmo item: exatamente uma vence; 50 pedidos de recurso: soma concedida nunca passa da capacidade); cursor sem perda nem duplicata depois de reiniciar o daemon no meio de `esperar`; idempotência por `--chave`; migrações a partir de cada versão anterior do esquema.
- **`process`:** cada linha da tabela do §6.2 (permitida e recusada); `proxima` com quadros sintéticos cobrindo cada regra do §6.3; sprint inteira com relógio simulado (cerimônias, congelamento, encerramento antecipado).
- **Recursos:** admissão declarada × medida com medidor falso; backfill; exclusivo; concessão sem uso; prazo vencido; aprendizado do pico.
- **Contratos:** arquivos golden de `/v1` e `ext/v1`; uma extensão de exemplo testada no CI.
- **Ponta a ponta:** subir o daemon, rodar um cenário de dois agentes pela CLI (enviar, esperar, reservar, concluir, revisar, integrar) e conferir eventos e métricas.

## 12. Marcos
| Marco | Conteúdo | Pronto quando |
|---|---|---|
| **M0 piloto** | daemon mínimo com autostart, `enviar`, `caixa`, `esperar` | duas sessões do app Claude em `/loop` trocam mensagens e a destinatária acorda em < 10 s sem polling (ou o plano B do §5.1 fica decidido com medição) |
| **M1 velocidade** | núcleo completo; processo (§6); recursos com medidor (§5.3); `rodar`; adaptador de arquivos; `importar-locks`/`exportar-locks`; `doctor`/`init` | uma sprint do LyricCut roda só com a Central, e as métricas do §2 de latência são medidas |
| **M2 contexto** | `brief`, cartões, regras, fatos, mapa de leitura; MCP; plugin do Claude Code | brief do LyricCut < 15 mil tokens por papel |
| **M3 visibilidade** | painel completo (§8) | o humano acompanha uma sprint inteira pelo painel |
| **M4 extensões** | `ext/v1`, visões, pasta de entrada; extensões `exemplos` e `e2e` do LyricCut | Digimon e um vídeo-meta novo adicionados sem editar arquivo à mão |
| depois | lançar e supervisionar agentes | — |

## 13. Migração do LyricCut
1. Instalar `central.exe`; `central init` no CortesEditor; preencher `central.toml` (papéis com os nomes atuais, áreas, recurso `gpu`, adaptador de `backlog/`, mapa de leitura).
2. `central importar-locks .git/lyriccut-agents` (reservas, pontos, canal, fila da GPU, registros de e2e, ids) e `central regra importar docs/agents/LICOES.md`.
3. Reescrever o manual em `.central/nucleo.md` + `.central/papeis/*.md` + docs de referência (decisão do humano; manual é alçada humana, §2 item 7 do manual atual).
4. A troca vale a partir da **abertura** de uma sprint (L-013). Se algo falhar no meio, `central exportar-locks` regrava o estado no formato antigo e o time volta aos arquivos.

## 14. Riscos
| Risco | Mitigação |
|---|---|
| A re-invocação por comando em segundo plano não funcionar dentro do `/loop` | M0 mede antes de tudo; plano B do §5.1 |
| Porta ocupada ou bloqueada por antivírus | porta configurável; `doctor` detecta |
| Banco corrompido | WAL, transações curtas, backup horário e antes de migrar, um banco por projeto |
| Go é linguagem nova no ecossistema do humano | pacotes pequenos com fronteiras da §4, testes fortes, interfaces sem lógica |
| O processo embutido não servir a outro projeto | papéis renomeáveis e desligáveis; parâmetros no `central.toml`; o núcleo continua utilizável sozinho pela API |
| Agentes voltarem ao jeito antigo por hábito | cartões curtos que só ensinam a CLI; hook que recusa a escrita no formato antigo |
