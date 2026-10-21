# Central M4a (extensões `ext/v1`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (execução nativa). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Um projeto pluga ferramentas próprias na Central sem mudar o binário (spec §9). Uma extensão é um executável declarado no `central.toml`, chamado com uma linha JSON no stdin e respondendo com uma linha JSON no stdout. Ela ganha subcomandos de CLI, uma aba no painel e uma pasta de entrada, e os eventos dela acordam papéis.

**Spec:** `docs/spec.md` §9 (e §8 para as abas, §11 para o golden de contrato).

**Architecture:** O pacote `internal/ext` só fala o contrato: monta o pedido, roda o comando com prazo e limite de saída e lê a resposta. Ele não toca no banco. O `service` decide quem pode chamar o quê, grava os eventos pelo `core` (numa transação, com as mensagens de acordar) e cuida da pasta de entrada. O painel só usa rotas `/v1`.

## Global Constraints
- **Contrato `ext/v1`:**
  - Pedido: `{"versao":"ext/v1","acao":A,"args":{...},"projeto":{"id","nome","raiz"},"agente":{"vaga","papel"}|null}`.
  - Resposta: `{"ok":bool,"dados":any,"eventos":[{"tipo","ref","dados"}],"formulario":{...}|null,"erro":{"mensagem"}|null}`.
  - A resposta é a última linha não vazia do stdout, para o que vier antes poder ser log.
  - Mudar esses campos exige `ext/v2`.
- **Execução:**
  - O comando roda num shell (`cmd /S /C` no Windows, `sh -c` no resto), com a raiz registrada do projeto como pasta atual.
  - O ambiente recebe `CENTRAL_EXT=ext/v1`, `CENTRAL_PROJETO=<id>` e `CENTRAL_ADDR`.
  - O prazo padrão é 60 s e o máximo, 60 min. O cancelamento mata a árvore de processos.
  - O stdout passa de 1 MB? É erro. O stderr fica guardado até 64 KB, para a mensagem de erro.
- **Permissões:** as ações chamáveis de uma extensão são as do `cli`, mais `aba.acao` e `entrada.ao_chegar`. Valem para agentes (pela pasta) e para o humano (pelo painel, como `HUMANO`). Outra ação dá 4/recusado.
- **Eventos de extensão:**
  - Viram eventos `ext.<tipo>` (o `quem` é a vaga, `HUMANO` ou `CENTRAL`). O `tipo` aceita `[a-z0-9][a-z0-9._-]*`.
  - Cada papel listado em `[acordar]` para o tipo recebe uma mensagem do sistema com tipo `ext.<tipo>`, a ref do evento e um corpo com `dados.resumo` ou um texto padrão. O motivo do `esperar` fica `ext`.
  - Eventos e mensagens entram numa transação só.
  - Toda chamada também grava `ext.chamada` (`{nome, acao, ok, ms}`), sem acordar ninguém.
- **Isolamento:** um erro de extensão vira erro da chamada (5xx não; `invalido`/`recusado`/`interno` conforme o caso) e não afeta o resto. Há uma chamada por vez por extensão e projeto (mutex).
- **Pasta de entrada:**
  - O daemon olha a pasta a cada 2 s. Um arquivo está pronto quando o tamanho e o horário não mudam entre duas leituras.
  - Os prontos são enviados juntos para `ao_chegar`, com `{"arquivos":[caminhos relativos à raiz]}`. Um arquivo já entregue (nome, tamanho, horário) não é entregue de novo enquanto o daemon estiver no ar.
  - Nomes começando com `.` são ignorados. O upload do painel grava `.central-parcial-<nome>` e renomeia no fim.
- **Formulário:**
  - Quando a resposta traz `formulario`, a chamada que veio da pasta de entrada fica pendente em memória (por projeto e extensão) até o humano responder pela aba.
  - Responder chama a mesma ação com os args originais mais os campos.
  - Se o daemon reiniciar, os arquivos ainda estão na pasta e o ciclo recomeça sozinho.

## Tasks

### Task 1: `central.toml` — `[[extensoes]]` e `[acordar]`
- `ExtensaoSec{Nome, Comando, Timeout Duration, CLI []string, Aba *AbaSec{Titulo, Visao, Acao}, Entrada *EntradaSec{Pasta, AoChegar}}` e `Acordar map[string][]string`.
- Validação:
  - nome em `[a-z][a-z0-9-]*`, sem repetir nem colidir com um comando da CLI (lista `ComandosReservados` no pacote `config`);
  - comando não vazio;
  - timeout entre 1 s e 60 min (padrão 60 s);
  - visão em `tabela|galeria|serie|formulario`;
  - `aba.acao` e `entrada.ao_chegar` obrigatórios quando a aba ou a entrada existem;
  - pasta relativa e sem `..`;
  - chaves de `[acordar]` no formato de tipo, com ao menos um papel cada.
- [ ] Testes: `TestExtensoesValidas`, `TestExtensaoInvalida` (casos em tabela) e `TestAcordarInvalido`.

