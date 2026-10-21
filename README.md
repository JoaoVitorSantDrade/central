# Central

Coordenação local de times de agentes: mensagens, tarefas, travas, recursos e painel. Um binário só (`central.exe`), sem dependências de runtime. Funciona com Claude Code ou qualquer agente que rode comandos.

- Design: [docs/spec.md](docs/spec.md) · contrato das extensões: [docs/ext-v1.md](docs/ext-v1.md) · mudanças: [CHANGELOG.md](CHANGELOG.md)
- Planos: [M0](docs/plans/m0-piloto.md) · [M1a](docs/plans/m1a-nucleo.md) · [M1b](docs/plans/m1b-processo.md) · [M2](docs/plans/m2-contexto.md) · [M3](docs/plans/m3-painel.md) · [M4a](docs/plans/m4a-extensoes.md) · [M4b](docs/plans/m4b-lyriccut.md) · [V1](docs/plans/v1-release.md) · registros em [docs/registro/](docs/registro/)

Estado: **1.2.0**. A versão inclui:
- o núcleo: mensagens que acordam na hora, respostas com prazo, travas, recursos com dimensões e medidor, `rodar`, fatos, backups e `doctor`;
- o processo de sprint: papéis, tarefas, `proxima`, decisões de duas chaves, cerimônias, adaptador do backlog em git, saúde e métricas;
- o brief por papel e as regras do time;
- MCP e plugin do Claude Code;
- o painel local;
- as extensões `ext/v1`.

Os contratos `/v1`, `ext/v1` e o formato do `central.toml` estão congelados. Quebrar qualquer um deles exige uma versão maior (spec §10).

## Instalar

**Pelo Claude Code** (o plugin já traz a Central):
```
/plugin marketplace add JoaoVitorSantDrade/central
/plugin install central@central
```
O plugin instala o binário no primeiro uso, ou quando uma sessão começa, e o mantém na versão do plugin. A partir daí, o `central` funciona no Bash dos agentes e no seu terminal. Detalhes em [integrations/claude-code](integrations/claude-code).

**Sem o Claude Code, no Windows** (PowerShell):
```powershell
irm https://github.com/JoaoVitorSantDrade/central/releases/latest/download/install.ps1 | iex
```
**Sem o Claude Code, no Linux e no macOS:**
```bash
curl -fsSL https://github.com/JoaoVitorSantDrade/central/releases/latest/download/install.sh | sh
```

O instalador:
1. baixa o binário da sua plataforma e confere o SHA256;
2. roda `central instalar`, que copia o binário para uma pasta do usuário (`%LOCALAPPDATA%\Programs\central` ou `~/.local/bin`), sem pedir administrador;
3. põe essa pasta no PATH do usuário (no Linux e no macOS, só avisa se ela faltar);
4. se o `claude` estiver no PATH, instala o plugin do Claude Code (skill, MCP e hook).

Depois, abra um terminal novo e rode `central versao`.