### Task 2: `internal/shell` e `internal/ext`
- Mover `shellCommand` do `service` para `internal/shell.Command(ctx, comando)`; o medidor passa a usar o pacote novo.
- `ext.Pedido`, `ext.Resposta`, `ext.Evento`, `ext.Formulario` (campos livres: `json.RawMessage` para dados e formulário).
- `ext.Rodar(ctx, Execucao{Comando, Pasta, Ambiente, Timeout}, Pedido) (Resposta, error)` aplica prazo, limite de saída e a regra da última linha. Os erros são `ErrPrazo`, `ErrSaidaGrande` e `ErrResposta` (JSON inválido ou sem `ok`), sempre com o stderr resumido.
- Exemplo `examples/ext/eco` (Go): devolve os args em `dados`, emite um evento quando `args.evento` vem, pede formulário quando `args.pedir` vem, dorme quando `args.dormir` vem e despeja saída quando `args.inundar` vem.
- [ ] Testes (compilam o eco uma vez em `TestMain`): `TestPedidoGolden` (`testdata/pedido.golden.json`), `TestRespostaEco`, `TestPrazoMataArvore`, `TestSaidaGrande` e `TestRespostaInvalida`.

### Task 3: chamada pelo serviço, eventos e API
- `core.ExtEvents(ctx, quem, []ExtEvent{Tipo, Ref, Dados, Para []string, Corpo})` grava os eventos `ext.<tipo>` e as mensagens numa transação, mais `RecordEvent` para o `ext.chamada`.
- `service.Extensao(ctx, projetoID, pasta, nome, acao, args)`:
  - pasta vazia é o HUMANO;
  - aplica as permissões e o mutex;
  - devolve `ExtResultado{Dados, Formulario, Eventos}`.
- `service.Extensoes(ctx, projetoID)` lista as declaradas (nome, cli, aba, entrada).
- Rotas:
  - `GET /v1/projetos/{id}/extensoes`;
  - `POST /v1/projetos/{id}/extensoes/{nome}/{acao} {pasta, args}`.
- [ ] Testes de serviço/API: `TestExtensaoChamadaEAcorda` (o evento acorda o papel de `[acordar]`, o motivo é `ext` e o `ext.chamada` é gravado), `TestExtensaoAcaoRecusada`, `TestExtensaoErroNaoQuebra` (erro do eco vira erro da chamada, e a próxima funciona) e `TestExtensaoHumano`.

### Task 4: CLI, MCP e doctor
- `central <extensao> <acao> [--k v ...] [posicionais]` (resolvido pelo `central.toml` local quando o comando não é da Central): flags viram args de texto, posicionais viram `args._`, `-` lê o stdin para `args.stdin`.
- Ferramenta MCP `extensao {nome, acao, args}`.
- `doctor`: para cada extensão do `central.toml` local, confere se o primeiro termo do comando existe (no PATH ou relativo à raiz).
- [ ] Testes: `TestCLIExtensao` (e2e com o eco), `TestMCPExtensao` e `TestDoctorExtensao`.

### Task 5: pasta de entrada e formulários pendentes
- `service.RunEntradas(ctx, intervalo)` no daemon. Pendências em memória: `service.Pendentes(projetoID, nome)` e `service.ResponderFormulario(ctx, projetoID, nome, idPendente, campos)`.
- Rotas:
  - `PUT /v1/projetos/{id}/extensoes/{nome}/entrada/{arquivo}`: corpo cru, até 8 GiB, nome só de base;
  - `GET /v1/projetos/{id}/extensoes/{nome}/pendentes`;
  - `POST /v1/projetos/{id}/extensoes/{nome}/pendentes/{pid}`.
- [ ] Testes: `TestEntradaEntregaArquivoEstavel`, `TestEntradaNaoRepete`, `TestEntradaIgnoraParcial`, `TestUploadNomeInvalido` e `TestFormularioPendente` (o eco pede, o humano responde, a ação roda de novo com os campos).

### Task 6: abas de extensão no painel
- O painel lista as extensões com aba e cria a rota `#/p/<id>/ext/<nome>`. Ao abrir, chama `aba.acao` (com botão "Atualizar").
- Visões:
  - `tabela`: `dados` é uma lista de objetos ou `{colunas, linhas}`;
  - `galeria`: lista de `{imagem, legenda, detalhe}`;
  - `serie`: `{series:[{nome, pontos:[{x, y}]}]}`, com x sendo um horário ou um número;
  - `formulario`: formulário de `campos`.
- Pendências da pasta de entrada aparecem no topo da aba com o formulário delas. Quando há entrada, a aba aceita soltar arquivos, que vão para o upload.
- `GET /v1/projetos/{id}/arquivo?caminho=` serve imagens (`png`, `jpg`, `jpeg`, `gif`, `webp`) de dentro da raiz, recusa `..`, caminho absoluto e link para fora, e manda `X-Content-Type-Options: nosniff`.
- [ ] Testes: `TestArquivoDentroDaRaiz` e `TestArquivoForaDaRaiz`. Verificação visual com o eco configurado como extensão de demonstração.

### Task 7: documentação
- README: seção "Extensões" (contrato, exemplo de `central.toml`, CLI, aba, entrada e `[acordar]`) e `docs/ext-v1.md` com o contrato completo.