- **Atualizar:** rode `central atualizar`. Ele baixa a última release, confere o SHA256 e se troca. O daemon antigo é substituído sozinho no próximo comando, e os bancos migram com cópia antes.
- **Desinstalar:** rode `central desinstalar`. Ele tira o binário, a pasta do PATH e o plugin. Os dados (`%LOCALAPPDATA%\central`) ficam.
- **Sem o instalador:** baixe o binário da [release](https://github.com/JoaoVitorSantDrade/central/releases/latest) e rode `.\central-windows-amd64.exe instalar`. Com Go 1.26+, `go install github.com/JoaoVitorSantDrade/central/cmd/central@latest`.
- **Encerrar o daemon:** `central parar`. O próximo comando sobe outro.

## Uso rápido

```powershell
go build -o bin/central.exe ./cmd/central      # Go 1.26+, CGO_ENABLED=0 (ou use o binário da release)

cd C:\caminho\do\projeto
central init --id meu-projeto --nome "Meu projeto"    # cria central.toml (versione-o)

# em cada worktree/pasta de agente:
central entrar --vaga FIXER1 --papel FIXER
central enviar REVISOR1 concluida --ref T-12 "T-12 pronta na ponta abc123"
Get-Content nota.md | central enviar PLANEJADOR duvida -   # corpo longo pelo stdin
central caixa                 # mensagens novas (marca como lidas)
central esperar --ate 30m     # bloqueia até chegar algo para você
```

- O daemon sobe sozinho na primeira chamada, escuta só em `127.0.0.1:7420` e guarda os dados em `%LOCALAPPDATA%\central\`. `CENTRAL_ADDR` e `CENTRAL_HOME` sobrescrevem.
- **PowerShell 5.1:** para canalizar texto com acento, rode antes `$OutputEncoding = [Text.UTF8Encoding]::new($false)` (o PowerShell 7 já usa UTF-8). O BOM e as quebras de linha finais são removidos; um corpo que não é UTF-8 é recusado com código 4.
- O destinatário pode ser uma vaga (`REVISOR1`), um papel (`REVISOR`) ou `TODOS`.
- A saída é sempre uma linha JSON. Códigos de saída: `0` ok, `1` erro, `2` conflito, `3` não encontrado, `4` recusado/inválido, `5` daemon indisponível.

### Travas
```powershell
central trava package.json --motivo "atualizando dependências"   # outra vaga recebe 2 com o dono
central soltar package.json
central travas
```
Uma trava não tem prazo próprio: vale enquanto o dono não estiver `sumido` nem `encerrado`. Só o dono solta.

### Recursos
Declare o recurso no `central.toml` (o `entrar` envia o arquivo ao daemon; com ele inválido, sai com 4 e a linha do erro, e a configuração anterior continua valendo):

```toml
[recursos.gpu]
dimensoes   = { vram_mb = 8192, uso_pct = 100 }
reservado   = { vram_mb = 2200, uso_pct = 15 }     # o "zero" da máquina
medidor     = { comando = "nvidia-smi --query-gpu=utilization.gpu,memory.used --format=csv,noheader,nounits", mapa = ["uso_pct", "vram_mb"], intervalo = "15s" }
prazo_max   = "30m"
prioridades = ["versao", "executor", "pesquisa"]   # da maior para a menor
sem_uso     = { dimensao = "uso_pct", limite = 5, apos = "5m" }

[recursos.gpu.tipos]
e2e-nivel1 = { vram_mb = 5000, uso_pct = 80 }
e2e-nivel2 = { exclusivo = true }
```

```powershell
central recurso pedir gpu --dims vram_mb=4500,uso_pct=80 --prioridade executor --min 25 --motivo "gate" --esperar 30m
central recurso pedir gpu --tipo e2e-nivel1 --esperar 30m    # estimativa pelo histórico do tipo
central recurso pedir gpu --exclusivo sim --esperar 30m
central recurso ver gpu        # ativas, fila, última leitura do medidor
central recurso soltar gpu
```

- As dimensões são genéricas: `--dims k=v,k=v` com os nomes declarados em `dimensoes`.
- Sem `--dims`, a estimativa é o maior pico das últimas 5 execuções do tipo com +20 %; sem histórico, o valor do tipo; sem isso, exclusivo.
- Um pedido entra quando cabe na conta declarada, na medida (última leitura + pedido) e na regra do exclusivo. Com `--esperar`, o comando bloqueia até a vez; se o tempo acabar ainda na fila, sai com 2 (`ainda_na_fila`) e o pedido continua na fila (repita com a mesma `--chave` para voltar a esperar por ele).
- `--min` acima do teto (`prazo_max`, padrão 30 min) é recusado com 4: divida o trabalho. Prazo vencido gera aviso; 10 min depois, sem batimento, a concessão é liberada. O daemon nunca mata processos.
- O medidor roda no shell do sistema, na raiz do projeto, a cada `intervalo` (mínimo 1 s); a saída é uma linha com um número por coluna, na ordem de `mapa`.

### `rodar`
```powershell
central rodar --rotulo "gate completo" --recurso gpu --tipo e2e-nivel1 -- python -m pytest tests/e2e
```
Roda o comando como filho (a saída dele vai ao stderr), renova o batimento, grava a execução (duração, código, últimas 50 linhas) e, com `--recurso`, pede a concessão antes (espera até 60 min) e a solta no fim, mesmo com erro ou Ctrl+C. O código de saída é o do comando.

### Fatos
```powershell
central fato set e2e.base '{"commit":"abc123"}' --motivo "gate verde"   # JSON ou texto
central fato get e2e.base
central fato historico e2e.base
central fatos
central id T --minimo 115      # contador atômico: T-115, depois T-116...
```

### Respostas com prazo
```powershell
central enviar REVISOR1 pergunta --responder 30m "pode revisar a T-12?"
central pendentes                    # "devo" (a responder) e "esperam" (que enviei)
central responder 42 "sim, revisado" # fecha a pendência da mensagem 42
```
Com o prazo vencido sem resposta, o destino recebe `resposta.prazo` (uma vez).

### Mensagens do sistema
Tudo que precisa acordar um agente vira mensagem do remetente `CENTRAL`. O `esperar` devolve em `motivo` a parte do tipo antes do ponto (`recurso`, `resposta`, `agente`); para mensagens de agentes, `mensagem`.

| Tipo | Para | `ref` | Quando |
|---|---|---|---|
| `recurso.concedida` | dono do pedido | id da concessão | o pedido saiu da fila e a concessão está ativa |
| `recurso.prazo` | dono | id da concessão | o prazo da concessão venceu |
| `recurso.liberada` | dono | id da concessão | 10 min depois do prazo, sem batimento, a concessão foi liberada |
| `recurso.sem_uso` | dono | id da concessão | o uso medido, menos o reservado, ficou abaixo de `sem_uso.limite` por `sem_uso.apos` |
| `resposta.prazo` | destino da pergunta | seq da pergunta | o prazo do `--responder` venceu sem resposta |
| `agente.sumiu` | `GERENTE` (se houver um vivo), senão `TODOS` | vaga | a vaga ficou sem batimento por `agentes.sumido_apos` (padrão 30 min) |
| `item.<estado>` | o papel da próxima etapa (ou o dono) | id do item | o item mudou de estado (veja Processo) |
| `cerimonia.<nome>` | os papéis da cerimônia | id da sprint | abertura, checkin, escopo, congelamento, corte, retro, fim |
| `sprint.encerrar_antes` | `GERENTE` | id da sprint | 10 min seguidos sem trabalho (spec §6.4) |
| `fila.vazia` | `PLANEJADOR` | vaga | um executor pediu tarefa e não havia (no máximo a cada 30 min) |
| `decisao.<estado>` | `GERENTE` ou `PLANEJADOR` | id da decisão | chave 1, aprovada, objeção, humano, vetada |

### `doctor`
```powershell
central doctor
```
Confere sem subir o daemon: se ele responde, a pasta de dados, o `central.toml` do projeto, cada medidor (roda o comando e mostra a leitura) e a porta. Sai com 0 se tudo está ok, 1 senão. O daemon também faz backup dos bancos a cada hora (mantém 48) e gira o próprio log acima de 10 MB.

### No Claude Code (sessão em `/loop`)
Sem trabalho, rode `central esperar --ate 30m` pelo Bash em segundo plano (`run_in_background: true`), agende um despertar de reserva de 30 min e termine o turno. A sessão é re-invocada quando chega uma mensagem (mediana de 2,5 s no piloto).

## Processo (sprint)

O processo é opinativo (spec §6): papéis, ciclo de vida, escolha de tarefa, sprint e decisões. O projeto ajusta pelo `central.toml`:

```toml
[papeis]
nomes = { EXECUTOR = "FIXER" }   # o nome que os agentes usam no --papel
desligados = ["VALIDADOR"]       # o fluxo pula a etapa (sem REVISOR, o INTEGRADOR aprova)
senior = ["FIXER_SENIOR"]        # vagas que pegam primeiro as tarefas que já bloquearam

[itens]
obrigatorios = ["titulo", "area", "prioridade", "criterio_de_pronto"]

[itens.arquivos]                 # o backlog em arquivos markdown com frontmatter YAML (o daemon nunca escreve neles)
ref = "develop"                  # branch git; vazio = pasta de trabalho
padrao = "T-*.md"
intervalo = "30s"
pastas = { proposta = "backlog/proposed", pronta = "backlog/todo", integrada = "backlog/done" }
mapa = { arquivos = "arquivos_provaveis" }
recurso_se = { usa_gpu = "gpu" }

[sprint]                         # antes do fim; o check-in fica no meio até o congelamento
escopo = "75m"
congelamento = "60m"
corte = "30m"
retro = "20m"
```

### Ciclo de uma tarefa
```powershell
central item criar --titulo "corrigir x" --area core --prioridade P1 --arquivos backend/x.py --criterio "teste passa"
central item pronto T-001          # PLANEJADOR
central proxima                    # EXECUTOR: a melhor tarefa e o porquê
central reservar T-001
central concluir T-001 --ponta <hash>      # acorda o REVISOR
central bloquear T-001 "não reproduz"     # volta a pronta com a nota; acorda o PLANEJADOR
central revisar T-001
central aprovar T-001 --ponta <hash>       # a ponta precisa ser a atual; acorda o INTEGRADOR
central rejeitar T-001 "falta teste"       # acorda o dono
central integrar T-001 --commit <hash>     # acorda o VALIDADOR (se visível) e o PLANEJADOR
central validar T-001 ligada               # ligada, sem_caminho, sem_efeito ou quebrada
central reabrir T-001                      # o dono mudou a ponta: a revisão é anulada
central item listar --estado concluida
```
Estado errado sai com 2; papel errado, com 4. Missões (`--tipo missao`) voltam ao dono a cada marco integrado; `integrar --final sim` fecha.

`proxima` segue a spec §6.3: bugs P0, depois a sprint, depois a folga, depois o resto por prioridade (bloqueante e bug primeiro). A área do `entrar --area` é preferência. Conflito de arquivos com uma tarefa em andamento e o congelamento tiram a tarefa da lista. Recurso ocupado vai para o fim.

### Sprint e cerimônias
```powershell
central sprint abrir --expediente 22:00-07:00   # GERENTE; o id é S-AAAAMMDD (-2, -3… no mesmo dia)
central sprint ver
central sprint antecipar                        # congelamento e corte agora, fim em 30 min
central sprint encerrar
```
Cada cerimônia acorda os papéis dela com `cerimonia.<nome>`. Com 10 min seguidos sem trabalho (nada em andamento ou esperando QA e entrega, executores ociosos), o GERENTE recebe `sprint.encerrar_antes`.

### Decisões de duas chaves
```powershell
central decisao abrir --titulo "ligar X" "contexto, opções, riscos"   # PLANEJADOR (chave 1)
central decisao chave D-001 --acordo sim                              # GERENTE (chave 2)
central decisao chave D-001 --objecao "conflita com a missão"
central decisao revisar D-001 "versão menor"                          # última rodada; a 2ª objeção vai ao humano
central decisao veto D-001 "muda o rumo"                              # o humano, em qualquer estado
central decisao responder D-001 --aprovar sim "pode seguir"           # o humano, nas escaladas
```

### Saúde, órfãs e métricas
```powershell
central saude                    # sumidos, itens parados além do limite, fila de QA, concessões vencidas, respostas atrasadas
central orfas [--liberar sim]    # itens com dono encerrado ou sumido há mais de 12 h
central metricas [--sprint S]    # integradas, rejeições, bloqueios, latências, tarefas por executor/hora, uso de recursos, ociosidade
```

### Migração do `$LOCKS` do LyricCut
```powershell
central importar-locks C:\caminho\do\repo\.git\lyriccut-agents   # vagas, mensagens (como lidas), reservas, e2e (fatos), ids, sprint aberta
central exportar-locks C:\volta                                     # o caminho de volta, no formato do manual
```
Reimportar não duplica nada.

## Brief e contexto

Em vez de ler manuais inteiros, o agente lê o brief do papel dele (spec §7):
```powershell
central brief --md            # os 8 blocos: quem você é, agora, pendências, seu papel, núcleo, regras, fatos, mapa de leitura
central brief --curto --md    # só os blocos 1–3, depois de um resumo automático
```
- **Cartão do papel:** `.central/papeis/<PAPEL>.md` (pelo nome do projeto, depois pelo canônico); sem arquivo, vale o cartão embutido.
- **Núcleo:** `.central/nucleo.md` (objetivo, invariantes, alçadas, "nunca"). Entra no brief de todos.
- **Regras:** as lições ativas que se aplicam ao papel (abaixo).
- **Fatos:** `[brief.fatos]` diz quais chaves cada papel vê com o valor (`EXECUTOR = ["e2e.base*"]`); sem isso, o bloco lista só as chaves.
- **Mapa de leitura:** `[[leitura]]` com `caminho`, `resumo`, `quando` e `papeis`; o brief mostra uma linha por documento e o agente só abre o que precisar.
- O teto é `[brief] max_tokens` (padrão 8000, tokens ≈ bytes/4). Ao estourar, os blocos 8, 7 e 6 são cortados nessa ordem; os blocos 1–5 nunca. Todo brief grava `brief.gerado` (com os tokens), e o corte grava `brief.truncado`.

### Regras (lições do time)
```powershell
central regra importar docs/agents/LICOES.md        # a tabela | ID | Origem | Lição | Regra prática | Quem aplica | Estado |
central regra criar --id L-029 --aplica FIXER,REVISOR --pratica "o teste novo falha na develop"
central regra estado L-009 no_codigo --comando "central status"   # virou código: sai do brief
central regra listar --papel FIXER --estado ativa
```
Estados: `ativa`, `no_manual`, `no_codigo` e `aposentada`. Só GERENTE e PLANEJADOR gravam. Os nomes de quem aplica são traduzidos para os papéis canônicos.

## Painel
```powershell
central painel              # sobe o daemon se preciso e abre http://127.0.0.1:7420/ no navegador
central painel --so-url sim # só imprime a URL
```
Um painel só para todos os projetos da máquina, servido pelo próprio daemon. Funciona offline, sem nada carregado de fora, em tela estreita e nos temas claro e escuro. A primeira tela tem um cartão por projeto: sprint e próxima cerimônia, vagas por estado, fila de QA e alertas. Cada projeto tem estas abas:

- **Agora:** as vagas (papel, estado, status, reserva e batimento, com as sumidas em vermelho), os alertas da `saude` e os recursos (medido × declarado × capacidade, concessões ativas e fila).
- **Quadro:** as colunas de proposta a validada. Os cartões parados acima do `[processo.limite_parado]` ficam em vermelho.
- **Conversa:** todas as mensagens, com filtro por vaga, papel ou item. As pendências de resposta aparecem destacadas, e as vencidas em vermelho. Há um formulário para mandar como **HUMANO**: a mensagem acorda o destino como qualquer outra.
- **Linha do tempo:** uma faixa por vaga colorida pelo estado (na última hora, nas últimas 6 h, nas últimas 24 h ou na sprint). Para cada recurso, a série medida por dimensão com o declarado das concessões por baixo.
- **Sprint:** as cerimônias (feitas e a próxima), as métricas e o tamanho do brief por papel.
- **Decisões:** as que estão em aberto, com notas. Dá para vetar qualquer uma e aprovar ou recusar as escaladas ao humano.

O painel se atualiza ao vivo por SSE (`GET /v1/projetos/{id}/stream`), no máximo uma vez por segundo, e não apaga o que você está digitando. Ele só lê pela API `/v1` e só escreve mensagem do humano, veto e resposta. O daemon recusa `Host` que não seja loopback, contra DNS rebinding, e escrita com `Origin` de outra página, contra CSRF. A vaga `HUMANO` é reservada, como `TODOS` e `CENTRAL`.

Para ler pela API (os mesmos dados do painel):

| Rota | O que traz |
|---|---|
| `GET /v1/projetos` | projetos com resumo |
| `GET /v1/projetos/{id}/mensagens?depois=&limite=` | histórico (padrão: 200 mais novas) e pendências em aberto |
| `GET /v1/projetos/{id}/eventos?depois=&tipos=&prefixos=&ref=&de=&ate=&limite=` | eventos e `ultimo` (o seq mais alto) |
| `GET /v1/projetos/{id}/recursos` | estado de cada recurso |
| `GET /v1/projetos/{id}/recursos/{nome}/amostras?de=&ate=` | leituras e concessões do período (padrão: 6 h) |
| `GET /v1/projetos/{id}/stream?depois=` | SSE com `id: <seq>` e um comentário de vida a cada 15 s |
| `POST /v1/projetos/{id}/humano/mensagens {para, tipo, corpo, ref}` | mensagem do humano |

## Extensões
Um projeto pluga ferramentas próprias sem mudar a Central: um programa qualquer declarado no `central.toml` recebe uma linha JSON no stdin e responde outra no stdout (contrato completo em [docs/ext-v1.md](docs/ext-v1.md)).
```toml
[[extensoes]]
nome    = "exemplos"
comando = "resources/runtime/python.exe -X utf8 -I .central/ext/exemplos.py"
timeout = "10m"
cli     = ["listar", "importar"]
aba     = { titulo = "Exemplos", visao = "galeria", acao = "listar" }
entrada = { pasta = "artifacts/entrada", ao_chegar = "importar" }

[acordar]
exemplo-novo = ["PESQUISADOR"]
```
- **CLI:** `central exemplos importar --nome digimon a.mp4`. As flags viram args de texto, os posicionais vão em `args._` e `-` lê o stdin para `args.stdin`.
- **MCP:** a ferramenta `extensao {nome, acao, args}`.
- **Painel:** uma aba por extensão com `aba`, nas visões `tabela`, `galeria`, `serie` ou `formulario`. Com `entrada`, a aba aceita arquivos arrastados, e um formulário pedido pela extensão fica pendente até o humano responder.
- **Pasta de entrada:** os arquivos estáveis vão juntos para `ao_chegar`, e a extensão os tira de lá.
- **Eventos:** a resposta pode trazer eventos; eles viram `ext.<tipo>` e acordam os papéis de `[acordar]`.
- **Isolamento:** o prazo vale para a árvore de processos inteira, e a saída é limitada a 1 MB. Um erro aparece na chamada (código `extensao`) sem afetar o resto.
- **`doctor`:** confere se o executável de cada extensão existe.

## MCP
`central mcp` serve as operações de agente pelo protocolo MCP (stdio), na pasta do projeto: `entrar`, `brief`, `caixa`, `enviar`, `responder`, `pendentes`, `esperar` (até 5 min por chamada), `status`, `agentes`, `proxima`, `reservar`, `concluir`, `bloquear`, `revisar`, `aprovar`, `rejeitar`, `integrar`, `validar`, `reabrir`, `item_ver`, `item_listar`, `item_criar`, `trava`, `soltar`, `recurso_pedir`, `recurso_soltar`, `fato_get`, `fato_set`, `saude` e `extensao`. Cada ferramenta roda o mesmo comando da CLI.

## Plugin do Claude Code
Em [integrations/claude-code](integrations/claude-code): a skill `central` (o ciclo do agente), o servidor MCP e um hook `SessionStart` que injeta `central brief --curto --md` depois de um resumo automático. Instale pelo marketplace deste repositório:
```
/plugin marketplace add JoaoVitorSantDrade/central
/plugin install central@central
```

## Desenvolvimento

```powershell
go test ./...
go vet ./...
```

## Licença

[MIT](LICENSE). Use, modifique e distribua à vontade, mantendo o aviso de copyright.
